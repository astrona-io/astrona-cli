// Copy one value (a lab name, a command) to the clipboard, with a short
// "Copied" state per key for the button that did it.

import { ref } from 'vue'

const COPIED_MS = 1500

export function useCopyText() {
  const copied = ref<string | null>(null)
  const failed = ref<string | null>(null)
  let timer: number | undefined

  async function copy(key: string, text: string): Promise<void> {
    window.clearTimeout(timer)
    try {
      await navigator.clipboard.writeText(text)
      copied.value = key
      failed.value = null
    } catch {
      copied.value = null
      failed.value = key
    }
    timer = window.setTimeout(() => {
      copied.value = null
      failed.value = null
    }, COPIED_MS)
  }

  return { copied, failed, copy }
}
