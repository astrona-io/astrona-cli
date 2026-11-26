<script setup lang="ts">
import { computed, onMounted, ref, shallowRef, toRef } from 'vue'
import {
  getCatalogSuggestions,
  isUnauthorized,
  listMachineLabs,
  type CatalogSuggestions,
  type MachineLab,
  type Run,
} from '../api'
import { formatDuration, useCountdown } from '../clock'

// "All labs" from inside the workspace: this run, the other labs running on
// this computer (switch to one without ending this one), and what to try
// next from the Astrona catalog. A native modal <dialog> drawn as a drawer:
// showModal() keeps the focus inside and Esc closes it. The full list, with
// stopped labs and Remove, stays on the home screen (emit home).

const props = defineProps<{ run: Run }>()
const emit = defineEmits<{ close: []; open: [lab: string]; home: []; unauthorized: [] }>()

const dialog = ref<HTMLDialogElement>()
const closeButton = ref<HTMLButtonElement>()
let opener: Element | null = null

const labs = shallowRef<MachineLab[] | null>(null)
const labsError = ref<string | null>(null)
const catalog = shallowRef<CatalogSuggestions | null>(null)
const catalogError = ref<string | null>(null)
const switching = ref<string | null>(null)

const left = useCountdown(toRef(() => props.run.deadlineAt))
const kind = computed(() => (props.run.playground ? 'Playground' : props.run.mode === 'exam' ? 'Exam' : 'Lab'))
const hereMeta = computed(() => (left.value === null ? kind.value : `${kind.value} · ${formatDuration(left.value, { hours: true })} left`))

const others = computed(() => (labs.value ?? []).filter((l) => l.lab !== props.run.lab && l.state !== 'stopped'))

function labKind(l: MachineLab): string {
  if (l.playground) return 'Playground'
  if (l.mode === 'exam') return 'Exam'
  return 'Lab'
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

async function loadLabs(): Promise<void> {
  try {
    labs.value = (await listMachineLabs()).labs
    labsError.value = null
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
    else labsError.value = message(err)
  }
}

async function loadCatalog(): Promise<void> {
  try {
    catalog.value = await getCatalogSuggestions(props.run.lab)
    catalogError.value = null
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
    else catalogError.value = message(err)
  }
}

/** Only http(s) links from the catalog are shown as links. */
function safeUrl(raw: string | undefined): string {
  if (!raw) return ''
  try {
    const u = new URL(raw)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.href : ''
  } catch {
    return ''
  }
}

const catalogUrl = computed(() => safeUrl(catalog.value?.catalogUrl))

onMounted(() => {
  opener = document.activeElement
  dialog.value?.showModal()
  closeButton.value?.focus()
  void loadLabs()
  void loadCatalog()
})

function close(): void {
  dialog.value?.close()
  if (opener instanceof HTMLElement) opener.focus()
  emit('close')
}

function onCancel(ev: Event): void {
  ev.preventDefault()
  close()
}

function switchTo(lab: string): void {
  switching.value = lab
  emit('open', lab)
}
</script>

<template>
  <dialog ref="dialog" class="dialog drawer" aria-labelledby="drawer-title" @cancel="onCancel">
    <div class="drawer__card">
      <div class="drawer__head">
        <h2 id="drawer-title" class="dialog__title">All labs</h2>
        <button ref="closeButton" type="button" class="btn btn--ghost btn--sm" @click="close">Close</button>
      </div>

      <section class="drawer__section" aria-labelledby="drawer-running">
        <h3 id="drawer-running" class="eyebrow">Running now</h3>
        <ul class="drawer__list">
          <li class="labcard labcard--here">
            <div class="labcard__text">
              <span class="labcard__name mono">{{ run.lab }}</span>
              <span class="small muted">{{ hereMeta }}</span>
            </div>
            <span class="labcard__here small">You're here</span>
          </li>
          <li v-for="l in others" :key="l.lab" class="labcard">
            <div class="labcard__text">
              <span class="labcard__name mono">{{ l.name || l.lab }}</span>
              <span class="small muted">{{ labKind(l) }}<template v-if="l.title"> · {{ l.title }}</template></span>
            </div>
            <button
              v-if="l.workspace"
              type="button"
              class="btn btn--soft btn--sm"
              :disabled="switching !== null"
              @click="switchTo(l.lab)"
            >
              {{ switching === l.lab ? 'Opening…' : 'Switch' }}
            </button>
            <span v-else class="small muted">Not in this workspace</span>
          </li>
        </ul>
        <p v-if="labsError" class="error small" role="alert">Could not list the labs on this computer: {{ labsError }}</p>
        <p v-else-if="labs === null" class="small muted">Looking for other labs…</p>
        <p class="small muted">
          Your lab and timer keep running while you browse. Starting another lab won't stop this one until you end it.
        </p>
        <p class="small">
          <button type="button" class="link-btn" @click="emit('home')">Every lab on this computer, with stopped ones</button>
        </p>
      </section>

      <section class="drawer__section" aria-labelledby="drawer-next">
        <h3 id="drawer-next" class="eyebrow">Continue learning</h3>
        <p v-if="catalogError" class="error small" role="alert">Could not load suggestions: {{ catalogError }}</p>
        <p v-else-if="catalog === null" class="small muted">Loading suggestions…</p>
        <p v-else-if="catalog.labs.length === 0" class="small muted">No suggestions for this lab.</p>
        <ul v-else class="drawer__list">
          <li v-for="s in catalog.labs" :key="s.id" class="labcard labcard--next">
            <div class="labcard__text">
              <a v-if="safeUrl(s.url)" class="labcard__name" :href="safeUrl(s.url)" target="_blank" rel="noopener noreferrer">
                {{ s.title }} <span aria-hidden="true">↗</span>
              </a>
              <span v-else class="labcard__name">{{ s.title }}</span>
              <span v-if="s.summary" class="small muted">{{ s.summary }}</span>
              <code v-if="s.runCommand" class="labcard__cmd small">{{ s.runCommand }}</code>
            </div>
          </li>
        </ul>
        <p v-if="catalogUrl" class="small">
          <a :href="catalogUrl" target="_blank" rel="noopener noreferrer">Browse the full catalog on astrona.io <span aria-hidden="true">↗</span></a>
        </p>
      </section>
    </div>
  </dialog>
</template>
