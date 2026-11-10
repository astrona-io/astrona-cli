// Whether this page can still reach the workspace server. Two signals feed
// it: the terminals' WebSockets (reconnecting, with their attempt numbers;
// one per terminal tab that has been opened) and the page's own status
// polling (consecutive failures). The workspace shows its "connection lost"
// dialog while any of them is down.

import { computed, reactive, readonly, ref } from 'vue'

const state = reactive({
  /** Reconnect attempt per terminal tab that is reconnecting. */
  terminals: {} as Record<string, number>,
  /** Consecutive status polls that could not reach the server. */
  apiFailures: 0,
})

const back = ref(0)

/**
 * Counts the times the server answered again after being lost. A terminal
 * waiting out a backoff watches it to reconnect at once.
 */
export const serverBack = readonly(back)

const terminalAttempt = computed(() => Math.max(0, ...Object.values(state.terminals)))

export const connectionLost = computed(() => terminalAttempt.value > 0 || state.apiFailures > 0)

/** The attempt number to show: the highest of the counts. */
export const reconnectAttempt = computed(() => Math.max(terminalAttempt.value, state.apiFailures))

/** The terminal shown in tab `tab` is reconnecting. */
export function terminalReconnecting(tab: string, attempt: number): void {
  state.terminals[tab] = attempt
}

/** The terminal in tab `tab` is connected, ended or gone: not reconnecting. */
export function terminalSettled(tab: string): void {
  delete state.terminals[tab]
}

export function apiUnreachable(): void {
  state.apiFailures += 1
}

export function apiReachable(): void {
  if (connectionLost.value) back.value += 1
  state.apiFailures = 0
}

export function resetConnectivity(): void {
  for (const tab of Object.keys(state.terminals)) delete state.terminals[tab]
  state.apiFailures = 0
}
