<script setup lang="ts">
// The right panel: the tabs the lab declares, the focus tab (the terminal on
// the lab's own cluster) first. The learner switches between them; there is
// no way to add or close one. A tab's view is created the first time it is
// shown and then kept, so a terminal keeps its shell and scrollback and a
// page is not reloaded on every switch.
//
// Keyboard: the ARIA tablist pattern with manual activation (opening a
// terminal tab starts a container, so arrows only move focus): Left/Right,
// Home/End move between tabs, Enter or Space shows the focused one.
import { computed, defineAsyncComponent, reactive, ref, type Component } from 'vue'
import type { RunTab, TabKind } from '../api'
import { views, type ViewHandle, type ViewStatus } from '../views/types'

const props = defineProps<{ lab: string; runId: string; tabs: RunTab[] }>()
const emit = defineEmits<{
  status: [ViewStatus]
  leave: []
  unauthorized: []
  runGone: []
}>()

const components = Object.fromEntries(
  Object.entries(views).map(([kind, load]) => [kind, defineAsyncComponent(load)]),
) as Record<TabKind, Component>

const known = computed(() => props.tabs.filter((t) => t.kind in components))
const storeKey = computed(() => `astrona.workspace.tab.${props.lab}.${props.runId}`)

function storedTab(): string | null {
  try {
    return window.sessionStorage.getItem(storeKey.value)
  } catch {
    return null
  }
}

function initialTab(): string {
  const stored = storedTab()
  if (stored && known.value.some((t) => t.name === stored)) return stored
  return known.value[0]?.name ?? ''
}

const active = ref(initialTab())
const focused = ref(active.value)
const shown = reactive(new Set<string>(active.value ? [active.value] : []))
const statuses = reactive<Record<string, ViewStatus>>({})
const handles: Record<string, ViewHandle | null> = {}
const buttons: Record<string, HTMLButtonElement | null> = {}

function tabId(name: string): string {
  return `lab-tab-${name}`
}

function panelId(name: string): string {
  return `lab-panel-${name}`
}

function select(name: string): void {
  if (!known.value.some((t) => t.name === name)) return
  active.value = name
  focused.value = name
  shown.add(name)
  try {
    window.sessionStorage.setItem(storeKey.value, name)
  } catch {
    // storage unavailable: the choice lasts until reload
  }
  emit('status', statuses[name] ?? { tone: 'busy', label: 'Loading' })
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
  const list = known.value
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
    default:
      return
  }
  ev.preventDefault()
  moveFocus(list[next].name)
}

function tone(name: string): string {
  return `tabs__dot conn--${statuses[name]?.tone ?? 'busy'}`
}

defineExpose({
  focus: () => handles[active.value]?.focus(),
})
</script>

<template>
  <div class="tabs">
    <div
      v-if="known.length > 1"
      class="tabs__list"
      role="tablist"
      aria-label="Lab tabs"
      @keydown="onKeydown"
    >
      <button
        v-for="tab in known"
        :id="tabId(tab.name)"
        :key="tab.name"
        :ref="(el) => (buttons[tab.name] = el as HTMLButtonElement | null)"
        type="button"
        role="tab"
        class="tabs__tab"
        :class="{ 'tabs__tab--active': tab.name === active }"
        :aria-selected="tab.name === active"
        :aria-controls="panelId(tab.name)"
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
    </div>
    <div
      v-for="tab in known"
      v-show="tab.name === active"
      :id="panelId(tab.name)"
      :key="tab.name"
      class="tabs__panel"
      :role="known.length > 1 ? 'tabpanel' : undefined"
      :aria-labelledby="known.length > 1 ? tabId(tab.name) : undefined"
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
    <p v-if="known.length === 0" class="view__empty muted" role="status">This lab has nothing to show here.</p>
  </div>
</template>
