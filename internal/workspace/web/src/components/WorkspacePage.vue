<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, toRef, watch } from 'vue'
import { useCountdown } from '../clock'
import { extendRun, getQuestion, isRunGone, isUnauthorized, type Question, type Run } from '../api'
import { envSpecs } from '../environment'
import { dismissLowTime, LOW_TIME_MS, lowTimeDismissed } from '../lowTime'
import { connectionLost, reconnectAttempt, resetConnectivity } from '../connectivity'
import { useCopyCode } from '../copyCode'
import type { ShownDocLink } from '../docLinks'
import type { ViewHandle, ViewStatus } from '../views/types'
import AboutDialog from './AboutDialog.vue'
import ConnectionLostDialog from './ConnectionLostDialog.vue'
import DocLinks from './DocLinks.vue'
import LabTabs from './LabTabs.vue'
import LabsDrawer from './LabsDrawer.vue'
import TopBar from './TopBar.vue'
import SplitDivider from './SplitDivider.vue'

// ending: the end, submit or remove dialog is working. Removing the
// environment closes the terminals on purpose, so their drop is not a lost
// connection and the "connection lost" dialog stays away.
const props = defineProps<{ run: Run; ending?: boolean }>()
const emit = defineEmits<{
  unauthorized: []
  runGone: []
  end: []
  home: []
  /** Switch to another lab running on this computer. */
  openLab: [lab: string]
  /** The server's run after a change made here (more time). */
  runUpdated: [run: Run]
}>()

// ---------------------------------------------------------------- time up

// Time-up never submits anything: it says so and offers Submit. A strict
// exam's terminal stops taking input on the server at the deadline.
const left = useCountdown(toRef(() => props.run.deadlineAt))
const timeUp = computed(() => props.run.status === 'expired' || left.value === 0)

// ---------------------------------------------------------------- low time

// At ten minutes or less: a banner that says what happens at the end, with
// Extend when the server allows it. Dismissed once per run, in this browser.
const dismissed = ref(lowTimeDismissed(props.run.runId))
watch(
  () => props.run.runId,
  (id) => (dismissed.value = lowTimeDismissed(id)),
)
const lowTime = computed(() => !timeUp.value && left.value !== null && left.value <= LOW_TIME_MS && !dismissed.value)
const minutesLeft = computed(() => (left.value === null ? 0 : Math.ceil(left.value / 60_000)))

function dismiss(): void {
  dismissed.value = true
  dismissLowTime(props.run.runId)
}

// ---------------------------------------------------------------- extend

const canExtend = computed(() => props.run.canExtend === true)
const extending = ref(false)
const extendError = ref<string | null>(null)

async function extend(): Promise<void> {
  if (extending.value) return
  extending.value = true
  extendError.value = null
  try {
    const r = await extendRun(props.run.lab, props.run.runId)
    emit('runUpdated', r)
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
    else if (isRunGone(err)) emit('runGone')
    else extendError.value = err instanceof Error ? err.message : String(err)
  } finally {
    extending.value = false
  }
}

// ---------------------------------------------------------------- task

const question = shallowRef<Question | null>(null)
const questionError = ref<string | null>(null)
const task = ref<HTMLElement>()

async function loadQuestion(): Promise<void> {
  const first = props.run.questions[0]
  if (!first) {
    questionError.value = 'This lab has no task text.'
    return
  }
  try {
    questionError.value = null
    question.value = await getQuestion(props.run.lab, first.id)
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
    else if (isRunGone(err)) emit('runGone')
    else questionError.value = err instanceof Error ? err.message : String(err)
  }
}
onMounted(loadQuestion)
watch(() => props.run.runId, loadQuestion)

const prose = ref<HTMLElement>()
const copyCode = useCopyCode(prose, () => question.value?.html)

// ---------------------------------------------------------------- view

// LabTabs: focus() like a view, and openResource() for a documentation badge.
const viewRef = ref<
  (ViewHandle & { openResource(link: ShownDocLink): void; paste(text: string): Promise<boolean> }) | null
