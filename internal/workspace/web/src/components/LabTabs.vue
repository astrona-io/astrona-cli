<script setup lang="ts">
// The right panel's tabs. It opens with the tabs the lab declares, the
// focus tab (the terminal on the lab's own cluster) first. The learner can
// close any tab with its ×, except the last terminal, and open more from the
// + menu: another terminal (on a VM or cluster of the run, when there are
// several), the task, the prerequisites, the solution guide or, when the
// lab has several, each solution from a Solutions group (after one
// confirmation for all of them; never in an exam or a playground), the
// lab's documentation links and any of the lab's own tabs that were closed. A documentation
// badge above the task opens its link here too (openResource), or shows its
// tab when it is already open. Closing a terminal ends its shell, after a
// confirmation. The open tabs are remembered per run in localStorage, so a
// reload keeps them.
//
// A tab's view is created the first time it is shown and then kept, so a
// terminal keeps its shell and scrollback and a page is not reloaded on
// every switch.
//
// Keyboard: the ARIA tablist pattern with manual activation (opening a
// terminal tab starts a container, so arrows only move focus): Left/Right,
// Home/End move between tabs, Enter or Space shows the focused one, Delete
// closes it. The + menu is an ARIA menu: Up/Down move, Esc closes it.
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, reactive, ref, type Component } from 'vue'
import { closeTerminalTab, isRunGone, isUnauthorized, type Run, type RunTab, type SolutionSummary, type TabKind } from '../api'
import { views, type TabSource, type ViewHandle, type ViewStatus, type WorkspaceTab } from '../views/types'
import type { EndCopy } from '../endSession'
import { resourceTabName, shownDocLinks, type ShownDocLink } from '../docLinks'
import ConfirmDialog from './ConfirmDialog.vue'

const props = defineProps<{ run: Run }>()
const emit = defineEmits<{
  status: [ViewStatus]
  leave: []
  unauthorized: []
  runGone: []
}>()

/** The server's cap on terminals per lab (maxTerminalsPerLab). */
const MAX_TERMINALS = 12

const components = Object.fromEntries(
  Object.entries(views).map(([kind, load]) => [kind, defineAsyncComponent(load)]),
) as Record<TabKind, Component>

const lab = computed(() => props.run.lab)
const runId = computed(() => props.run.runId)
const labTabs = computed(() => props.run.tabs.filter((t) => t.kind in components))
const terminalTargets = computed(() => props.run.targets.filter((t) => t.views.includes('terminal')))
const docLinks = computed(() => shownDocLinks(props.run.docLinks ?? []))

// ---------------------------------------------------------------- storage

const STORE_PREFIX = 'astrona.workspace.tabs.'
const storeKey = computed(() => `${STORE_PREFIX}${lab.value}.${runId.value}`)

/** What a reload needs to open the same tabs again. */
interface StoredTab {
  name: string
  source: TabSource
  title?: string
  target?: string
  questionId?: string
  confirmed?: boolean
  /** resource: the documentation link it shows. */
  url?: string
  /** solution: which of the lab's solutions. */
  solutionName?: string
}

interface Stored {
  open: StoredTab[]
  active: string
}

function readStored(): Stored | null {
  try {
    const raw = window.localStorage.getItem(storeKey.value)
    if (!raw) return null
    const v = JSON.parse(raw) as Stored
    return Array.isArray(v.open) ? v : null
  } catch {
    return null
  }
}

function save(): void {
  const v: Stored = {
    active: active.value,
    open: open.map((t) => ({
      name: t.name,
      source: t.source,
      title: t.source === 'terminal' ? t.title : undefined,
      target: t.source === 'terminal' ? t.target : undefined,
      questionId: t.questionId,
      confirmed: t.confirmed || undefined,
      url: t.source === 'resource' ? t.url : undefined,
      solutionName: t.source === 'solution' ? t.solutionName : undefined,
    })),
  }
  try {
    // Older runs of this lab are gone; forget their tabs.
    const prefix = `${STORE_PREFIX}${lab.value}.`
    for (let i = window.localStorage.length - 1; i >= 0; i--) {
      const k = window.localStorage.key(i)
      if (k && k.startsWith(prefix) && k !== storeKey.value) window.localStorage.removeItem(k)
    }
    window.localStorage.setItem(storeKey.value, JSON.stringify(v))
  } catch {
    // storage unavailable: the tabs last until reload
  }
}

