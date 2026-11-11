// Click to copy for code in a rendered document (the task, the docs tabs).
// Every inline `code` becomes a chip with a copy icon that copies its text;
// every code block gets a Copy button. The document arrives from the server
// as HTML and is inserted with v-html, so these are added to its DOM after
// each render, and the article's own click and keydown handlers (the
// returned ones) do the copying. Nothing from the document is evaluated.

import { ref, watch, type Ref, type WatchSource } from 'vue'

/** How long "Copied" shows. */
const COPIED_MS = 1500

export interface CopyCode {
  onClick(e: MouseEvent): void
  onKeydown(e: KeyboardEvent): void
  /** For an aria-live region: what the last copy did. */
  announcement: Ref<string>
}

/**
 * Decorates the code in root whenever root or html changes, and returns the
 * handlers to bind on root.
 */
export function useCopyCode(root: Ref<HTMLElement | undefined>, html: WatchSource<unknown>): CopyCode {
  const announcement = ref('')
  watch([root, html], () => decorate(root.value), { flush: 'post', immediate: true })

  async function copy(el: HTMLElement): Promise<void> {
    const text = el.classList.contains('code-copy') ? (el.parentElement?.querySelector('pre')?.textContent ?? '') : (el.textContent ?? '')
    let ok = true
    try {
      await navigator.clipboard.writeText(text.replace(/\n$/, ''))
    } catch {
      ok = false
    }
    announcement.value = ok ? 'Copied to the clipboard.' : 'Could not copy: select the text and copy it instead.'
    const state = ok ? 'is-copied' : 'is-failed'
    if (el.classList.contains('code-copy')) el.textContent = ok ? 'Copied' : 'Not copied'
    el.classList.add(state)
    window.setTimeout(() => {
      el.classList.remove(state)
      if (el.classList.contains('code-copy')) el.textContent = 'Copy'
    }, COPIED_MS)
  }

  function target(e: Event): HTMLElement | null {
    return e.target instanceof Element ? e.target.closest<HTMLElement>('.copyable, .code-copy') : null
  }

  return {
    announcement,
    onClick(e) {
      const el = target(e)
      // Dragging over code to select part of it is not a click to copy.
      if (!el || (el.classList.contains('copyable') && window.getSelection()?.toString())) return
      void copy(el)
    },
    onKeydown(e) {
      const el = target(e)
      if (!el?.classList.contains('copyable') || (e.key !== 'Enter' && e.key !== ' ')) return
      e.preventDefault()
      void copy(el)
    },
  }
}

function decorate(root: HTMLElement | undefined): void {
  if (!root) return
  for (const code of root.querySelectorAll<HTMLElement>('code')) {
    // Code inside a link stays a link.
    if (code.closest('pre, a') || code.classList.contains('copyable')) continue
    code.classList.add('copyable')
    code.tabIndex = 0
    code.setAttribute('role', 'button')
    code.title = 'Copy'
  }
  for (const pre of root.querySelectorAll('pre')) {
    if (pre.parentElement?.classList.contains('code-block')) continue
    // The button sits outside the <pre>, so it neither scrolls with a long
    // line nor ends up in the copied text.
    const block = document.createElement('div')
    block.className = 'code-block'
    pre.replaceWith(block)
    block.append(pre)
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'code-copy'
    button.textContent = 'Copy'
    button.setAttribute('aria-label', 'Copy this code')
    block.append(button)
  }
}