>(null)
const status = ref<ViewStatus>({ tone: 'busy', label: 'Connecting' })
const aboutOpen = ref(false)
const labsOpen = ref(false)

// ---------------------------------------------------------------- free explore

// A playground has no task: its panel offers commands to try and what the
// lab is made of instead. Only what the server sent.
const noTask = computed(() => props.run.playground && props.run.questions.length === 0)
const suggested = computed(() => props.run.suggestedCommands ?? [])
const specs = computed(() => envSpecs(props.run.environment))
const pasteNote = ref('')

async function pasteCommand(command: string): Promise<void> {
  const ok = (await viewRef.value?.paste(command)) ?? false
  pasteNote.value = ok ? `Pasted into the terminal: ${command}. Press Enter to run it.` : 'No terminal to paste into.'
}

// ---------------------------------------------------------------- layout

// The task panel's share of the row, in percent; the terminal gets the rest.
// The key carries a version so a width saved under an older default does not
// keep the old split.
const WIDTH_KEY = 'astrona.workspace.taskWidth.v2'
const DEFAULT_WIDTH = 25
const MIN_WIDTH = 15
const MAX_WIDTH = 70

function storedWidth(): number {
  try {
    const raw = window.localStorage.getItem(WIDTH_KEY)
    const v = Number(raw)
    if (raw !== null && v >= MIN_WIDTH && v <= MAX_WIDTH) return v
  } catch {
    // storage unavailable
  }
  return DEFAULT_WIDTH
}

const width = ref(storedWidth())

function saveWidth(v: number): void {
  try {
    window.localStorage.setItem(WIDTH_KEY, String(v))
  } catch {
    // storage unavailable: the width lasts until reload
  }
}

// CSSOM custom property, which CSP allows (unlike a style attribute in markup).
const rowStyle = computed(() => ({ '--task-width': `${width.value}%` }))

// The task panel can be collapsed to a thin rail, for more terminal. The
// browser remembers the choice, like the width.
const COLLAPSED_KEY = 'astrona.workspace.taskCollapsed'

function storedCollapsed(): boolean {
  try {
    return window.localStorage.getItem(COLLAPSED_KEY) === '1'
  } catch {
    return false
  }
}

const collapsed = ref(storedCollapsed())
const expandButton = ref<HTMLButtonElement>()

async function setCollapsed(value: boolean): Promise<void> {
  collapsed.value = value
  try {
    window.localStorage.setItem(COLLAPSED_KEY, value ? '1' : '0')
  } catch {
    // storage unavailable: the choice lasts until reload
  }
  await nextTick()
  if (value) expandButton.value?.focus()
  else task.value?.focus()
}

function openResource(link: ShownDocLink): void {
  viewRef.value?.openResource(link)
}

function focusView(): void {
  viewRef.value?.focus()
}

function focusTask(): void {
  if (collapsed.value) void setCollapsed(false)
  else task.value?.focus()
}

// ---------------------------------------------------------------- connection

// While the server cannot be reached the workspace is covered (blurred and
// inert) by a dialog that cannot be dismissed. A blip shorter than this does
// not show it.
const SHOW_LOST_AFTER_MS = 1500
const lostShown = ref(false)
const announcement = ref('')
let lostTimer: number | undefined
let returnFocus: HTMLElement | null = null

watch([connectionLost, () => !!props.ending], async ([lost, ending]) => {
  window.clearTimeout(lostTimer)
  if (lost && ending) {
    // Hide it if it was already up: the end dialog says what happens.
    lostShown.value = false
    announcement.value = ''
    return
  }
  if (lost) {
    lostTimer = window.setTimeout(() => {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      lostShown.value = true
      announcement.value = 'Connection to the workspace server lost. Reconnecting.'
    }, SHOW_LOST_AFTER_MS)
    return
  }
  if (!lostShown.value) return
  lostShown.value = false
  announcement.value = 'Connection restored.'
  // Back to where the learner was, once the workspace is no longer inert.
  await nextTick()
  if (returnFocus?.isConnected && returnFocus !== document.body) returnFocus.focus({ preventScroll: true })
  else focusView()
  returnFocus = null
})

