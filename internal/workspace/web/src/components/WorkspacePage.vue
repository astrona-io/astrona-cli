<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, toRef, watch } from 'vue'
import { useCountdown } from '../clock'
import { getQuestion, isRunGone, isUnauthorized, type Question, type Run } from '../api'
import { connectionLost, reconnectAttempt, resetConnectivity } from '../connectivity'
import type { ViewHandle, ViewStatus } from '../views/types'
import ConnectionLostDialog from './ConnectionLostDialog.vue'
import LabTabs from './LabTabs.vue'
import TopBar from './TopBar.vue'
import SplitDivider from './SplitDivider.vue'

const props = defineProps<{ run: Run }>()
const emit = defineEmits<{ unauthorized: []; runGone: []; end: [] }>()

// ---------------------------------------------------------------- time up

// Time-up never submits anything: it says so and offers Submit. A strict
// exam's terminal stops taking input on the server at the deadline.
const left = useCountdown(toRef(() => props.run.deadlineAt))
const timeUp = computed(() => props.run.status === 'expired' || left.value === 0)

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

// ---------------------------------------------------------------- view

// The question's own target, else the lab's own cluster.
const target = computed(() => {
  const want = question.value?.target
  return props.run.targets.find((t) => t.name === want) ?? props.run.targets[0] ?? null
})
const viewRef = ref<ViewHandle | null>(null)
const status = ref<ViewStatus>({ tone: 'busy', label: 'Connecting' })

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

function focusView(): void {
  viewRef.value?.focus()
}

function focusTask(): void {
  task.value?.focus()
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

watch(connectionLost, async (lost) => {
  window.clearTimeout(lostTimer)
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

    <TopBar :run="run" :status="status" @end="emit('end')" />

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

    <div class="workspace__row" :style="rowStyle">
      <section
        id="task-panel"
        ref="task"
        class="task"
        tabindex="-1"
        aria-labelledby="task-heading"
      >
        <div class="task__head">
          <p class="eyebrow">
            Task<template v-if="question?.points"> · {{ question.points }} points</template>
          </p>
          <h2 id="task-heading" class="task__title">{{ question?.title || run.title }}</h2>
          <p v-if="target" class="task__target small muted">
            Target: <code>{{ target.label || target.name }}</code>
          </p>
        </div>
        <p v-if="questionError" class="error" role="alert">Could not load the task: {{ questionError }}</p>
        <p v-else-if="!question" class="muted">Loading the task…</p>
        <!-- Rendered and sanitised on the server; innerHTML never runs scripts and CSP blocks inline handlers. -->
        <article v-else class="prose" v-html="question.html" />
      </section>

      <SplitDivider v-model="width" :min="MIN_WIDTH" :max="MAX_WIDTH" controls="task-panel" @commit="saveWidth" />

      <section class="view" aria-label="Lab">
        <LabTabs
          v-if="question || questionError"
          :key="run.runId"
          ref="viewRef"
          :lab="run.lab"
          :run-id="run.runId"
          :tabs="run.tabs"
          @status="status = $event"
          @leave="focusTask"
          @unauthorized="emit('unauthorized')"
          @run-gone="emit('runGone')"
        />
      </section>
    </div>
  </div>

  <ConnectionLostDialog v-if="lostShown" :lab="run.lab" :attempt="reconnectAttempt" />
  <p class="visually-hidden" aria-live="assertive">{{ announcement }}</p>
</template>
