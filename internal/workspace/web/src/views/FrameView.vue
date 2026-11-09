<script setup lang="ts">
// A page shown in the tab: a page the lab serves on a loopback port forward
// (browser tabs) or a documentation site the lab marked as embeddable. The
// server lists exactly these origins in the page's CSP frame-src.
//
// The frame is sandboxed. It gets allow-same-origin so the lab's own app
// works (cookies, storage); that is safe only because its origin is never
// this page's origin, which is checked here as well as on the server.
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
const reloads = ref(0)

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

function reload(): void {
  reloads.value++
}

onMounted(() => {
  emit('status', url.value ? { tone: 'ok', label: 'Page' } : { tone: 'error', label: 'Cannot show this page' })
})

defineExpose({
  focus: () => frame.value?.focus(),
})
</script>

<template>
  <div class="frame">
    <div class="frame__bar">
      <code class="frame__url small" :title="tab.url">{{ tab.url }}</code>
      <button v-if="url" type="button" class="btn btn--sm btn--ghost" @click="reload">Reload</button>
      <a
        v-if="url"
        class="btn btn--sm btn--ghost"
        :href="url.href"
        target="_blank"
        rel="noopener noreferrer"
      >Open in new window</a>
    </div>
    <iframe
      v-if="url"
      :key="reloads"
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
