<script setup lang="ts">
// A documentation tab. Two forms:
//   - a Markdown document: one shipped in the lab (a lab tab), the task, the
//     prerequisites or the solution guide (tabs the learner opened from the
//     + menu); rendered and sanitised on the server, like the task;
//   - a documentation URL: framed (FrameView), with Back to start and Open in
//     new window. A site that refuses framing stays blank; FrameView says so
//     and offers the window. Nothing is proxied.
import { onMounted, ref } from 'vue'
import { getQuestion, getSolution, getTabDoc, isRunGone, isUnauthorized, type TabDoc } from '../api'
import { useCopyCode } from '../copyCode'
import FrameView from './FrameView.vue'
import type { ViewHandle, ViewProps, ViewStatus } from './types'

const props = defineProps<ViewProps>()
const emit = defineEmits<{
  status: [ViewStatus]
  leave: []
  unauthorized: []
  runGone: []
}>()

const root = ref<HTMLElement>()
const frame = ref<ViewHandle | null>(null)
const doc = ref<TabDoc | null>(null)
const problem = ref<string | null>(null)
const prose = ref<HTMLElement>()
const copyCode = useCopyCode(prose, () => doc.value?.html)

async function fetchDoc(): Promise<TabDoc> {
  const { tab } = props
  switch (tab.source) {
    case 'task': {
      const q = await getQuestion(props.lab, tab.questionId ?? '')
      return { name: tab.name, title: q.title, html: q.html }
    }
    case 'prerequisites':
      return { name: tab.name, title: tab.title, html: tab.html ?? '' }
    case 'solution': {
      const s = await getSolution(props.lab)
      return { name: tab.name, title: s.title, html: s.html }
    }
    default:
      return getTabDoc(props.lab, tab.name)
  }
}

async function load(): Promise<void> {
  problem.value = null
  emit('status', { tone: 'busy', label: 'Loading' })
  try {
    doc.value = await fetchDoc()
    emit('status', { tone: 'ok', label: 'Docs' })
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
    else if (isRunGone(err)) emit('runGone')
    else {
      problem.value = err instanceof Error ? err.message : String(err)
      emit('status', { tone: 'error', label: 'No document' })
    }
  }
}

onMounted(() => {
  if (props.tab.document) void load()
})

defineExpose({
  focus: () => (frame.value ? frame.value.focus() : root.value?.focus()),
})
</script>

<template>
  <FrameView
    v-if="!tab.document"
    ref="frame"
    :lab="lab"
    :run-id="runId"
    :tab="tab"
    @status="emit('status', $event)"
    @unauthorized="emit('unauthorized')"
    @run-gone="emit('runGone')"
  />
  <div v-else ref="root" class="docs" tabindex="-1">
    <div v-if="problem" class="docs__problem" role="alert">
      <p class="error">Could not load the document: {{ problem }}</p>
      <button type="button" class="btn btn--sm btn--primary" @click="load">Try again</button>
    </div>
    <p v-else-if="!doc" class="muted">Loading the document…</p>
    <!-- Rendered and sanitised on the server; innerHTML never runs scripts and CSP blocks inline handlers. -->
    <article
      v-else
      ref="prose"
      class="prose"
      @click="copyCode.onClick"
      @keydown="copyCode.onKeydown"
      v-html="doc.html"
    />
    <p class="visually-hidden" aria-live="polite">{{ copyCode.announcement.value }}</p>
  </div>
</template>
