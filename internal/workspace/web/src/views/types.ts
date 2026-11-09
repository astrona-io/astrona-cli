// The right panel shows one "view" of a target. M1 has the terminal view; a
// desktop (noVNC) view slots in later by adding a component here and the
// server listing "desktop" in the target's views. Nothing else changes.

import type { Component } from 'vue'
import type { ViewKind } from '../api'

/** What a view reports to the top bar. */
export interface ViewStatus {
  tone: 'ok' | 'busy' | 'warn' | 'error'
  label: string
}

/**
 * Props every view component accepts. A view emits "status" with a
 * ViewStatus whenever its connection changes, emits "leave" when the user
 * asks to move focus out of it, "unauthorized" when the session token is
 * refused, "runGone" when the server says the run changed or stopped, and
 * exposes focus().
 */
export interface ViewProps {
  lab: string
  runId: string
  target: string
}

export interface ViewHandle {
  focus(): void
}

export interface ViewDefinition {
  kind: ViewKind
  label: string
  component: () => Promise<Component>
}

/** Views this build can show, in order of preference. */
export const views: ViewDefinition[] = [
  {
    kind: 'terminal',
    label: 'Terminal',
    component: () => import('./TerminalView.vue').then((m) => m.default),
  },
]

export function pickView(offered: ViewKind[]): ViewDefinition | null {
  return views.find((v) => offered.includes(v.kind)) ?? null
}
