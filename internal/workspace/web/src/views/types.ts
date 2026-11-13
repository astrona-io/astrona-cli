// The right panel shows the open tabs (src/components/LabTabs.vue): the lab's
// own, and the ones the learner opened from the + menu. Each tab is shown by
// the view for its kind: a terminal, a page the lab serves, or
// documentation. A new kind (for example a desktop) is a new view component
// here and a new tab kind on the server; the workspace page does not change.

import type { Component } from 'vue'
import type { RunTab, TabKind } from '../api'

/** What a view reports to the top bar. */
export interface ViewStatus {
  tone: 'ok' | 'busy' | 'warn' | 'error'
  label: string
}

/**
 * Props every view component accepts. A view emits "status" with a
 * ViewStatus whenever its state changes, emits "leave" when the user asks
 * to move focus out of it, "unauthorized" when the session token is
 * refused, "runGone" when the server says the run changed or stopped, and
 * exposes focus(). A view stays mounted while another tab is shown. A view
 * that loses its connection reports it to connectivity.ts, which covers the
 * whole workspace with the "connection lost" dialog, whichever tab is shown.
 */
export interface ViewProps {
  lab: string
  runId: string
  tab: WorkspaceTab
}

/**
 * Where an open tab comes from: one the lab declared (lab), or one the
 * learner opened from the + menu: another terminal on a target (terminal),
 * the task, the prerequisites or the solution guide; or a documentation link
 * of the lab (resource), opened from its badge above the task.
 */
export type TabSource = 'lab' | 'terminal' | 'task' | 'prerequisites' | 'solution' | 'resource'

/** An open tab of the right panel. */
export interface WorkspaceTab extends RunTab {
  source: TabSource
  /** prerequisites: the run's rendered prerequisites. */
  html?: string
  /** task: the question it shows. */
  questionId?: string
  /** A lab tab that shows the solution: the learner already confirmed. */
  confirmed?: boolean
}

export interface ViewHandle {
  focus(): void
}

/** The view component for each tab kind. */
export const views: Record<TabKind, () => Promise<Component>> = {
  terminal: () => import('./TerminalView.vue').then((m) => m.default),
  browser: () => import('./FrameView.vue').then((m) => m.default),
  docs: () => import('./DocsView.vue').then((m) => m.default),
}
