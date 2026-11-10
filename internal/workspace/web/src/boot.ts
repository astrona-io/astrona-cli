// The first module the page evaluates (main.ts imports it before anything
// else). It runs synchronously, before any await: the launch code is read from
// the URL fragment and removed from the address bar and from history at once,
// so it never lingers where a screenshot, a bookmark or another script could
// pick it up.
//
// The fragment the CLI prints is "#code=<code>", optionally with "&lab=<id>"
// or "&launch=<id>" (an `astrona run` whose lab is not ready yet). After this
// module the address bar shows "#/runs/<lab>", "#/launches/<id>" or nothing.

export interface Launch {
  /** Single-use launch code, or null when the page was not opened from the CLI. */
  code: string | null
  /** Lab to open, from the launch URL or the in-page route. */
  lab: string | null
  /** An `astrona run` still starting: terms, then setup, then its lab. */
  launchId: string | null
}

function readLaunch(): Launch {
  const hash = window.location.hash.replace(/^#/, '')
  if (hash.startsWith('/runs/')) {
    return { code: null, lab: decodeURIComponent(hash.slice('/runs/'.length)) || null, launchId: null }
  }
  if (hash.startsWith('/launches/')) {
    return { code: null, lab: null, launchId: decodeURIComponent(hash.slice('/launches/'.length)) || null }
  }
  const params = new URLSearchParams(hash)
  const code = params.get('code')
  const lab = params.get('lab')
  const launchId = params.get('launch')
  if (code !== null || lab !== null || launchId !== null) {
    let route = ''
    if (lab) route = `#/runs/${encodeURIComponent(lab)}`
    else if (launchId) route = `#/launches/${encodeURIComponent(launchId)}`
    window.history.replaceState(null, '', window.location.pathname + window.location.search + route)
  }
  return { code: code || null, lab: lab || null, launchId: launchId || null }
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