// ---------------------------------------------------------------- tabs

function fromLab(t: RunTab, confirmed = false): WorkspaceTab {
  return { ...t, source: 'lab', confirmed: t.solution ? confirmed : undefined }
}

function taskTab(questionId: string): WorkspaceTab | null {
  const qs = props.run.questions
  const i = qs.findIndex((q) => q.id === questionId)
  if (i < 0) return null
  const title = qs.length > 1 ? `Task ${i + 1}` : 'Task'
  return { name: `@task-${i + 1}`, title, kind: 'docs', focus: false, document: true, source: 'task', questionId }
}

function prerequisitesTab(): WorkspaceTab | null {
  const html = props.run.intro?.prerequisitesHtml ?? ''
  if (!html) return null
  return { name: '@prerequisites', title: 'Prerequisites', kind: 'docs', focus: false, document: true, source: 'prerequisites', html }
}

/**
 * The lab's solutions, in order. An older server lists none but may still
 * have the default (run.solution).
 */
const solutions = computed<SolutionSummary[]>(() => {
  const list = props.run.solutions ?? []
  if (list.length > 0) return list
  return props.run.solution ? [{ name: 'default', title: 'Solution guide' }] : []
})

/**
 * The tab of one solution. The default keeps the name @solution; with one
 * solution it is the "Solution guide", with several each is its title.
 */
function solutionTab(name = 'default'): WorkspaceTab | null {
  const s = solutions.value.find((x) => x.name === name)
  if (!s) return null
  const title = solutions.value.length === 1 ? 'Solution guide' : s.title
  const tabName = name === 'default' ? '@solution' : `@solution-${name}`
  return { name: tabName, title, kind: 'docs', focus: false, document: true, source: 'solution', solution: true, solutionName: name, confirmed: solutionsConfirmed.value || undefined }
}

function resourceTab(link: ShownDocLink): WorkspaceTab {
  return { name: resourceTabName(link), title: link.name, kind: 'docs', focus: false, url: link.url, source: 'resource' }
}

function terminalTab(name: string, target: string, title: string): WorkspaceTab | null {
  if (!terminalTargets.value.some((t) => t.name === target)) return null
  return { name, title, kind: 'terminal', focus: false, target, source: 'terminal' }
}

/** A stored tab as it opens now, or null when the run no longer has it. */
function restore(s: StoredTab): WorkspaceTab | null {
  switch (s.source) {
    case 'lab': {
      const t = labTabs.value.find((l) => l.name === s.name)
      return t ? fromLab(t, !!s.confirmed) : null
    }
    case 'terminal':
      return /^term-[a-z0-9]{4,16}$/.test(s.name) && s.target ? terminalTab(s.name, s.target, s.title || 'Terminal') : null
    case 'task':
      return s.questionId ? taskTab(s.questionId) : null
    case 'prerequisites':
      return prerequisitesTab()
    case 'resource': {
      const link = docLinks.value.find((l) => l.url === s.url)
      return link ? resourceTab(link) : null
    }
    case 'solution': {
      const t = solutionTab(s.solutionName ?? 'default')
      if (t && s.confirmed) {
        t.confirmed = true
        solutionsConfirmed.value = true
      }
      return t
    }
    default:
      return null
  }
}

// One confirmation covers every solution of the run: once the learner has
// said yes to one, the others open without asking again.
const solutionsConfirmed = ref(false)

function initialTabs(): { tabs: WorkspaceTab[]; active: string } {
  const stored = readStored()
  if (stored?.open.some((s) => s && (s.source === 'solution' || s.source === 'lab') && s.confirmed)) solutionsConfirmed.value = true
  let tabs: WorkspaceTab[] = []
  if (stored) {
    const seen = new Set<string>()
    for (const s of stored.open) {
      const t = s && typeof s.name === 'string' ? restore(s) : null
      if (t && !seen.has(t.name)) {
        seen.add(t.name)
        tabs.push(t)
      }
    }
  }
  if (!tabs.some((t) => t.kind === 'terminal')) tabs = labTabs.value.map((t) => fromLab(t, solutionsConfirmed.value))
  if (solutionsConfirmed.value) for (const t of tabs) if (t.solution) t.confirmed = true
  const usable = (name: string | undefined) => tabs.find((t) => t.name === name && !(t.solution && !t.confirmed))
  const first = tabs.find((t) => !(t.solution && !t.confirmed))
  return { tabs, active: usable(stored?.active)?.name ?? first?.name ?? '' }
}

