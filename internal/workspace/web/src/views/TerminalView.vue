<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import '@xterm/xterm/css/xterm.css'
import { closeTerminal, isRunGone, isUnauthorized, listTerminals, openTerminal } from '../api'
import { serverBack, terminalReconnecting, terminalSettled } from '../connectivity'
import { mirrorInlineStyles } from '../terminal/cspStyles'
import { TerminalConnection, type LinkState } from '../terminal/connection'
import { fontFamily, fontsReady, readTheme } from '../terminal/theme'
import type { ViewProps, ViewStatus } from './types'

const props = defineProps<ViewProps>()
const emit = defineEmits<{
  status: [ViewStatus]
  leave: []
  unauthorized: []
  runGone: []
}>()

const host = ref<HTMLDivElement>()
const link = ref<LinkState>({ kind: 'connecting' })
const problem = ref<string | null>(null)
const opening = ref(false)
const term = shallowRef<Terminal>()
let fit: FitAddon | undefined
let conn: TerminalConnection | undefined
let stopMirror: (() => void) | undefined
let resizeObserver: ResizeObserver | undefined
let schemeQuery: MediaQueryList | undefined
let frame = 0
let unmounted = false
// Keyboard focus moves into the terminal when it first connects (after
// Start, or on opening the page) and after the user asks to reattach. A
// reconnect after a dropped connection leaves focus where the user put it.
let focusOnConnect = true

const notice = computed<{ text: string; action?: string } | null>(() => {
  if (problem.value) return { text: problem.value, action: 'Try again' }
  if (opening.value) return { text: 'Starting a terminal… The first time on this machine this can take a minute.' }
  switch (link.value.kind) {
    case 'connecting':
      return { text: 'Connecting to the terminal…' }
    case 'reconnecting':
      return { text: `Connection lost. Reconnecting… (attempt ${link.value.attempt})` }
    case 'takenOver':
      return { text: 'This terminal was opened in another tab.', action: 'Use it here' }
    case 'ended':
      return {
        text:
          link.value.exitCode === undefined || link.value.exitCode === 0
            ? 'The shell ended.'
            : `The shell ended (exit code ${link.value.exitCode}).`,
        action: 'Open a new shell',
      }
    case 'gone':
      return {
        text: 'This terminal is no longer available. If the workspace server restarted, open a new shell; your files are still there.',
        action: 'Open a new shell',
      }
    default:
      return null
  }
})

function report(state: LinkState): void {
  link.value = state
  // The workspace covers itself with its "connection lost" dialog while the
  // terminal reconnects.
  if (state.kind === 'reconnecting') terminalReconnecting(state.attempt)
  else terminalSettled()
  const t = term.value
  if (t) t.options.disableStdin = state.kind !== 'connected'
  if (t && state.kind === 'connected' && focusOnConnect) {
    focusOnConnect = false
    t.focus()
  }
  const status: ViewStatus = (() => {
    switch (state.kind) {
      case 'connected':
        return { tone: 'ok', label: 'Connected' }
      case 'connecting':
        return { tone: 'busy', label: 'Connecting' }
      case 'reconnecting':
        return { tone: 'warn', label: `Reconnecting (${state.attempt})` }
      case 'takenOver':
        return { tone: 'warn', label: 'Open in another tab' }
      case 'ended':
        return { tone: 'error', label: 'Shell ended' }
      case 'gone':
        return { tone: 'error', label: 'Disconnected' }
      case 'unauthorized':
        return { tone: 'error', label: 'Session ended' }
    }
  })()
  emit('status', status)
  if (state.kind === 'unauthorized') emit('unauthorized')
}

function scheduleFit(): void {
  if (frame) return
  frame = window.requestAnimationFrame(() => {
    frame = 0
    try {
      fit?.fit()
    } catch {
      // Not laid out yet.
    }
  })
}

// Reattaches to the target's running shell (so a reload keeps it), or opens
// one. Ended shells of this target are deleted first: they count towards the
// server's limit of open terminals.
async function findOrOpenTerminal(): Promise<string> {
  const mine = (await listTerminals(props.lab)).filter((t) => t.target === props.target)
  const running = mine.find((t) => t.status === 'running')
  if (running) return running.id
  await Promise.allSettled(mine.map((t) => closeTerminal(props.lab, t.id)))
  opening.value = true
  try {
    return (await openTerminal(props.lab, props.runId, props.target)).id
  } finally {
    opening.value = false
  }
}

