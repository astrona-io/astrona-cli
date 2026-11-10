// The first module the page evaluates (main.ts imports it before anything
// else). It runs synchronously, before any await: the launch code is read from
// the URL fragment and removed from the address bar and from history at once,
// so it never lingers where a screenshot, a bookmark or another script could
// pick it up.
//
// The fragment the CLI prints is "#code=<code>", optionally with "&lab=<id>".
// After this module the address bar shows "#/runs/<lab>" or nothing.

export interface Launch {
  /** Single-use launch code, or null when the page was not opened from the CLI. */
  code: string | null
  /** Lab to open, from the launch URL or the in-page route. */
  lab: string | null
}

function readLaunch(): Launch {
  const hash = window.location.hash.replace(/^#/, '')
  if (hash.startsWith('/runs/')) {
    return { code: null, lab: decodeURIComponent(hash.slice('/runs/'.length)) || null }
  }
  const params = new URLSearchParams(hash)
  const code = params.get('code')
  const lab = params.get('lab')
  if (code !== null || lab !== null) {
    const clean = window.location.pathname + window.location.search + (lab ? `#/runs/${encodeURIComponent(lab)}` : '')
    window.history.replaceState(null, '', clean)
  }
  return { code: code || null, lab: lab || null }
}

export const launch: Launch = readLaunch()

// A fresh link opened in a tab that already shows the workspace (for example
// one showing "Workspace unavailable") only changes the fragment, which loads
// nothing. Load the page again so the new code is read above.
window.addEventListener('hashchange', () => {
  if (new URLSearchParams(window.location.hash.replace(/^#/, '')).has('code')) window.location.reload()
})

/** Records the open lab in the address bar so a reload returns to it. */
export function rememberLab(lab: string): void {
  const target = `#/runs/${encodeURIComponent(lab)}`
  if (window.location.hash !== target) {
    window.history.replaceState(null, '', window.location.pathname + window.location.search + target)
  }
}
