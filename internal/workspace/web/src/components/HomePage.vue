<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { isUnauthorized, listMachineLabs, type MachineLab } from '../api'
import AboutDialog from './AboutDialog.vue'
import BrandMark from './BrandMark.vue'

// "Labs on this computer": every astrona lab on this machine, opened in the
// browser or not. `astrona run` returns while its lab keeps running in the
// background, so this is where a learner sees what still runs and ends it.

const emit = defineEmits<{ open: [lab: string]; remove: [lab: MachineLab]; unauthorized: [] }>()

const POLL_MS = 5000
const labs = shallowRef<MachineLab[] | null>(null)
const engine = ref<string | undefined>()
const loadError = ref<string | null>(null)
const now = ref(Date.now())
const aboutOpen = ref(false)
let timer: number | undefined
let stopped = false

async function load(): Promise<void> {
  try {
    const res = await listMachineLabs()
    labs.value = res.labs
    engine.value = res.engine
    loadError.value = null
    now.value = Date.now()
  } catch (err) {
    if (isUnauthorized(err)) {
      stopped = true
      emit('unauthorized')
      return
    }
    loadError.value = err instanceof Error ? err.message : String(err)
  }
  if (!stopped) timer = window.setTimeout(load, POLL_MS)
}

onMounted(load)
onBeforeUnmount(() => {
  stopped = true
  window.clearTimeout(timer)
})

/** Reloads the list now, for example after a lab was removed. */
function reload(): void {
  window.clearTimeout(timer)
  void load()
}
defineExpose({ reload })

const running = computed(() => (labs.value ?? []).filter((l) => l.state !== 'stopped'))
const stoppedLabs = computed(() => (labs.value ?? []).filter((l) => l.state === 'stopped'))

function kind(l: MachineLab): string {
  if (l.playground) return 'Playground'
  if (l.mode === 'exam') return 'Exam'
  return 'Lab'
}

function stateLabel(l: MachineLab): string {
  switch (l.state) {
    case 'running':
      return l.runtime === 'qemu' ? `Running · ${l.vms ?? 1} VM${(l.vms ?? 1) === 1 ? '' : 's'}` : 'Running'
    case 'degraded':
      return 'Partly stopped'
    case 'stopped':
      return 'Stopped'
    default:
      return 'Unknown'
  }
}

function ends(l: MachineLab): string {
  if (!l.deadlineAt) return ''
  const ms = Date.parse(l.deadlineAt) - now.value
  if (Number.isNaN(ms)) return ''
  if (ms <= 0) return l.playground ? 'time is up; it is being removed' : 'time is up'
  const min = Math.ceil(ms / 60_000)
  const text = min < 60 ? `${min} min` : `${Math.floor(min / 60)} h ${min % 60} min`
  return l.playground ? `deleted automatically in ${text}` : `${text} left`
}

// The page opens a run the workspace has; another Kind lab needs
// `astrona ui <name>` once, which records it for the browser.
const canOpen = (l: MachineLab) => l.workspace && l.runtime === 'kind' && l.state !== 'stopped'
</script>

<template>
  <main class="intro">
    <section class="panel intro__card home" aria-labelledby="home-title">
      <div class="home__head">
        <div class="brand">
          <BrandMark />
          <p class="eyebrow eyebrow--page">Astrona workspace</p>
        </div>
        <button type="button" class="btn btn--sm btn--ghost" aria-haspopup="dialog" @click="aboutOpen = true">About</button>
      </div>
      <h1 id="home-title" class="intro__title">Labs on this computer</h1>
      <p class="muted">
        A lab keeps running in the background after <code>astrona run</code> returns, until you end it (a
        playground also ends when its time is up). Each one uses CPU and memory, so end the ones you are done with.
      </p>

      <p v-if="engine" class="dialog__error" role="status">{{ engine }}</p>
      <p v-if="loadError" class="error" role="alert">Could not list the labs: {{ loadError }}. Retrying…</p>

      <p v-if="labs === null && !loadError" class="readiness readiness--busy" role="status">
        <span class="dot" aria-hidden="true" />Looking for labs…
      </p>

      <template v-else-if="labs">
        <div v-if="running.length === 0" class="home__empty">
          <p><strong>No lab is running.</strong> Nothing astrona started uses this computer right now.</p>
          <p class="muted small">Start one from your terminal; it opens here:</p>
          <pre class="message__command"><code>astrona run &lt;lab&gt;</code></pre>
        </div>

        <ul v-else class="home__list" aria-label="Running labs">
          <li v-for="l in running" :key="l.lab" class="home__lab">
            <div class="home__main">
              <p class="home__kind eyebrow">{{ kind(l) }} · {{ l.runtime === 'qemu' ? 'QEMU' : 'Kind' }}</p>
              <h2 class="home__title">{{ l.title || l.name }}</h2>
              <p class="home__meta small muted">
                <span class="home__state" :class="`home__state--${l.state}`">
                  <span class="dot" aria-hidden="true" />{{ stateLabel(l) }}
                </span>
                <span v-if="l.uptime"> · up {{ l.uptime }}</span>
                <span v-if="ends(l)"> · {{ ends(l) }}</span>
                <span v-if="l.title"> · <code>{{ l.name }}</code></span>
              </p>
            </div>
            <div class="home__actions">
              <template v-if="canOpen(l)">
                <button type="button" class="btn btn--sm btn--primary" @click="emit('open', l.lab)">Open</button>
                <button type="button" class="btn btn--sm btn--ghost" @click="emit('remove', l)">
                  {{ l.playground ? 'End playground' : 'Remove' }}
                </button>
              </template>
              <p v-else class="small muted home__cmd">
                <template v-if="l.runtime === 'kind'">Open it here with <code>astrona ui {{ l.name }}</code>, or</template>
                <template v-else>Connect with <code>astrona status {{ l.name }}</code>, or</template>
                remove it with <code>astrona destroy {{ l.name }}</code>
              </p>
            </div>
          </li>
        </ul>

        <p v-if="stoppedLabs.length" class="small muted">
          Stopped but not removed: <code>{{ stoppedLabs.map((l) => l.name).join(', ') }}</code>.
          <code>astrona destroy &lt;lab&gt;</code> removes one.
        </p>
      </template>
    </section>
    <AboutDialog v-if="aboutOpen" @close="aboutOpen = false" />
  </main>
</template>
