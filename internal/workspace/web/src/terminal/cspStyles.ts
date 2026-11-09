// xterm.js's DOM renderer writes its theme, cell size and scrollbar rules
// into <style> elements it creates at runtime. The workspace's Content
// Security Policy (style-src 'self') refuses inline <style> content, so those
// rules would never apply and the terminal would render without colours or
// cell metrics.
//
// This mirror copies the text of every <style> element inside the terminal's
// container into a constructed stylesheet (CSSStyleSheet.replaceSync) adopted
// by the document. Constructed stylesheets are CSSOM, which CSP does not
// restrict, and no policy is loosened: the browser still refuses the inline
// elements themselves (each one logs a CSP report in the console).
//
// Not covered: xterm.js sets a style attribute on cells that use 24-bit
// ("truecolor") colours or a minimum-contrast adjustment. CSP refuses those
// too, so such cells fall back to the default colours. 16 and 256-colour
// output, which lab tools use, is unaffected.

export function mirrorInlineStyles(container: HTMLElement): () => void {
  const sheets = new Map<HTMLStyleElement, CSSStyleSheet>()

  const adopt = (sheet: CSSStyleSheet) => {
    if (!document.adoptedStyleSheets.includes(sheet)) {
      document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet]
    }
  }
  const drop = (sheet: CSSStyleSheet) => {
    document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== sheet)
  }

  const sync = () => {
    const present = new Set(container.querySelectorAll('style'))
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
  }
}