onBeforeUnmount(() => {
  window.clearTimeout(lostTimer)
  resetConnectivity()
})
</script>

<template>
  <div class="workspace" :class="{ 'workspace--lost': lostShown }" :inert="lostShown">
    <nav class="skip" aria-label="Skip links">
      <button type="button" class="skip__link" @click="focusView">Skip to terminal</button>
      <button type="button" class="skip__link" @click="focusTask">Skip to task</button>
    </nav>

    <TopBar
      :run="run"
      :status="status"
      :extending="extending"
      :extend-error="extendError"
      @end="emit('end')"
      @extend="extend"
    />

    <div v-if="lowTime" class="lowtime" role="status">
      <p class="lowtime__text">
        <strong>{{ minutesLeft }} {{ minutesLeft === 1 ? 'minute' : 'minutes' }} left.</strong>
        When time runs out, the lab is shut down.
      </p>
      <button type="button" class="lowtime__dismiss" @click="dismiss">Dismiss</button>
      <button v-if="canExtend" type="button" class="btn btn--soft btn--sm" :disabled="extending" @click="extend">
        {{ extending ? 'Extending…' : 'Extend time' }}
      </button>
    </div>

    <div v-if="timeUp && run.playground" class="timeup" role="status">
      <p class="timeup__text">
        <span class="timeup__title">The playground's time is up.</span>
        <span class="small">astrona removes it shortly. End it now to remove it yourself.</span>
      </p>
    </div>
    <div v-else-if="timeUp" class="timeup" role="status">
      <p class="timeup__text">
        <span class="timeup__title">Time is up.</span>
        <span class="small">
          Submit now to record your result; nothing is submitted automatically.
          <template v-if="run.rules.strict">
            In this strict exam a submission after the deadline cannot pass, and the terminal no longer takes input.
          </template>
        </span>
      </p>
      <button v-if="run.canSubmit" type="button" class="btn btn--sm btn--primary" aria-haspopup="dialog" @click="emit('end')">
        {{ run.mode === 'exam' ? 'Submit exam' : 'Submit lab' }}
      </button>
    </div>

    <div class="workspace__row" :class="{ 'workspace__row--collapsed': collapsed }" :style="rowStyle">
      <div v-if="collapsed" class="task-rail">
        <button
          ref="expandButton"
          type="button"
          class="task-rail__button"
          aria-controls="task-panel"
          aria-expanded="false"
          :title="run.playground ? 'Show the panel' : 'Show the task'"
          @click="setCollapsed(false)"
        >
          <span class="task-rail__icon" aria-hidden="true">›</span>
          <span class="task-rail__label" aria-hidden="true">{{ run.playground ? 'Show panel' : 'Show task' }}</span>
          <span class="visually-hidden">{{ run.playground ? 'Show the panel' : 'Show the task' }}</span>
        </button>
        <div class="task-rail__links">
          <button type="button" class="task-rail__link" title="All labs" aria-haspopup="dialog" @click="labsOpen = true">
            <span aria-hidden="true">▦</span>
            <span class="visually-hidden">All labs</span>
          </button>
          <button type="button" class="task-rail__link" title="About Astrona" aria-haspopup="dialog" @click="aboutOpen = true">
            <span aria-hidden="true">ⓘ</span>
            <span class="visually-hidden">About</span>
          </button>
        </div>
      </div>
      <section
        v-show="!collapsed"
        id="task-panel"
        ref="task"
        class="task"
        tabindex="-1"
        aria-labelledby="task-heading"
      >
        <div class="task__body">
          <div class="task__bar">
            <p v-if="run.playground" id="task-heading" class="eyebrow">Free explore</p>
            <p v-else id="task-heading" class="eyebrow">
              Task<template v-if="question?.points"> · {{ question.points }} points</template>
            </p>
            <button
              type="button"
              class="task__link task__collapse"
              aria-controls="task-panel"
              aria-expanded="true"
              :title="run.playground ? 'Hide the panel for more room' : 'Hide the task for more room'"
              @click="setCollapsed(true)"
            >
              <span class="task__link-icon" aria-hidden="true">‹</span>
              {{ run.playground ? 'Hide' : 'Hide task' }}
            </button>
          </div>
          <template v-if="run.playground">
            <div v-if="noTask" class="explore__intro">
              <h2 class="explore__title">No task — it's your sandbox</h2>
              <p class="muted small">This playground has no task document. Break things, try ideas, poke around the cluster.</p>
            </div>
            <section v-if="suggested.length" class="explore__block" aria-labelledby="explore-try">
              <h3 id="explore-try" class="explore__heading small muted">Try a command — click to paste</h3>
              <ul class="explore__cmds">
                <li v-for="c in suggested" :key="c.command">
                  <button type="button" class="cmd" :title="c.label || undefined" @click="pasteCommand(c.command)">
                    <span class="cmd__prompt" aria-hidden="true">$</span>
                    <span class="cmd__text mono">{{ c.command }}</span>
                    <span v-if="c.label" class="visually-hidden"> — {{ c.label }}</span>
                  </button>
                </li>
              </ul>
            </section>
            <section v-if="specs.length" class="explore__block" aria-labelledby="explore-lab">
              <h3 id="explore-lab" class="eyebrow">Your lab</h3>
              <dl class="specs">
                <template v-for="(sp, i) in specs" :key="i">
                  <dt class="specs__label">{{ sp.label }}</dt>
                  <dd class="specs__value mono">{{ sp.value }}</dd>
                </template>
              </dl>
            </section>
          </template>
          <DocLinks :links="run.docLinks ?? []" @open="openResource" />
          <p v-if="questionError && !noTask" class="error" role="alert">Could not load the task: {{ questionError }}</p>
          <p v-else-if="!question && !noTask" class="muted">Loading the task…</p>
          <!-- Rendered and sanitised on the server; innerHTML never runs scripts and CSP blocks inline handlers. -->
          <article
            v-else-if="question"
            ref="prose"
            class="prose"
            @click="copyCode.onClick"
            @keydown="copyCode.onKeydown"
            v-html="question.html"
          />
        </div>
        <footer class="task__foot">
          <button type="button" class="task__link" title="Labs running on this computer" aria-haspopup="dialog" @click="labsOpen = true">
            <span class="task__link-icon" aria-hidden="true">▦</span>
            All labs
          </button>
          <button type="button" class="task__link" aria-haspopup="dialog" @click="aboutOpen = true">
            <span class="task__link-icon" aria-hidden="true">ⓘ</span>
            About Astrona
          </button>
        </footer>
      </section>

      <SplitDivider v-if="!collapsed" v-model="width" :min="MIN_WIDTH" :max="MAX_WIDTH" controls="task-panel" @commit="saveWidth" />

      <section class="view" aria-label="Lab">
        <LabTabs
          v-if="question || questionError"
          :key="run.runId"
          ref="viewRef"
          :run="run"
          @status="status = $event"
          @leave="focusTask"
          @unauthorized="emit('unauthorized')"
          @run-gone="emit('runGone')"
        />
      </section>
    </div>
  </div>

  <AboutDialog v-if="aboutOpen" :lab="run.lab" @close="aboutOpen = false" />
  <LabsDrawer
    v-if="labsOpen"
    :run="run"
    @close="labsOpen = false"
    @open="emit('openLab', $event)"
    @home="emit('home')"
    @unauthorized="emit('unauthorized')"
  />
  <ConnectionLostDialog v-if="lostShown" :lab="run.lab" :attempt="reconnectAttempt" />
  <p class="visually-hidden" aria-live="assertive">{{ announcement }}</p>
  <p class="visually-hidden" aria-live="polite">{{ copyCode.announcement.value }}</p>
  <p class="visually-hidden" aria-live="polite">{{ pasteNote }}</p>
</template>
