<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch, watchEffect } from 'vue'
import { launch, rememberLab } from './boot'
import {
  ApiError,
  clearToken,
  exchangeCode,
  getRun,
  getToken,
  isRunGone,
  isUnauthorized,
  listRuns,
  startRun,
  type Run,
} from './api'
import { syncServerClock } from './clock'
import { apiReachable, apiUnreachable, connectionLost } from './connectivity'
import MessagePage from './components/MessagePage.vue'
import IntroPage from './components/IntroPage.vue'
import WorkspacePage from './components/WorkspacePage.vue'

type Screen =
  | { kind: 'loading' }
  | { kind: 'message'; title: string; body: string; command?: string }
  | { kind: 'intro' }
  | { kind: 'workspace' }

const screen = shallowRef<Screen>({ kind: 'loading' })
const run = ref<Run | null>(null)
const starting = ref(false)
const startError = ref<string | null>(null)
let labId: string | null = launch.lab
let poll: number | undefined
// What to run for a new link. The lab is known from the launch link or the
// hash route; a reload keeps it in the address bar.
const uiCommand = launch.lab ? `astrona ui ${launch.lab}` : 'astrona ui <lab>'

const expiredLink: Screen = {
  kind: 'message',
  title: 'Link expired',
  body: 'This link has expired or was already used. Run astrona again to get a new one.',
  command: uiCommand,
}
const sessionEnded: Screen = {
  kind: 'message',
  title: 'Session ended',
  body: 'This page is no longer signed in to the workspace, for example because the workspace server restarted. Run astrona again to get a new link.',
  command: uiCommand,
}

function showError(err: unknown): void {
  if (isUnauthorized(err)) {
    clearToken()
    screen.value = sessionEnded
    return
  }
  const body =
    err instanceof ApiError && err.status === 0
      ? 'The workspace server cannot be reached. It stops when no lab is running; run astrona again to start it.'
      : `Something went wrong: ${err instanceof Error ? err.message : String(err)}`
  screen.value = { kind: 'message', title: 'Workspace unavailable', body, command: uiCommand }
}

// Playgrounds have no intro and no Start: they open the workspace directly.
// While an environment is still provisioning the intro shows its readiness.
const needsIntro = (r: Run) => r.status === 'provisioning' || (!r.playground && r.status === 'ready')

async function refresh(): Promise<void> {
  if (!labId) return
  const previous = run.value?.runId
  const r = await getRun(labId)
  syncServerClock(r.serverNow)
  run.value = r
  if (r.status === 'failed') {
    screen.value = {
      kind: 'message',
      title: 'The lab could not start',
      body: 'Provisioning failed. The CLI shows the log and offers a retry.',
      command: `astrona status ${r.lab}`,
    }
    return
  }
  if (r.status === 'stopped') {
    screen.value = {
      kind: 'message',
      title: 'This lab is no longer running',
      body: 'It was stopped, destroyed or restarted. If it is running again, open it from your terminal for a new link.',
      command: `astrona ui ${r.lab}`,
    }
    return
  }
  // A restarted lab has a new runId: back to its intro, if it has one.
  const replaced = previous !== undefined && previous !== r.runId
  if (screen.value.kind === 'loading' || screen.value.kind === 'intro' || replaced) {
    screen.value = needsIntro(r) ? { kind: 'intro' } : { kind: 'workspace' }
  }
}

const unreachable = (err: unknown) => err instanceof ApiError && err.status === 0

async function onRunGone(): Promise<void> {
  try {
    await refresh()
    apiReachable()
  } catch (err) {
    // An open workspace stays as it is behind its "connection lost" dialog,
    // so the session continues when the server answers again.
    if (screen.value.kind === 'workspace' && unreachable(err)) {
      apiUnreachable()
      schedule()
      return
    }
    showError(err)
  }
}

