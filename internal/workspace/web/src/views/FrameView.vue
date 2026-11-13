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
// from a page that loaded. For a documentation link of the lab (a resource
// tab) the server reads those headers first (GET /doclinks/frame): a site
// that refuses gets a notice with Open in browser instead of a blank frame.
// Any other docs tab, and a resource whose check failed, says next to Open
// in new window that a blank page means the site refuses.
import { computed, onMounted, ref } from 'vue'
import { getDocLinkFrame, isRunGone, isUnauthorized, type FrameCheck } from '../api'
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
const isResource = computed(() => props.tab.source === 'resource')
// A resource tab waits for the server's check; any other tab frames at once.
const check = ref<FrameCheck | 'checking'>(isResource.value ? 'checking' : 'unknown')
const refusedReason = ref('')

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

/** The frame is shown: a refused resource gets the notice instead. */
const framed = computed(() => !!url.value && check.value !== 'checking' && check.value !== 'refused')

function backToStart(): void {
  generation.value++
}

async function checkFrame(href: string): Promise<void> {
  emit('status', { tone: 'busy', label: 'Loading' })
  try {
    const res = await getDocLinkFrame(props.lab, href)
    check.value = res.frame
    refusedReason.value = res.reason ?? ''
  } catch (err) {
    if (isUnauthorized(err)) return emit('unauthorized')
    if (isRunGone(err)) return emit('runGone')
    check.value = 'unknown'
  }
  emit('status', check.value === 'refused' ? { tone: 'warn', label: 'Opens in browser only' } : { tone: 'ok', label: 'Docs' })
}

onMounted(() => {
  if (!url.value) {
    emit('status', { tone: 'error', label: 'Cannot show this page' })
    return
  }
  if (isResource.value) {
    void checkFrame(props.tab.url ?? '')
    return
  }
  emit('status', isDocs.value ? { tone: 'ok', label: 'Docs' } : { tone: 'ok', label: 'Page' })
})

defineExpose({
  focus: () => frame.value?.focus(),
})
</script>

<template>
  <div class="frame">
    <div class="frame__bar">
      <code class="frame__url small" :title="tab.url">{{ tab.url }}</code>
      <button v-if="url && framed" type="button" class="btn btn--sm btn--ghost" @click="backToStart">Back to start</button>
      <a
        v-if="url && check !== 'refused'"
        class="btn btn--sm btn--ghost"
        :href="url.href"
        target="_blank"
        rel="noopener noreferrer"
      >Open in new window</a>
      <p v-if="url && isDocs && check === 'unknown'" class="frame__note small muted">
        If this page stays blank, the site does not allow being shown inside another page: use
        <strong>Open in new window</strong>.
      </p>
    </div>
    <p v-if="url && check === 'checking'" class="view__empty muted" role="status">Opening {{ url.hostname }}…</p>
    <div v-else-if="url && check === 'refused'" class="frame__refused" role="status">
      <p class="eyebrow">Not embeddable</p>
      <h2 class="frame__refused-title">This site does not allow embedding</h2>
      <p class="muted">
        <strong>{{ url.hostname }}</strong> cannot be shown inside the workspace<template v-if="refusedReason">: {{ refusedReason }}</template>.
        Open it in your browser instead; the lab keeps running here.
      </p>
      <a class="btn btn--primary" :href="url.href" target="_blank" rel="noopener noreferrer">
        Open in browser<span class="visually-hidden"> (new browser tab)</span>
      </a>
    </div>
    <iframe
      v-else-if="framed"
      :key="generation"
      ref="frame"
      class="frame__iframe"
      :src="url?.href"
      :title="tab.title"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads"
      referrerpolicy="no-referrer"
    />
    <p v-else-if="!url" class="view__empty error" role="alert">This tab's address cannot be shown in the workspace.</p>
  </div>
</template>
