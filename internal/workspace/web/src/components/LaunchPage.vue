<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import {
  answerTerms,
  getLaunch,
  isLaunchGone,
  isUnauthorized,
  linkLaunchSession,
  type Launch,
  type LaunchEvent,
  type SignIn,
} from '../api'
import { envSpecs } from '../environment'
import { useCopyText } from '../copyText'
import AccountNotice from './AccountNotice.vue'
import BrandMark from './BrandMark.vue'
import LaunchSignIn from './LaunchSignIn.vue'

// What `astrona run` shows before its lab exists: the CLI terms (when they
// still need accepting), then the lab being set up, step by step, as the
// terminal shows it. Once the lab is ready the page opens it (emit ready).

const props = defineProps<{ id: string }>()
const emit = defineEmits<{ ready: [lab: string]; unauthorized: []; gone: [] }>()

const POLL_MS = 600
const launch = shallowRef<Launch | null>(null)
const loadError = ref<string | null>(null)
// The lab is ready: the page waits for "Begin session" instead of
// switching on its own. Begin starts the lab: the next page is the workspace.
const readyLab = ref<string | null>(null)
let timer: number | undefined
let stopped = false

async function poll(): Promise<void> {
  try {
    const l = await getLaunch(props.id)
    loadError.value = null
    launch.value = l
    if (l.stage === 'ready' && l.lab) {
      stopped = true
      readyLab.value = l.lab
      return
    }
    if (final.value) {
      stopped = true
      return
    }
  } catch (err) {
    if (isUnauthorized(err)) {
      stopped = true
      emit('unauthorized')
      return
    }
    if (isLaunchGone(err)) {
      stopped = true
      emit('gone')
      return
    }
    loadError.value = err instanceof Error ? err.message : String(err)
  }
  if (!stopped) timer = window.setTimeout(poll, POLL_MS)
}

onMounted(poll)
onBeforeUnmount(() => {
  stopped = true
  window.clearTimeout(timer)
})

// ---------------------------------------------------------------- stage

const final = computed(() => {
  const l = launch.value
  if (!l) return false
  return l.stage === 'failed' || l.stage === 'declined' || l.stage === 'ended' || !l.alive
})

const heading = computed(() => {
  const l = launch.value
  if (!l) return 'Starting astrona…'
  if (readyLab.value) {
    if (l.playground) return 'Your playground is ready'
    return l.title ? `${l.title} is ready` : 'Your lab is ready'
  }
  if (l.stage === 'declined') return 'Terms declined'
  if (l.stage === 'failed') return 'The lab could not be set up'
  if (l.stage === 'ended') return 'Nothing more to show here'
  if (!l.alive) return 'astrona stopped'
  if (l.stage === 'terms') return 'Accept the CLI terms to continue'
  return l.title ? `Setting up ${l.title}` : 'Setting up your lab'
})

const lead = computed(() => {
  const l = launch.value
  if (!l) return ''
  if (readyLab.value)
    return 'Nothing counts yet. Take a look around — your time starts when you click Begin session.'
  if (l.stage === 'declined') return 'Nothing was started and astrona has stopped. You can close this tab.'
  if (l.stage === 'failed') return l.message || 'astrona stopped with an error. The lab was not started, or what was started was removed.'
  if (l.stage === 'ended') return l.message || 'astrona finished in your terminal.'
  if (!l.alive) return 'The command in your terminal ended before the lab was ready. Run it again to start over.'
  if (l.stage === 'terms') return 'astrona is waiting for your answer. Nothing has been started yet.'
  if (l.stage === 'checking') return 'Checking the lab. If your terminal asks a question, answer it there.'
  return 'This takes a few minutes. Your time does not start until you click Begin session.'
})

// ---------------------------------------------------------------- steps

interface Row {
  key: number
  kind: 'section' | 'step'
  label: string
  state: 'running' | 'ok' | 'skip' | 'fail' | 'warn' | 'section'
  detail: string
  notes: string[]
  seconds?: number
}

