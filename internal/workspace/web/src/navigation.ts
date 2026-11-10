interface BrowserNavigation {
  location: Pick<Location, 'pathname' | 'search' | 'reload'>
  history: Pick<History, 'replaceState'>
}

// A run may have been created after this page loaded. Fetch a new document so
// its CSP includes the run's frame origins. A fresh code also works when the
// current token lives only in memory; the bearer token never enters the URL.
export async function enterRun(
  lab: string,
  mintCode: () => Promise<string>,
  browser: BrowserNavigation = window,
): Promise<void> {
  const code = await mintCode()
  const fragment = new URLSearchParams({ code, lab })
  const { location, history } = browser
  history.replaceState(null, '', `${location.pathname}${location.search}#${fragment}`)
  location.reload()
}