const init = initialTabs()
const open = reactive<WorkspaceTab[]>(init.tabs)
const active = ref(init.active)
const focused = ref(active.value)
const shown = reactive(new Set<string>(active.value ? [active.value] : []))
const statuses = reactive<Record<string, ViewStatus>>({})
const handles: Record<string, ViewHandle | null> = {}
const buttons: Record<string, HTMLButtonElement | null> = {}

const terminalCount = computed(() => open.filter((t) => t.kind === 'terminal').length)
const atTerminalCap = computed(() => terminalCount.value >= MAX_TERMINALS)

function tabId(name: string): string {
  return `lab-tab-${name}`
}

function panelId(name: string): string {
  return `lab-panel-${name}`
}

function find(name: string): WorkspaceTab | undefined {
  return open.find((t) => t.name === name)
}

function select(name: string): void {
  const tab = find(name)
  if (!tab) return
  if (tab.solution && !tab.confirmed) {
    askSolution(tab)
    return
  }
  active.value = name
  focused.value = name
  shown.add(name)
  save()
  emit('status', statuses[name] ?? { tone: 'busy', label: 'Loading' })
}

/** Adds tab after the active one (or shows it, when already open). */
function add(tab: WorkspaceTab): void {
  if (!find(tab.name)) {
    const i = open.findIndex((t) => t.name === active.value)
    open.splice(i < 0 ? open.length : i + 1, 0, tab)
  }
  select(tab.name)
  void nextTick(() => buttons[tab.name]?.focus())
}

function onStatus(name: string, status: ViewStatus): void {
  statuses[name] = status
  if (name === active.value) emit('status', status)
}

function moveFocus(name: string): void {
  focused.value = name
  buttons[name]?.focus()
}

function onKeydown(ev: KeyboardEvent): void {
  const list = open
  const i = list.findIndex((t) => t.name === focused.value)
  if (i < 0 || list.length === 0) return
  let next = -1
  switch (ev.key) {
    case 'ArrowRight':
      next = (i + 1) % list.length
      break
    case 'ArrowLeft':
      next = (i - 1 + list.length) % list.length
      break
    case 'Home':
      next = 0
      break
    case 'End':
      next = list.length - 1
      break
    case 'Delete':
      ev.preventDefault()
      requestClose(list[i])
      return
    default:
      return
  }
  ev.preventDefault()
  moveFocus(list[next].name)
}

function tone(name: string): string {
  return `tabs__dot conn--${statuses[name]?.tone ?? 'busy'}`
}

// ---------------------------------------------------------------- closing

/** The last terminal stays: the learner always has a shell. */
function closable(tab: WorkspaceTab): boolean {
  return tab.kind !== 'terminal' || terminalCount.value > 1
}

function closeLabel(tab: WorkspaceTab): string {
  return closable(tab) ? `Close ${tab.title}` : 'The last terminal cannot be closed'
}

function remove(tab: WorkspaceTab): void {
  const i = open.findIndex((t) => t.name === tab.name)
  if (i < 0) return
  open.splice(i, 1)
  shown.delete(tab.name)
  delete statuses[tab.name]
  delete handles[tab.name]
  delete buttons[tab.name]
  if (active.value === tab.name) {
    const next = open[Math.min(i, open.length - 1)] ?? open[i - 1]
    const usable = next && !(next.solution && !next.confirmed) ? next : open.find((t) => !(t.solution && !t.confirmed))
    if (usable) {
      select(usable.name)
      void nextTick(() => buttons[usable.name]?.focus())
    } else {
      active.value = ''
      save()
    }
  } else {
    if (focused.value === tab.name) focused.value = active.value
    save()
  }
}

function requestClose(tab: WorkspaceTab): void {
  if (!closable(tab)) return
  if (tab.kind === 'terminal') {
    dialog.value = { kind: 'close', tab }
    dialogError.value = null
    return
  }
  remove(tab)
}

// ---------------------------------------------------------------- dialogs

type Dialog = { kind: 'close'; tab: WorkspaceTab } | { kind: 'solution'; tab: WorkspaceTab }

const dialog = ref<Dialog | null>(null)
const dialogBusy = ref(false)
const dialogError = ref<string | null>(null)

