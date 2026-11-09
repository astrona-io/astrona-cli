<script setup lang="ts">
// A page shown in the tab: a page the lab serves on a loopback port forward
// (browser tabs) or a documentation URL (docs tabs). The server lists exactly
// these origins in the page's CSP frame-src.
//
// The frame is sandboxed. It gets allow-same-origin so the lab's own app
// works (cookies, storage); that is safe only because its origin is never
// this page's origin, which is checked here as well as on the server.
//
// "Back to start" recreates the frame on the tab's configured URL: the
// frame's own history is cross-origin and cannot be read or rewound, so a
// fresh frame is the only reliable reset, wherever the learner navigated.
//
// A site that refuses to be framed (X-Frame-Options, CSP frame-ancestors)
// leaves the frame blank, and a cross-origin page cannot tell that apart
// from a page that loaded. A docs tab therefore always says so next to
// Open in new window.
import { computed, onMounted, ref } from 'vue'
import type { ViewProps, ViewStatus } from './types'

const props = defineProps<ViewProps>()
const emit = defineEmits<{
  status: [ViewStatus]
  leave: []
  unauthorized: []
  runGone: []
}>()

const frame = ref<HTMLIFrameElement>()
const generation = ref(0)
const isDocs = computed(() => props.tab.kind === 'docs')

const url = computed<URL | null>(() => {
  try {
    const u = new URL(props.tab.url ?? '')
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
    // Never frame the workspace itself: the framed page could read the
    // session token.
    if (u.origin === window.location.origin) return null
    return u
  } catch {
    return null
  }
})

function backToStart(): void {
  generation.value++
}

onMounted(() => {
  const ok: ViewStatus = isDocs.value ? { tone: 'ok', label: 'Docs' } : { tone: 'ok', label: 'Page' }
  emit('status', url.value ? ok : { tone: 'error', label: 'Cannot show this page' })
})

defineExpose({
  focus: () => frame.value?.focus(),
})
</script>

<template>
  <div class="frame">
    <div class="frame__bar">
      <code class="frame__url small" :title="tab.url">{{ tab.url }}</code>
      <button v-if="url" type="button" class="btn btn--sm btn--ghost" @click="backToStart">Back to start</button>
      <a
        v-if="url"
        class="btn btn--sm btn--ghost"
        :href="url.href"
        target="_blank"
        rel="noopener noreferrer"
      >Open in new window</a>
      <p v-if="url && isDocs" class="frame__note small muted">
        If this page stays blank, the site does not allow being shown inside another page: use
        <strong>Open in new window</strong>.
      </p>
    </div>
    <iframe
      v-if="url"
      :key="generation"
      ref="frame"
      class="frame__iframe"
      :src="url.href"
      :title="tab.title"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads"
      referrerpolicy="no-referrer"
    />
    <p v-else class="view__empty error" role="alert">This tab's address cannot be shown in the workspace.</p>
  </div>
</template>
