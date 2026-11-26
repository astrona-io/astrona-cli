// When the time left counts as low: the top bar's timer turns amber and the
// workspace shows the low-time banner.
export const LOW_TIME_MS = 10 * 60_000

const DISMISS_KEY = 'astrona.workspace.lowTimeDismissed'

/** The run whose low-time banner was dismissed in this browser, if any. */
export function lowTimeDismissed(runId: string): boolean {
  try {
    return window.localStorage.getItem(DISMISS_KEY) === runId
  } catch {
    return false
  }
}

export function dismissLowTime(runId: string): void {
  try {
    window.localStorage.setItem(DISMISS_KEY, runId)
  } catch {
    // storage unavailable: the banner stays away until reload
  }
}