const dialogCopy = computed<EndCopy>(() => {
  const d = dialog.value
  if (d?.kind === 'close') {
    return {
      button: 'Close',
      title: `Close ${d.tab.title}?`,
      paragraphs: [
        'Closing this tab ends its shell: anything still running in it stops and its scrollback is gone.',
        'Your files and everything in the lab’s environment stay. You can open a new terminal from the + menu.',
      ],
      confirm: 'Close terminal',
      busy: 'Closing the terminal…',
    }
  }
  const several = solutions.value.length > 1
  return {
    button: 'Show solution',
    title: several ? 'Show the solutions?' : 'Show the solution guide?',
    paragraphs: [
      several
        ? 'This shows a full solution, and then every other solution of this lab without asking again. Your attempt still counts.'
        : 'This shows the full solution. Your attempt still counts.',
      'Try the task on your own first: working it out is how it sticks.',
    ],
    confirm: 'Show solution',
    busy: 'Opening…',
  }
})

function askSolution(tab: WorkspaceTab): void {
  if (solutionsConfirmed.value) {
    tab.confirmed = true
    add(tab)
    return
  }
  dialog.value = { kind: 'solution', tab }
  dialogError.value = null
}

function cancelDialog(): void {
  if (dialogBusy.value) return
  dialog.value = null
  dialogError.value = null
}

async function confirmDialog(): Promise<void> {
  const d = dialog.value
  if (!d) return
  if (d.kind === 'solution') {
    dialog.value = null
    solutionsConfirmed.value = true
    for (const t of open) if (t.solution) t.confirmed = true
    d.tab.confirmed = true
    add(d.tab)
    return
  }
  dialogBusy.value = true
  dialogError.value = null
  try {
    await closeTerminalTab(lab.value, runId.value, d.tab.name)
    dialog.value = null
    remove(d.tab)
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
    else if (isRunGone(err)) emit('runGone')
    else dialogError.value = err instanceof Error ? err.message : String(err)
  } finally {
    dialogBusy.value = false
  }
}

// ---------------------------------------------------------------- + menu

interface MenuItem {
  key: string
  label: string
  hint?: string
  disabled?: boolean
  run: () => void
}

interface MenuGroup {
  label: string
  items: MenuItem[]
}

const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement>()
const menuRoot = ref<HTMLElement>()
const menuItems: Record<string, HTMLButtonElement | null> = {}

