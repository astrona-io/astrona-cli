// Whether this page can still reach the workspace server. Two signals feed
// it: the terminal's WebSocket (reconnecting, with its attempt number) and
// the page's own status polling (consecutive failures). The workspace shows
// its "connection lost" dialog while either is down.

import { computed, reactive, readonly, ref } from 'vue'

const state = reactive({
  /** Reconnect attempt of the terminal, or 0 while it is not reconnecting. */
  terminalAttempt: 0,
  /** Consecutive status polls that could not reach the server. */
  apiFailures: 0,
})

const back = ref(0)

/**
 * Counts the times the server answered again after being lost. A terminal
 * waiting out a backoff watches it to reconnect at once.
 */
export const serverBack = readonly(back)

export const connectionLost = computed(() => state.terminalAttempt > 0 || state.apiFailures > 0)

/** The attempt number to show: the higher of the two counts. */
export const reconnectAttempt = computed(() => Math.max(state.terminalAttempt, state.apiFailures))

export function terminalReconnecting(attempt: number): void {
  state.terminalAttempt = attempt
}

export function terminalSettled(): void {
  state.terminalAttempt = 0
}

export function apiUnreachable(): void {
  state.apiFailures += 1
}

export function apiReachable(): void {
  if (connectionLost.value) back.value += 1
  state.apiFailures = 0
}

export function resetConnectivity(): void {
  state.terminalAttempt = 0
  state.apiFailures = 0
}
