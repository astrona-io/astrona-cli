<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import type { EndCopy } from '../endSession'

// A modal confirmation on the native <dialog>: showModal() makes the rest of
// the page inert (the focus stays inside) and Esc cancels. Cancel has the
// focus first; the confirm button names the action.
const props = defineProps<{ copy: EndCopy; busy: boolean; error: string | null; danger?: boolean }>()
const emit = defineEmits<{ confirm: []; cancel: [] }>()

const dialog = ref<HTMLDialogElement>()
const cancelButton = ref<HTMLButtonElement>()
const errorBox = ref<HTMLElement>()
let opener: Element | null = null

onMounted(() => {
  opener = document.activeElement
  dialog.value?.showModal()
  cancelButton.value?.focus()
})

watch(
  () => props.error,
  async (e) => {
    if (!e) return
    await nextTick()
    errorBox.value?.focus()
  },
)

function onCancel(ev: Event): void {
  // Esc: the dialog closes itself unless prevented. While it runs, it stays.
  ev.preventDefault()
  if (!props.busy) close()
}

function close(): void {
  dialog.value?.close()
  if (opener instanceof HTMLElement) opener.focus()
  emit('cancel')
}
</script>

<template>
  <dialog
    ref="dialog"
    class="dialog"
    :class="{ 'dialog--danger': danger }"
    aria-labelledby="dialog-title"
    aria-describedby="dialog-body"
    :aria-busy="busy"
    @cancel="onCancel"
  >
    <div class="dialog__card">
      <p class="eyebrow eyebrow--page">Astrona workspace</p>
      <h2 id="dialog-title" class="dialog__title">{{ copy.title }}</h2>
      <div id="dialog-body" class="dialog__body">
        <p v-for="(p, i) in copy.paragraphs" :key="i">{{ p }}</p>
      </div>
      <p v-if="busy" class="dialog__busy" role="status"><span class="dot" aria-hidden="true" /> {{ copy.busy }}</p>
      <p v-if="error" ref="errorBox" class="dialog__error" role="alert" tabindex="-1">{{ error }}</p>
      <div class="dialog__actions">
        <button ref="cancelButton" type="button" class="btn btn--ghost" :disabled="busy" @click="close">
          {{ error ? 'Close' : 'Cancel' }}
        </button>
        <button type="button" class="btn btn--primary" :disabled="busy" @click="emit('confirm')">
          {{ error ? 'Try again' : copy.confirm }}
        </button>
      </div>
    </div>
  </dialog>
</template>