function randomId(): string {
  const bytes = new Uint8Array(4)
  window.crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

function newTerminal(target: string): void {
  if (atTerminalCap.value) return
  const t = terminalTargets.value.find((x) => x.name === target)
  const many = terminalTargets.value.length > 1
  const base = many && t ? t.label : 'Terminal'
  const same = open.filter((x) => x.kind === 'terminal' && (!many || x.target === target)).length
  const tab = terminalTab(`term-${randomId()}`, target, `${base} ${same + 1}`)
  if (tab) add(tab)
}

const groups = computed<MenuGroup[]>(() => {
  const out: MenuGroup[] = []
  const capHint = atTerminalCap.value ? `At most ${MAX_TERMINALS} terminals; close one to open another` : undefined
  const targets = terminalTargets.value
  if (targets.length > 0) {
    out.push({
      label: 'Terminal',
      items:
        targets.length === 1
          ? [{ key: 'term', label: 'New terminal', hint: capHint, disabled: atTerminalCap.value, run: () => newTerminal(targets[0].name) }]
          : targets.map((t) => ({
              key: `term-${t.name}`,
              label: `New terminal on ${t.label}`,
              hint: capHint,
              disabled: atTerminalCap.value,
              run: () => newTerminal(t.name),
            })),
    })
  }
  const docs: MenuItem[] = []
  for (const q of props.run.questions) {
    const t = taskTab(q.id)
    if (t) docs.push({ key: t.name, label: t.title, run: () => add(find(t.name) ?? t) })
  }
  const pre = prerequisitesTab()
  if (pre) docs.push({ key: pre.name, label: pre.title, run: () => add(find(pre.name) ?? pre) })
  const solutionItems: MenuItem[] = []
  for (const s of solutions.value) {
    const sol = solutionTab(s.name)
    if (!sol) continue
    solutionItems.push({
      key: sol.name,
      label: sol.title,
      hint: solutions.value.length > 1 && s.summary ? s.summary : solutionsConfirmed.value ? undefined : 'Asks first',
      run: () => {
        const existing = find(sol.name)
        if (existing) select(existing.name)
        else askSolution(sol)
      },
    })
  }
  // One solution stays the "Solution guide" among the documents; several
  // get their own group, each with its title and summary.
  if (solutionItems.length === 1) docs.push(solutionItems[0])
  if (docs.length > 0) out.push({ label: 'Documents', items: docs })
  if (solutionItems.length > 1) out.push({ label: 'Solutions', items: solutionItems })
  if (docLinks.value.length > 0) {
    out.push({
      label: 'Resources',
      items: docLinks.value.map((l) => ({ key: resourceTabName(l), label: l.name, hint: l.host, run: () => openResource(l) })),
    })
  }
  const closed = labTabs.value.filter((t) => !find(t.name))
  if (closed.length > 0) {
    out.push({
      label: 'Reopen',
      items: closed.map((t) => {
        const cap = t.kind === 'terminal' && atTerminalCap.value
        return {
          key: `lab-${t.name}`,
          label: t.title,
          hint: cap ? capHint : undefined,
          disabled: cap,
          run: () => {
            const tab = fromLab(t)
            if (tab.solution) askSolution(tab)
            else add(tab)
          },
        }
      }),
    })
  }
  return out
})

/** Opens a documentation link as a tab, or shows its tab when already open. */
function openResource(link: ShownDocLink): void {
  const t = resourceTab(link)
  add(find(t.name) ?? t)
}

const flatItems = computed(() => groups.value.flatMap((g) => g.items).filter((i) => !i.disabled))

async function openMenu(): Promise<void> {
  menuOpen.value = true
  document.addEventListener('pointerdown', onOutside, true)
  await nextTick()
  const first = flatItems.value[0]
  if (first) menuItems[first.key]?.focus()
}

function closeMenu(refocus = true): void {
  if (!menuOpen.value) return
  menuOpen.value = false
  document.removeEventListener('pointerdown', onOutside, true)
  if (refocus) menuButton.value?.focus()
}

function toggleMenu(): void {
  if (menuOpen.value) closeMenu()
  else void openMenu()
}

function onOutside(ev: Event): void {
  if (menuRoot.value && ev.target instanceof Node && menuRoot.value.contains(ev.target)) return
  closeMenu(false)
}

function choose(item: MenuItem): void {
  if (item.disabled) return
  closeMenu(false)
  item.run()
}

function onMenuKeydown(ev: KeyboardEvent): void {
  const items = flatItems.value
  const i = items.findIndex((it) => menuItems[it.key] === document.activeElement)
  let next = -1
  switch (ev.key) {
    case 'Escape':
      ev.preventDefault()
      closeMenu()
      return
    case 'Tab':
      closeMenu(false)
      return
    case 'ArrowDown':
      next = (i + 1) % items.length
      break
    case 'ArrowUp':
      next = (i - 1 + items.length) % items.length
      break
    case 'Home':
      next = 0
      break
    case 'End':
      next = items.length - 1
      break
    default:
      return
  }
  ev.preventDefault()
  if (items[next]) menuItems[items[next].key]?.focus()
}

onBeforeUnmount(() => document.removeEventListener('pointerdown', onOutside, true))

save()

// A suggested command: into the active terminal, or the first terminal tab
// (shown first) when the active tab is not one. Never with Enter.
async function paste(text: string): Promise<boolean> {
  let tab = find(active.value)
  if (tab?.kind !== 'terminal') tab = open.find((t) => t.kind === 'terminal')
  if (!tab) return false
  if (tab.name !== active.value) select(tab.name)
  // A terminal shown for the first time loads its view first.
  for (let i = 0; i < 40 && !handles[tab.name]?.paste; i++) {
    await nextTick()
    await new Promise((r) => window.setTimeout(r, 50))
  }
  const handle = handles[tab.name]
  if (!handle?.paste) return false
  handle.paste(text)
  return true
}

// The status bar under the tabs: which cluster the terminals talk to.
const env = computed(() => props.run.environment)
const statusBits = computed(() => {
  const e = env.value
  if (!e) return []
  const out: { key: string; value: string }[] = []
  if (e.context) out.push({ key: 'ctx', value: e.context })
  if (e.namespace) out.push({ key: 'ns', value: e.namespace })
  if (e.kubernetesVersion) out.push({ key: 'k8s', value: e.kubernetesVersion })
  return out
})
const hasTerminal = computed(() => open.some((t) => t.kind === 'terminal'))

defineExpose({
  focus: () => handles[active.value]?.focus(),
  openResource,
  paste,
})
</script>

<template>
  <div class="tabs">
    <div class="tabs__bar">
      <div class="tabs__list" role="tablist" aria-label="Lab tabs" @keydown="onKeydown">
        <div
          v-for="tab in open"
          :key="tab.name"
          class="tabs__item"
          :class="{ 'tabs__item--active': tab.name === active }"
        >
          <button
            :id="tabId(tab.name)"
            :ref="(el) => (buttons[tab.name] = el as HTMLButtonElement | null)"
            type="button"
            role="tab"
            class="tabs__tab"
            :aria-selected="tab.name === active"
            :aria-controls="panelId(tab.name)"
            :aria-haspopup="tab.solution && !tab.confirmed ? 'dialog' : undefined"
            :tabindex="tab.name === focused ? 0 : -1"
            @click="select(tab.name)"
            @focus="focused = tab.name"
            @blur="focused = active"
          >
            <span v-if="tab.kind === 'terminal' && shown.has(tab.name)" :class="tone(tab.name)" aria-hidden="true">
              <span class="dot" />
            </span>
            <span class="tabs__title">{{ tab.title }}</span>
          </button>
          <button
            type="button"
            class="tabs__close"
            tabindex="-1"
            :disabled="!closable(tab)"
            :aria-label="closeLabel(tab)"
            :title="closeLabel(tab)"
            @click="requestClose(tab)"
          >
            <span aria-hidden="true">×</span>
          </button>
        </div>
      </div>
      <p v-if="hasTerminal" class="tabs__hint small muted"><kbd>F6</kbd> leave terminal</p>
      <div ref="menuRoot" class="tabs__add">
        <button
          ref="menuButton"
          type="button"
          class="tabs__add-button"
          aria-haspopup="menu"
          :aria-expanded="menuOpen"
          aria-controls="tabs-menu"
          aria-label="Open a tab"
          title="Open a tab"
          @click="toggleMenu"
        >
          <span aria-hidden="true">+</span>
        </button>
        <div v-if="menuOpen" id="tabs-menu" class="tabs__menu" role="menu" aria-label="Open a tab" @keydown="onMenuKeydown">
          <template v-for="(group, gi) in groups" :key="group.label">
            <div v-if="gi > 0" class="tabs__menu-sep" role="separator" />
            <p class="tabs__menu-label" aria-hidden="true">{{ group.label }}</p>
            <button
              v-for="item in group.items"
              :key="item.key"
              :ref="(el) => (menuItems[item.key] = el as HTMLButtonElement | null)"
              type="button"
              role="menuitem"
              class="tabs__menu-item"
              tabindex="-1"
              :aria-disabled="item.disabled || undefined"
              @click="choose(item)"
            >
              <span>{{ item.label }}</span>
              <span v-if="item.hint" class="tabs__menu-hint">{{ item.hint }}</span>
            </button>
          </template>
          <p v-if="groups.length === 0" class="tabs__menu-label">Nothing to open.</p>
        </div>
      </div>
    </div>
    <div
      v-for="tab in open"
      v-show="tab.name === active"
      :id="panelId(tab.name)"
      :key="tab.name"
      class="tabs__panel"
      role="tabpanel"
      :aria-labelledby="tabId(tab.name)"
    >
      <component
        :is="components[tab.kind]"
        v-if="shown.has(tab.name)"
        :ref="(el: unknown) => (handles[tab.name] = el as ViewHandle | null)"
        :lab="lab"
        :run-id="runId"
        :tab="tab"
        @status="onStatus(tab.name, $event)"
        @leave="emit('leave')"
        @unauthorized="emit('unauthorized')"
        @run-gone="emit('runGone')"
      />
    </div>
    <p v-if="open.length === 0" class="view__empty muted" role="status">This lab has nothing to show here.</p>
    <footer v-if="hasTerminal" class="statusbar small">
      <p v-for="b in statusBits" :key="b.key" class="statusbar__item">
        <span class="statusbar__key">{{ b.key }}</span>
        <span class="statusbar__value mono">{{ b.value }}</span>
      </p>
      <p class="statusbar__note muted">Your shell keeps running if you reload or close this page</p>
    </footer>
  </div>

  <ConfirmDialog
    v-if="dialog"
    :copy="dialogCopy"
    :busy="dialogBusy"
    :error="dialogError"
    :danger="dialog.kind === 'close'"
    @confirm="confirmDialog"
    @cancel="cancelDialog"
  />
</template>
