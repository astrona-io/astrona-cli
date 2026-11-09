<script setup lang="ts">
// A documentation tab. Three forms:
//   - a Markdown document shipped in the lab: rendered and sanitised on the
//     server, like the task;
//   - a documentation URL the lab marked embeddable: framed (FrameView);
//   - any other documentation URL: many documentation sites forbid being
//     framed, so the tab offers to open it in a separate window. Nothing is
//     proxied.
import { onMounted, ref } from 'vue'
import { getTabDoc, isRunGone, isUnauthorized, type TabDoc } from '../api'
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

async function load(): Promise<void> {
  problem.value = null
  emit('status', { tone: 'busy', label: 'Loading' })
  try {
    doc.value = await getTabDoc(props.lab, props.tab.name)
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

function openWindow(): void {
  if (!props.tab.url) return
  window.open(props.tab.url, '_blank', 'popup,width=1100,height=800,noopener,noreferrer')
}

onMounted(() => {
  if (props.tab.document) void load()
  else if (!props.tab.embed) emit('status', { tone: 'ok', label: 'Docs' })
})

defineExpose({
  focus: () => (frame.value ? frame.value.focus() : root.value?.focus()),
})
</script>

<template>
  <FrameView
    v-if="!tab.document && tab.embed"
    ref="frame"
    :lab="lab"
    :run-id="runId"
    :tab="tab"
    @status="emit('status', $event)"
  />
  <div v-else ref="root" class="docs" tabindex="-1">
    <template v-if="tab.document">
      <div v-if="problem" class="docs__problem" role="alert">
        <p class="error">Could not load the document: {{ problem }}</p>
        <button type="button" class="btn btn--sm btn--primary" @click="load">Try again</button>
      </div>
      <p v-else-if="!doc" class="muted">Loading the document…</p>
      <!-- Rendered and sanitised on the server; innerHTML never runs scripts and CSP blocks inline handlers. -->
      <article v-else class="prose" v-html="doc.html" />
    </template>
    <div v-else class="docs__external">
      <p class="eyebrow">Documentation</p>
      <h2 class="docs__title">{{ tab.title }}</h2>
      <p class="muted">
        Many documentation sites do not allow being shown inside another page, so this one opens in its own window.
        Keep it next to the workspace while you work.
      </p>
      <p><code class="small">{{ tab.url }}</code></p>
      <p>
        <button type="button" class="btn btn--primary" @click="openWindow">Open the documentation</button>
      </p>
    </div>
  </div>
</template>