function schedule(): void {
  window.clearTimeout(poll)
  // Fast while waiting for the environment or for a lost connection to come
  // back, slow once working (keeps the status and the clock offset fresh
  // until the event stream lands).
  const delay = screen.value.kind === 'intro' || connectionLost.value ? 2000 : 30000
  poll = window.setTimeout(async () => {
    try {
      await refresh()
      apiReachable()
    } catch (err) {
      if (isUnauthorized(err)) {
        showError(err)
        return
      }
      if (unreachable(err) && screen.value.kind === 'workspace') apiUnreachable()
      // Otherwise transient: the next poll tries again.
    }
    if (screen.value.kind === 'intro' || screen.value.kind === 'workspace') schedule()
  }, delay)
}

// A terminal that starts reconnecting brings the next status poll forward,
// so the dialog also learns when the server itself answers again.
watch(connectionLost, (lost) => {
  if (lost && screen.value.kind === 'workspace') schedule()
})

async function chooseLab(): Promise<string | null> {
  const runs = await listRuns()
  const active = runs.filter((r) => r.status !== 'stopped' && r.status !== 'failed')
  if (active.length === 1) return active[0]!.lab
  screen.value = {
    kind: 'message',
    title: active.length === 0 ? 'No lab is running' : 'Choose a lab',
    body:
      active.length === 0
        ? 'Start a lab from your terminal and answer yes when astrona asks to open it in your browser.'
        : `Several labs are running (${active.map((r) => r.lab).join(', ')}). Open one with its name.`,
    command: active.length === 0 ? 'astrona run <lab>' : 'astrona ui <lab>',
  }
  return null
}

async function start(): Promise<void> {
  if (!run.value || starting.value) return
  starting.value = true
  startError.value = null
  try {
    const r = await startRun(run.value.lab, run.value.runId)
    syncServerClock(r.serverNow)
    run.value = r
    screen.value = { kind: 'workspace' }
    schedule()
  } catch (err) {
    if (isUnauthorized(err)) showError(err)
    else if (isRunGone(err)) await onRunGone()
    else startError.value = err instanceof Error ? err.message : String(err)
  } finally {
    starting.value = false
  }
}

function onUnauthorized(): void {
  clearToken()
  window.clearTimeout(poll)
  screen.value = sessionEnded
}

// A leave-page confirmation while a session runs: browser shortcuts such as
// Ctrl+W close the tab even when the shell wanted them.
function beforeUnload(ev: BeforeUnloadEvent): void {
  if (screen.value.kind === 'workspace') ev.preventDefault()
}

onMounted(async () => {
  window.addEventListener('beforeunload', beforeUnload)
  try {
    if (launch.code) {
      try {
        await exchangeCode(launch.code)
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) {
          // A link from an earlier tab may still have a live session here.
          if (!getToken()) {
            screen.value = expiredLink
            return
          }
        } else {
          throw err
        }
      }
    }
    if (!getToken()) {
      screen.value = {
        kind: 'message',
        title: 'Open the workspace from astrona',
        body: 'For your safety this page only works when opened through the link astrona prints. Run this in your terminal:',
        command: uiCommand,
      }
      return
    }
    labId ??= await chooseLab()
    if (!labId) return
    rememberLab(labId)
    await refresh()
    if (screen.value.kind === 'intro' || screen.value.kind === 'workspace') schedule()
  } catch (err) {
    showError(err)
  }
})

onBeforeUnmount(() => {
  window.clearTimeout(poll)
  window.removeEventListener('beforeunload', beforeUnload)
})

watchEffect(() => {
  document.title = run.value ? `${run.value.title} · Astrona` : 'Astrona workspace'
})
</script>

<template>
  <main v-if="screen.kind === 'loading'" class="center" aria-busy="true">
    <p class="eyebrow eyebrow--page">Astrona workspace</p>
    <p class="muted">Opening the workspace…</p>
  </main>
  <MessagePage
    v-else-if="screen.kind === 'message'"
    :title="screen.title"
    :body="screen.body"
    :command="screen.command"
  />
  <IntroPage
    v-else-if="screen.kind === 'intro' && run"
    :run="run"
    :starting="starting"
    :error="startError"
    @start="start"
  />
  <WorkspacePage v-else-if="screen.kind === 'workspace' && run" :run="run" @unauthorized="onUnauthorized" @run-gone="onRunGone" />
</template>
