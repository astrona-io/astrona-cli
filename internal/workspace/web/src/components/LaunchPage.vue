<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { answerTerms, getLaunch, isLaunchGone, isUnauthorized, type Launch, type LaunchEvent } from '../api'
import AccountNotice from './AccountNotice.vue'
import BrandMark from './BrandMark.vue'

// What `astrona run` shows before its lab exists: the CLI terms (when they
// still need accepting), then the lab being set up, step by step, as the
// terminal shows it. Once the lab is ready the page opens it (emit ready).

const props = defineProps<{ id: string }>()
const emit = defineEmits<{ ready: [lab: string]; unauthorized: []; gone: [] }>()

const POLL_MS = 600
const launch = shallowRef<Launch | null>(null)
const loadError = ref<string | null>(null)
// The lab is ready: the page waits for "Begin this session" instead of
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
  if (readyLab.value) return l.title ? `${l.title} is ready` : 'Your lab is ready'
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
    return 'Everything is set up and nothing counts yet. Your time starts when you click Begin this session.'
  if (l.stage === 'declined') return 'Nothing was started and astrona has stopped. You can close this tab.'
  if (l.stage === 'failed') return l.message || 'astrona stopped with an error. The lab was not started, or what was started was removed.'
  if (l.stage === 'ended') return l.message || 'astrona finished in your terminal.'
  if (!l.alive) return 'The command in your terminal ended before the lab was ready. Run it again to start over.'
  if (l.stage === 'terms') return 'astrona is waiting for your answer. Nothing has been started yet.'
  if (l.stage === 'checking') return 'Checking the lab. If your terminal asks a question, answer it there.'
  return 'This takes a few minutes. Your time does not start until you click Begin this session.'
})

// ---------------------------------------------------------------- steps

interface Row {
  key: number
  kind: 'section' | 'step'
  label: string
  state: 'running' | 'ok' | 'skip' | 'fail' | 'section'
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
        out.push({ key: i, kind: 'step', label: e.label, state: 'skip', detail: 'warning', notes: [] })
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

const mark: Record<Row['state'], string> = { running: '…', ok: '✓', skip: '–', fail: '✗', section: '' }

const list = ref<HTMLElement>()
watch(
  () => rows.value.length,
  async () => {
    await nextTick()
    const el = list.value
    if (el) el.scrollTop = el.scrollHeight
  },
)

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
  <main class="intro">
    <section class="panel intro__card launch" aria-labelledby="launch-title" :aria-busy="!final">
      <div class="brand">
        <BrandMark />
        <p class="eyebrow eyebrow--page">astrona run</p>
      </div>
      <h1 id="launch-title" class="intro__title">{{ heading }}</h1>
      <p class="muted">{{ lead }}</p>

      <p v-if="launch && !final && !readyLab && launch.stage !== 'terms'" class="readiness readiness--busy" role="status">
        <span class="dot" aria-hidden="true" />
        <template v-if="current">
          {{ current.label }}<span v-if="launch.status" class="muted"> — {{ launch.status }}</span>
        </template>
        <template v-else>{{ launch.stage === 'checking' ? 'Checking the lab…' : 'Working…' }}</template>
      </p>

      <AccountNotice v-if="launch?.account && (readyLab || !final)" :account="launch.account" :playground="launch.playground" />

      <div v-if="rows.length" class="launch__term">
        <div class="launch__bar" aria-hidden="true">
          <span class="launch__light" /><span class="launch__light" /><span class="launch__light" />
          <span class="launch__cmd">astrona run {{ launch?.lab || launch?.title || '' }}</span>
        </div>
      <ol ref="list" class="launch__steps" aria-label="Setup steps">
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
              <span v-if="r.detail" class="launch__detail">{{ r.detail }}</span>
              <span v-for="(n, i) in r.notes" :key="i" class="launch__detail">{{ n }}</span>
            </span>
            <span class="launch__time small muted">{{ seconds(r.seconds) }}</span>
          </template>
        </li>
        <li v-if="!final && !readyLab" class="launch__cursor" aria-hidden="true"><span>▍</span></li>
        <li v-if="readyLab" class="launch__step launch__step--ok launch__done">
          <span class="launch__mark" aria-hidden="true">➜</span>
          <span class="launch__label">Lab ready</span>
          <span />
        </li>
      </ol>
      </div>

      <div v-if="readyLab" class="launch__begin">
        <button type="button" class="btn btn--primary launch__cta" @click="emit('ready', readyLab)">
          Begin this session <span aria-hidden="true">→</span>
        </button>
      </div>

      <div v-if="launch?.stage === 'failed'" class="dialog__error" role="alert">
        <p v-if="launch.error">{{ launch.error }}</p>
        <p v-if="launch.logPath" class="small">Full log: <code>{{ launch.logPath }}</code> — open it with <code>astrona logs view</code>.</p>
      </div>
      <p v-if="loadError" class="error" role="alert">Lost touch with astrona: {{ loadError }}. Retrying…</p>
    </section>

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
