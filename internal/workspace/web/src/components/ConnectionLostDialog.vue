<script setup lang="ts">
import { onMounted, ref } from 'vue'

// Covers the workspace while the page cannot reach the workspace server. It
// has nothing to press and cannot be dismissed: it closes by itself when the
// connection is back (WorkspacePage decides when it shows).
defineProps<{ lab: string; attempt: number }>()

const dialog = ref<HTMLElement>()

onMounted(() => dialog.value?.focus())

// Focus stays on the dialog: Tab has nowhere else to go (the workspace behind
// it is inert) and Escape does not close it.
function trap(ev: KeyboardEvent): void {
  if (ev.key === 'Tab' || ev.key === 'Escape') ev.preventDefault()
}
</script>

<template>
  <!-- A press on the backdrop would move focus to the page body. -->
  <div class="lost" @mousedown.self.prevent>
    <section
      ref="dialog"
      class="panel lost__dialog"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="lost-title"
      aria-describedby="lost-body"
      tabindex="-1"
      @keydown="trap"
    >
      <p class="eyebrow lost__eyebrow">Connection lost</p>
      <h2 id="lost-title" class="lost__title">The workspace server is not answering</h2>
      <p id="lost-body" class="muted">
        This page lost its connection to the workspace server on your computer, so typing is paused. It keeps
        trying; when the server answers, you continue where you left off, in the same shell.
      </p>
      <p class="conn conn--warn lost__attempt">
        <span class="dot" aria-hidden="true" />
        Reconnecting… attempt {{ attempt }}
      </p>
      <hr class="rule" />
      <p class="small">If it does not come back, check in your terminal that the server is running:</p>
      <pre class="message__command"><code>astrona ui status</code></pre>
      <p class="small">
        If it is not, open the lab again. astrona starts the server and prints a new link to use instead of this
        page; your lab environment is still there.
      </p>
      <pre class="message__command"><code>astrona ui {{ lab }}</code></pre>
    </section>
  </div>
</template>