async function attach(): Promise<void> {
  problem.value = null
  conn?.close()
  report({ kind: 'connecting' })
  let id: string
  try {
    id = await findOrOpenTerminal()
  } catch (err) {
    if (isUnauthorized(err)) {
      report({ kind: 'unauthorized' })
      return
    }
    if (isRunGone(err)) {
      emit('runGone')
      return
    }
    problem.value = err instanceof Error ? `Could not open a terminal: ${err.message}` : 'Could not open a terminal.'
    emit('status', { tone: 'error', label: 'No terminal' })
    return
  }
  if (unmounted) return
  conn = new TerminalConnection(id, {
    onAttach: () => term.value?.reset(),
    onData: (bytes) => term.value?.write(bytes),
    onState: report,
  })
  const t = term.value
  if (t) conn.resize(t.cols, t.rows)
  conn.connect()
}

// The status poll reached the server again: reconnect now rather than at the
// end of the current backoff.
watch(serverBack, () => conn?.retryNow())

function act(): void {
  focusOnConnect = true
  // Reattaching takes the target's running shell back from another tab. That
  // tab may have ended this shell and opened a new one meanwhile, so look it
  // up again rather than reconnecting to the old one.
  void attach()
}

// The WebGL renderer draws cells on a canvas, so 24-bit ("truecolor") cells
// need no style attribute, which the Content Security Policy refuses (see
// cspStyles.ts). Without WebGL, or when the GPU drops the context, xterm.js
// falls back to its DOM renderer.
function useWebgl(t: Terminal): void {
  let addon: WebglAddon | undefined
  try {
    addon = new WebglAddon()
    addon.onContextLoss(() => addon?.dispose())
    t.loadAddon(addon)
  } catch {
    addon?.dispose()
  }
}

function applyTheme(): void {
  if (term.value) term.value.options.theme = readTheme()
}

onMounted(async () => {
  await fontsReady()
  if (unmounted || !host.value) return
  const t = new Terminal({
    fontFamily,
    fontSize: 14,
    lineHeight: 1.15,
    cursorBlink: true,
    scrollback: 5000,
    allowProposedApi: false,
    disableStdin: true,
    theme: readTheme(),
  })
  term.value = t
  fit = new FitAddon()
  t.loadAddon(fit)
  // F6 / Shift+F6 leave the terminal (it keeps Tab for the shell).
  t.attachCustomKeyEventHandler((ev) => {
    if (ev.type === 'keydown' && ev.key === 'F6' && !ev.ctrlKey && !ev.altKey && !ev.metaKey) {
      ev.preventDefault()
      emit('leave')
      return false
    }
    return true
  })
  stopMirror = mirrorInlineStyles(host.value)
  t.open(host.value)
  useWebgl(t)
  t.onData((data) => conn?.sendInput(data))
  t.onBinary((data) => conn?.sendBinary(Uint8Array.from(data, (c) => c.charCodeAt(0) & 0xff)))
  t.onResize(({ cols, rows }) => conn?.resize(cols, rows))
  resizeObserver = new ResizeObserver(scheduleFit)
  resizeObserver.observe(host.value)
  schemeQuery = window.matchMedia('(prefers-color-scheme: dark)')
  schemeQuery.addEventListener('change', applyTheme)
  scheduleFit()
  await attach()
})

onBeforeUnmount(() => {
  unmounted = true
  conn?.close()
  terminalSettled()
  resizeObserver?.disconnect()
  schemeQuery?.removeEventListener('change', applyTheme)
  if (frame) window.cancelAnimationFrame(frame)
  term.value?.dispose()
  stopMirror?.()
})

defineExpose({
  focus: () => term.value?.focus(),
})
</script>

<template>
  <div class="term">
    <div ref="host" class="term__host" aria-label="Terminal" />
    <div v-if="notice" class="term__notice" role="status">
      <span>{{ notice.text }}</span>
      <button v-if="notice.action" type="button" class="btn btn--sm btn--primary" @click="act">
        {{ notice.action }}
      </button>
    </div>
    <p class="term__hint small muted">
      Press <kbd>F6</kbd> to leave the terminal. Your shell keeps running when you reload or close this page.
    </p>
  </div>
</template>