// Folds step/ok/skip/fail events into one row per step.
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const events: LaunchEvent[] = launch.value?.events ?? []
  let open: Row | null = null
  events.forEach((e, i) => {
    switch (e.kind) {
      case 'section':
        open = null
        out.push({ key: i, kind: 'section', label: e.label, state: 'section', detail: '', notes: [] })
        break
      case 'step':
        open = { key: i, kind: 'step', label: e.label, state: 'running', detail: '', notes: [] }
        out.push(open)
        break
      case 'note':
        if (open) open.notes.push(e.detail ?? '')
        break
      case 'warn':
        out.push({ key: i, kind: 'step', label: e.label, state: 'warn', detail: e.detail ?? '', notes: [] })
        break
      case 'ok':
      case 'skip':
      case 'fail': {
        const target: Row | null = open && open.label === e.label ? open : null
        const row: Row = target ?? { key: i, kind: 'step', label: e.label, state: 'running', detail: '', notes: [] }
        if (!target) out.push(row)
        row.state = e.kind
        row.detail = e.detail ?? ''
        row.seconds = e.seconds
        open = null
        break
      }
    }
  })
  return out
})

const current = computed(() => {
  const l = launch.value
  if (!l || final.value || readyLab.value) return null
  for (let i = rows.value.length - 1; i >= 0; i--) {
    const r = rows.value[i]
    if (r.state === 'running') return r
    if (r.kind === 'step') break
  }
  return null
})

