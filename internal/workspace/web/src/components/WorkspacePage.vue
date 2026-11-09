<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, shallowRef, watch } from 'vue'
import { getQuestion, isRunGone, isUnauthorized, type Question, type Run } from '../api'
import { pickView, type ViewHandle, type ViewStatus } from '../views/types'
import TopBar from './TopBar.vue'
import SplitDivider from './SplitDivider.vue'

const props = defineProps<{ run: Run }>()
const emit = defineEmits<{ unauthorized: []; runGone: [] }>()

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

// The question's own target first, then the first target with a view this
// build can show.
const target = computed(() => {
  const want = question.value?.target
  const usable = props.run.targets.filter((t) => pickView(t.views))
  return usable.find((t) => t.name === want) ?? usable[0] ?? null
})
const view = computed(() => (target.value ? pickView(target.value.views) : null))
const viewComponent = computed(() => (view.value ? defineAsyncComponent(view.value.component) : null))
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
</script>

<template>
  <div class="workspace">
    <nav class="skip" aria-label="Skip links">
      <button type="button" class="skip__link" @click="focusView">Skip to terminal</button>
      <button type="button" class="skip__link" @click="focusTask">Skip to task</button>
    </nav>

    <TopBar :run="run" :status="status" />

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

      <section class="view" aria-label="Terminal">
        <component
          :is="viewComponent"
          v-if="viewComponent && target && (question || questionError)"
          :key="`${run.runId}/${target.name}`"
          ref="viewRef"
          :lab="run.lab"
          :run-id="run.runId"
          :target="target.name"
          @status="status = $event"
          @leave="focusTask"
          @unauthorized="emit('unauthorized')"
          @run-gone="emit('runGone')"
        />
        <p v-else-if="!target" class="view__empty muted" role="status">This lab has no terminal to show.</p>
      </section>
    </div>
  </div>
</template>
