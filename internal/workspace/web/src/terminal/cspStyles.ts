// xterm.js writes its scrollbar rules (and, with the DOM renderer, its theme
// and cell size) into <style> elements it creates at runtime. The workspace's
// Content Security Policy (style-src 'self') refuses inline <style> content:
// those rules would never apply, and each element would log a CSP violation.
//
// While a terminal is mounted, document.createElement('style') therefore
// returns an inert, hidden <astrona-style> element instead. xterm.js only sets
// its textContent and appends it, so it notices no difference. This mirror
// copies the text of every such element inside the terminal's container into
// a constructed stylesheet (CSSStyleSheet.replaceSync) adopted by the
// document. Constructed stylesheets are CSSOM, which CSP does not restrict;
// no policy is loosened and nothing inline is ever handed to the browser.
// The page itself creates no <style> elements at runtime (its CSS is a file),
// so the substitution only ever affects xterm.js.
//
// Not covered: the DOM renderer sets a style attribute on cells that use
// 24-bit ("truecolor") colours or a minimum-contrast adjustment. CSP refuses
// those. The terminal therefore draws with the WebGL renderer (a canvas, no
// style attributes; see TerminalView.vue); only when WebGL is unavailable
// does it fall back to the DOM renderer, where such cells show the default
// colours. 16 and 256-colour output is unaffected either way.

const INERT_TAG = 'astrona-style'

let shimUsers = 0
let originalCreateElement: Document['createElement'] | undefined

function installStyleShim(): void {
  if (shimUsers++ > 0) return
  const original = document.createElement
  originalCreateElement = original
  document.createElement = function (this: Document, tagName: string, options?: ElementCreationOptions) {
    if (tagName.toLowerCase() === 'style') {
      const el = original.call(this, INERT_TAG)
      el.hidden = true
      return el
    }
    return original.call(this, tagName, options)
  } as Document['createElement']
}

function removeStyleShim(): void {
  if (--shimUsers > 0 || !originalCreateElement) return
  // The shim is an own property; deleting it uses Document.prototype again.
  Reflect.deleteProperty(document, 'createElement')
  originalCreateElement = undefined
}

export function mirrorInlineStyles(container: HTMLElement): () => void {
  installStyleShim()
  const sheets = new Map<Element, CSSStyleSheet>()

  const adopt = (sheet: CSSStyleSheet) => {
    if (!document.adoptedStyleSheets.includes(sheet)) {
      document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]
    }
  }
  const drop = (sheet: CSSStyleSheet) => {
    document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== sheet)
  }

  const sync = () => {
    // `style` too, should one be created another way (it is refused by CSP).
    const present = new Set(container.querySelectorAll(`${INERT_TAG}, style`))
    for (const el of present) {
      let sheet = sheets.get(el)
      if (!sheet) {
        sheet = new CSSStyleSheet()
        sheets.set(el, sheet)
        adopt(sheet)
      }
      try {
        sheet.replaceSync(el.textContent ?? '')
      } catch {
        // Unparsable text: keep the previous rules.
      }
    }
    for (const [el, sheet] of sheets) {
      if (!present.has(el)) {
        drop(sheet)
        sheets.delete(el)
      }
    }
  }

  const observer = new MutationObserver(sync)
  observer.observe(container, { childList: true, subtree: true, characterData: true })
  sync()

  return () => {
    observer.disconnect()
    for (const sheet of sheets.values()) drop(sheet)
    sheets.clear()
    removeStyleShim()
  }
}