function seconds(s?: number): string {
  if (s === undefined || s < 0.05) return ''
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`
  return `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`
}

const mark: Record<Row['state'], string> = { running: '…', ok: '✓', skip: '–', fail: '✗', warn: '!', section: '' }

// ---------------------------------------------------------------- ready summary

const events = computed<LaunchEvent[]>(() => launch.value?.events ?? [])
const okSteps = computed(() => events.value.filter((e) => e.kind === 'ok'))
const totalSeconds = computed(() => okSteps.value.reduce((n, e) => n + (e.seconds ?? 0), 0))
const warnings = computed(() => events.value.filter((e) => e.kind === 'warn').length)
const stepCount = computed(() => rows.value.filter((r) => r.kind === 'step').length)

interface Stage {
  key: number
  name: string
  seconds: number
  steps: number
  warnings: number
  failed: boolean
}

// One card per section event: its steps' time and count.
const stages = computed<Stage[]>(() => {
  const out: Stage[] = []
  let cur: Stage | null = null
  events.value.forEach((e, i) => {
    if (e.kind === 'section') {
      cur = { key: i, name: e.label, seconds: 0, steps: 0, warnings: 0, failed: false }
      out.push(cur)
      return
    }
    if (!cur) return
    if (e.kind === 'ok') {
      cur.steps++
      cur.seconds += e.seconds ?? 0
    } else if (e.kind === 'warn') cur.warnings++
    else if (e.kind === 'fail') cur.failed = true
  })
  return out
})

function stageDetail(st: Stage): string {
  const parts = [`${st.steps} ${st.steps === 1 ? 'step' : 'steps'} passed`]
  if (st.warnings) parts.push(`${st.warnings} ${st.warnings === 1 ? 'warning' : 'warnings'}`)
  return parts.join(' · ')
}

const environment = computed(() => launch.value?.environment)
const specs = computed(() => envSpecs(environment.value))
const connect = computed(() => environment.value?.connect ?? '')
const { copied, failed: copyFailed, copy } = useCopyText()

// The account chip in the header: signed in, or not.
const chip = computed(() => {
  const a = launch.value?.account
  if (!a) return null
  if (a.state === 'signed-in') {
    const what = launch.value?.playground ? 'time saved to your Astrona account' : 'time and result saved to your Astrona account'
    return { on: true, name: a.username ?? '', text: what }
  }
  if (a.state === 'signed-out') return { on: false, name: '', text: 'Not signed in' }
  if (a.state === 'local-only') return { on: false, name: '', text: 'Local only' }
  return null
})

// The setup log: open while the lab is set up (and when it failed),
// folded away once it is ready. The learner can open or fold it any time.
const logOpenChoice = ref<boolean | null>(null)
const logOpen = computed(() => logOpenChoice.value ?? !readyLab.value)
const logSection = ref<HTMLElement>()
async function showWarnings(): Promise<void> {
  logOpenChoice.value = true
  await nextTick()
  const el = list.value?.querySelector<HTMLElement>('.launch__step--warn')
  logSection.value?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  el?.scrollIntoView({ block: 'nearest' })
}

const list = ref<HTMLElement>()
watch(
  () => rows.value.length,
  async () => {
    await nextTick()
    const el = list.value
    if (el) el.scrollTop = el.scrollHeight
  },
)

// ---------------------------------------------------------------- sign in

// A signed-out run of a catalog lab or playground can still be saved to an
// account: signing in here before Begin links it (never --local-only, whose
// account state is local-only).
const canSignIn = computed(() => {
  const a = launch.value?.account
  return !!a && a.state === 'signed-out' && !!a.site
})

function onSignedIn(s: SignIn): void {
  const l = launch.value
  if (!l) return
  launch.value = { ...l, account: { state: 'signed-in', username: s.username, site: s.site }, signedInHere: true }
}

// Begin: after a sign-in on this page the run's lab session is started on
// that account first; Start then starts its clock as for a signed-in run.
const linking = ref(false)
const linkError = ref<string | null>(null)
async function begin(link = true): Promise<void> {
  const lab = readyLab.value
  if (!lab || linking.value) return
  if (link && launch.value?.signedInHere) {
    linking.value = true
    linkError.value = null
    try {
      await linkLaunchSession(props.id)
    } catch (err) {
      linking.value = false
      if (isUnauthorized(err)) emit('unauthorized')
      else linkError.value = err instanceof Error ? err.message : String(err)
      return
    }
  }
  emit('ready', lab)
}

// ---------------------------------------------------------------- terms

const termsDialog = ref<HTMLDialogElement>()
const answering = ref(false)
const answerError = ref<string | null>(null)
const showTerms = computed(() => {
  const l = launch.value
  return !!l && l.stage === 'terms' && !!l.terms && l.alive && !l.answered
})

watch(showTerms, async (show) => {
  await nextTick()
  const d = termsDialog.value
  if (!d) return
  if (show && !d.open) d.showModal()
  if (!show && d.open) d.close()
})

function onTermsCancel(ev: Event): void {
  // Esc does not dismiss the terms: astrona waits for accept or decline.
  ev.preventDefault()
}

async function answer(accept: boolean): Promise<void> {
  const l = launch.value
  if (!l?.terms || answering.value) return
  answering.value = true
  answerError.value = null
  try {
    await answerTerms(l.id, accept, l.terms.sha256)
    launch.value = { ...l, answered: accept ? 'accepted' : 'declined' }
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
    else answerError.value = err instanceof Error ? err.message : String(err)
  } finally {
    answering.value = false
  }
}
</script>

<template>
  <main class="launch-page">
    <header class="runbar">
      <div class="runbar__brand">
        <BrandMark size="sm" />
        <span class="runbar__name">Astrona run</span>
      </div>
      <p v-if="chip" class="chip" :class="{ 'chip--on': chip.on }">
        <span class="chip__dot" aria-hidden="true" />
        <template v-if="chip.on && chip.name">
          <span class="chip__name">{{ chip.name }}</span>
          <span class="chip__text">· {{ chip.text }}</span>
        </template>
        <span v-else class="chip__text">{{ chip.text }}</span>
      </p>
    </header>

    <div class="launch" :aria-busy="!final && !readyLab">
      <section class="launch__hero" :class="{ 'launch__hero--ready': readyLab }" aria-labelledby="launch-title">
        <p v-if="readyLab" class="launch__badges">
          <span class="badge badge--ready"><span class="dot" aria-hidden="true" /> Lab ready</span>
          <span v-if="okSteps.length" class="small muted">
            Set up in {{ seconds(totalSeconds) || '0s' }} · {{ okSteps.length }} {{ okSteps.length === 1 ? 'step' : 'steps' }} passed
          </span>
        </p>
        <p v-else class="eyebrow eyebrow--page">astrona run</p>
        <h1 id="launch-title" class="launch__title">{{ heading }}</h1>
        <p v-if="readyLab" class="launch__lab">
          <code class="launch__lab-name">{{ readyLab }}</code>
          <button type="button" class="copy-btn" :aria-label="`Copy the lab name ${readyLab}`" @click="copy('lab', readyLab)">
            {{ copied === 'lab' ? 'Copied' : copyFailed === 'lab' ? 'Copy failed' : 'Copy' }}
          </button>
        </p>
        <p class="launch__lead muted">{{ lead }}</p>

        <div v-if="readyLab" class="launch__begin">
          <button type="button" class="btn btn--go launch__cta" :disabled="linking" @click="begin()">
            {{ linking ? 'Saving to your account…' : 'Begin session' }} <span aria-hidden="true">→</span>
          </button>
          <p class="small muted launch__timer">Timer starts on click · <span class="mono">00:00</span></p>
        </div>
        <p v-if="readyLab && launch?.signedInHere && !linkError" class="small muted">
          Signed in{{ launch.account?.username ? ` as ${launch.account.username}` : '' }}: Begin session saves this
          {{ launch.playground ? 'playground' : 'run' }} to your Astrona account.
        </p>
        <div v-if="linkError" class="dialog__error" role="alert">
          <p>This run could not be saved to your account: {{ linkError }}</p>
          <div class="account__actions">
            <button type="button" class="btn btn--primary btn--sm" :disabled="linking" @click="begin()">Try again</button>
            <button type="button" class="btn btn--ghost btn--sm" :disabled="linking" @click="begin(false)">
              Begin without saving
            </button>
          </div>
        </div>

        <p v-if="launch && !final && !readyLab && launch.stage !== 'terms'" class="readiness readiness--busy" role="status">
          <span class="dot" aria-hidden="true" />
          <template v-if="current">
            {{ current.label }}<span v-if="launch.status" class="muted"> — {{ launch.status }}</span>
          </template>
          <template v-else>{{ launch.stage === 'checking' ? 'Checking the lab…' : 'Working…' }}</template>
        </p>

        <div v-if="launch?.stage === 'failed'" class="dialog__error" role="alert">
          <p v-if="launch.error">{{ launch.error }}</p>
          <p v-if="launch.logPath" class="small">Full log: <code>{{ launch.logPath }}</code> — open it with <code>astrona logs view</code>.</p>
        </div>
        <p v-if="loadError" class="error" role="alert">Lost touch with astrona: {{ loadError }}. Retrying…</p>
      </section>

      <AccountNotice
        v-if="launch?.account && launch.account.state !== 'signed-in' && (readyLab || !final)"
        :account="launch.account"
        :playground="launch.playground"
      >
        <template v-if="canSignIn" #default>
          <LaunchSignIn
            :launch-id="id"
            :site="launch.account.site"
            :playground="launch.playground"
            @signed-in="onSignedIn"
            @unauthorized="emit('unauthorized')"
          />
        </template>
      </AccountNotice>

      <ol v-if="readyLab && stages.length" class="stages" aria-label="Setup stages">
        <li v-for="st in stages" :key="st.key" class="stage" :class="{ 'stage--warn': st.warnings, 'stage--fail': st.failed }">
          <p class="stage__head">
            <span class="stage__mark" aria-hidden="true">{{ st.failed ? '✗' : st.warnings ? '!' : '✓' }}</span>
            <span class="stage__name">{{ st.name }}</span>
            <span class="stage__time mono small muted">{{ seconds(st.seconds) }}</span>
          </p>
          <p class="stage__detail small muted">{{ stageDetail(st) }}</p>
        </li>
      </ol>

      <div v-if="readyLab && (specs.length || connect)" class="launch__info">
        <section v-if="specs.length" class="launch__box" aria-labelledby="launch-specs">
          <h2 id="launch-specs" class="eyebrow">What's in your lab</h2>
          <dl class="specs">
            <template v-for="(sp, i) in specs" :key="i">
              <dt class="specs__label">{{ sp.label }}</dt>
              <dd class="specs__value mono">{{ sp.value }}</dd>
            </template>
          </dl>
        </section>
        <section v-if="connect" class="launch__box" aria-labelledby="launch-connect">
          <h2 id="launch-connect" class="eyebrow">Connect from your terminal</h2>
          <div class="connect">
            <code class="connect__cmd"><span class="connect__prompt" aria-hidden="true">$ </span>{{ connect }}</code>
            <button type="button" class="copy-btn" aria-label="Copy the command" @click="copy('connect', connect)">
              {{ copied === 'connect' ? 'Copied' : copyFailed === 'connect' ? 'Copy failed' : 'Copy' }}
            </button>
          </div>
        </section>
      </div>

      <p v-if="readyLab && warnings" class="callout callout--warn">
        <span class="callout__mark" aria-hidden="true">!</span>
        <span>
          {{ warnings }} {{ warnings === 1 ? 'warning' : 'warnings' }} during setup — it didn't block the lab.
          <button type="button" class="link-btn" @click="showWarnings">See it in the log</button>
        </span>
      </p>

      <section v-if="rows.length" ref="logSection" class="launch__term" aria-labelledby="launch-log">
        <div class="launch__bar">
          <h2 id="launch-log" class="launch__log-title">Setup log</h2>
          <span class="launch__cmd">astrona run {{ launch?.lab || launch?.title || '' }}</span>
          <button
            type="button"
            class="launch__toggle"
            aria-controls="launch-steps"
            :aria-expanded="logOpen"
            @click="logOpenChoice = !logOpen"
          >
            {{ logOpen ? 'Hide details' : `Show ${stepCount} ${stepCount === 1 ? 'step' : 'steps'}` }}
          </button>
        </div>
        <ol v-show="logOpen" id="launch-steps" ref="list" class="launch__steps" aria-label="Setup steps">
          <li
            v-for="r in rows"
            :key="r.key"
            :class="r.kind === 'section' ? 'launch__section' : `launch__step launch__step--${r.state}`"
          >
            <template v-if="r.kind === 'section'">{{ r.label }}</template>
            <template v-else>
              <span class="launch__mark" aria-hidden="true">{{ mark[r.state] }}</span>
              <span class="launch__label">
                {{ r.label }}
                <span v-if="r.state === 'fail'" class="visually-hidden">failed</span>
                <span v-else-if="r.state === 'skip'" class="visually-hidden">skipped</span>
                <span v-else-if="r.state === 'warn'" class="visually-hidden">warning</span>
                <span v-if="r.detail" class="launch__detail">{{ r.detail }}</span>
                <span v-for="(n, i) in r.notes" :key="i" class="launch__detail">{{ n }}</span>
              </span>
              <span class="launch__time small">{{ seconds(r.seconds) }}</span>
            </template>
          </li>
          <li v-if="!final && !readyLab" class="launch__cursor" aria-hidden="true"><span>▍</span></li>
          <li v-if="readyLab" class="launch__step launch__step--ok launch__done">
            <span class="launch__mark" aria-hidden="true">→</span>
            <span class="launch__label">Lab ready</span>
            <span />
          </li>
        </ol>
      </section>
    </div>

    <dialog
      ref="termsDialog"
      class="dialog terms"
      aria-labelledby="terms-title"
      aria-describedby="terms-lead"
      @cancel="onTermsCancel"
    >
      <div v-if="launch?.terms" class="dialog__card">
        <p class="eyebrow eyebrow--page">Astrona CLI · terms {{ launch.terms.revision }}</p>
        <h2 id="terms-title" class="dialog__title">Usage terms and risk notice</h2>
        <p id="terms-lead" class="dialog__body">
          Read these before astrona starts anything on this computer. You are asked again only when they change.
        </p>
        <pre class="terms__text" tabindex="0" aria-label="Terms text">{{ launch.terms.text }}</pre>
        <p class="small muted terms__digest">SHA-256: {{ launch.terms.sha256 }}</p>
        <p v-if="answerError" class="dialog__error" role="alert">{{ answerError }}</p>
        <div class="dialog__actions">
          <button type="button" class="btn btn--ghost" :disabled="answering" @click="answer(false)">
            Decline and stop
          </button>
          <button type="button" class="btn btn--primary" :disabled="answering" @click="answer(true)">
            Accept and continue
          </button>
        </div>
      </div>
    </dialog>
  </main>
</template>
