<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch, watchEffect } from 'vue'
import { launch, rememberLab } from './boot'
import {
  ApiError,
  clearToken,
  destroyRun,
  exchangeCode,
  getRun,
  getSubmission,
  getToken,
  isRunGone,
  isUnauthorized,
  listRuns,
  newIdempotencyKey,
  startRun,
  submitRun,
  type Run,
  type Submission,
} from './api'
import { serverNow, syncServerClock } from './clock'
import { apiReachable, apiUnreachable, connectionLost } from './connectivity'
import { endSessionCopy, removeCopy, type EndCopy } from './endSession'
import MessagePage from './components/MessagePage.vue'
import IntroPage from './components/IntroPage.vue'
import WorkspacePage from './components/WorkspacePage.vue'
import ResultsPage from './components/ResultsPage.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'

type Screen =
  | { kind: 'loading' }
  | { kind: 'message'; title: string; body: string; command?: string }
  | { kind: 'intro' }
  | { kind: 'workspace' }
  | { kind: 'results'; submission: Submission }

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
    // Submitted and removed (here or in a terminal): show that result.
    const sub = await latestSubmission(r)
    if (sub) {
      screen.value = { kind: 'results', submission: sub }
      return
    }
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
    // While a submission or removal runs, the dialog owns the page.
    if (dialogBusy.value) {
      schedule()
      return
    }
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

// ---------------------------------------------------------------- end session

// The open dialog: submitting a lab, ending a playground or removing a lab's
// environment. One Idempotency-Key per confirmed submission, so a retry after
// a lost connection never grades twice.
type DialogKind = 'end' | 'remove'
const dialog = ref<DialogKind | null>(null)
const dialogBusy = ref(false)
const dialogError = ref<string | null>(null)
let submitKey: string | null = null

const dialogCopy = computed<EndCopy | null>(() => {
  const r = run.value
  if (!dialog.value || !r) return null
  if (dialog.value === 'remove') return removeCopy(r)
  const expired =
    r.status === 'expired' || (r.deadlineAt !== null && Date.parse(r.deadlineAt) <= serverNow())
  return endSessionCopy(r, expired)
})

function openDialog(kind: DialogKind): void {
  dialogError.value = null
  submitKey = null
  dialog.value = kind
}

function closeDialog(): void {
  if (dialogBusy.value) return
  dialog.value = null
  dialogError.value = null
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

async function confirmDialog(): Promise<void> {
  const r = run.value
  if (!r || dialogBusy.value) return
  dialogBusy.value = true
  dialogError.value = null
  try {
    if (dialog.value === 'remove' || r.playground) await removeEnvironment(r)
    else await submit(r)
  } finally {
    dialogBusy.value = false
  }
}

async function submit(r: Run): Promise<void> {
  submitKey ??= newIdempotencyKey()
  let sub: Submission
  try {
    sub = await submitRun(r.lab, r.runId, submitKey)
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) {
      // The request was lost, not necessarily the grading: ask for it.
      try {
        sub = await waitForSubmission(r)
      } catch (waitErr) {
        failDialog(waitErr)
        return
      }
    } else {
      failDialog(err)
      return
    }
  }
  dialog.value = null
  screen.value = { kind: 'results', submission: sub }
  await updateRunQuietly()
}

// Polls GET /submission until the grading this page started is done.
async function waitForSubmission(r: Run): Promise<Submission> {
  let unreachable = 0
  for (;;) {
    await new Promise((resolve) => window.setTimeout(resolve, 3000))
    try {
      const s = await getSubmission(r.lab)
      unreachable = 0
      if (s.state === 'done' && s.submission.runId === r.runId) return s.submission
      if (s.state === 'failed') throw new ApiError(409, s.error.message, s.error.code)
      if (s.state === 'done') throw new ApiError(409, 'This lab was restarted; reload the page.', 'run_changed')
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 0) throw err
      if (++unreachable >= 5) throw err
    }
  }
}

async function removeEnvironment(r: Run): Promise<void> {
  try {
    await destroyRun(r.lab, r.runId)
  } catch (err) {
    failDialog(err)
    return
  }
  dialog.value = null
  window.clearTimeout(poll)
  screen.value = r.playground
    ? {
        kind: 'message',
        title: 'Playground ended',
        body: 'The playground and everything in it were deleted. Start a new one from your terminal.',
        command: `astrona run ${r.lab}`,
      }
    : {
        kind: 'message',
        title: 'Environment removed',
        body: 'The lab’s environment was deleted. To practise again, start a new run from your terminal.',
        command: `astrona run ${r.lab}`,
      }
}

function failDialog(err: unknown): void {
  if (isUnauthorized(err)) {
    dialog.value = null
    showError(err)
    return
  }
  if (isRunGone(err)) {
    dialog.value = null
    void onRunGone()
    return
  }
  // A definite answer from the server: a new attempt gets a new key.
  if (!(err instanceof ApiError) || err.status !== 0) submitKey = null
  dialogError.value = message(err)
}

// Refreshes the run behind the results page without changing the screen.
async function updateRunQuietly(): Promise<void> {
  if (!labId) return
  try {
    const r = await getRun(labId)
    syncServerClock(r.serverNow)
    run.value = r
  } catch {
    // The results page shows the submission's own data; the next action reports any error.
  }
}

async function latestSubmission(r: Run): Promise<Submission | null> {
  try {
    const s = await getSubmission(r.lab)
    return s.state === 'done' && s.submission.runId === r.runId ? s.submission : null
  } catch (err) {
    if (err instanceof ApiError && err.code === 'no_submission') return null
    throw err
  }
}

function backToWorkspace(): void {
  screen.value = { kind: 'workspace' }
  schedule()
}

// A leave-page confirmation while a session runs or a submission is being
// graded: browser shortcuts such as Ctrl+W close the tab even when the shell
// wanted them.
function beforeUnload(ev: BeforeUnloadEvent): void {
  if (screen.value.kind === 'workspace' || dialogBusy.value) ev.preventDefault()
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
  <WorkspacePage
    v-else-if="screen.kind === 'workspace' && run"
    :run="run"
    @unauthorized="onUnauthorized"
    @run-gone="onRunGone"
    @end="openDialog('end')"
  />
  <ResultsPage
    v-else-if="screen.kind === 'results'"
    :run="run"
    :submission="screen.submission"
    :lab="screen.submission.lab"
    @back="backToWorkspace"
    @remove="openDialog('remove')"
  />
  <ConfirmDialog
    v-if="dialog && dialogCopy"
    :copy="dialogCopy"
    :busy="dialogBusy"
    :error="dialogError"
    :danger="dialog === 'remove' || !!run?.playground"
    @confirm="confirmDialog"
    @cancel="closeDialog"
  />
</template>
