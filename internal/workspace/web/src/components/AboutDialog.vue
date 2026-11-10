<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getServer } from '../api'

// About the workspace: the CLI version, the licence, where Astrona lives and
// the open-source work the page is built on. A native <dialog>, like
// ConfirmDialog: showModal() keeps the focus inside and Esc closes it.
const emit = defineEmits<{ close: [] }>()

const dialog = ref<HTMLDialogElement>()
const closeButton = ref<HTMLButtonElement>()
const version = ref<string | null>(null)
let opener: Element | null = null

const thirdParty = [
  { name: 'Vue', license: 'MIT', url: 'https://github.com/vuejs/core' },
  { name: 'xterm.js', license: 'MIT', url: 'https://github.com/xtermjs/xterm.js' },
  { name: 'Space Grotesk', license: 'SIL Open Font License 1.1', url: 'https://github.com/floriankarsten/space-grotesk' },
  { name: 'JetBrains Mono', license: 'SIL Open Font License 1.1', url: 'https://github.com/JetBrains/JetBrainsMono' },
]

onMounted(async () => {
  opener = document.activeElement
  dialog.value?.showModal()
  closeButton.value?.focus()
  try {
    version.value = (await getServer()).version || null
  } catch {
    version.value = null
  }
})

function close(): void {
  dialog.value?.close()
  if (opener instanceof HTMLElement) opener.focus()
  emit('close')
}

function onCancel(ev: Event): void {
  ev.preventDefault()
  close()
}
</script>

<template>
  <dialog ref="dialog" class="dialog" aria-labelledby="about-title" @cancel="onCancel">
    <div class="dialog__card">
      <p class="eyebrow eyebrow--page">Astrona workspace</p>
      <h2 id="about-title" class="dialog__title">About</h2>
      <div class="dialog__body about">
        <p>
          The browser workspace of the Astrona CLI<template v-if="version">, version <code>{{ version }}</code></template>.
          It runs on your machine and talks only to the <code>astrona</code> command that started it.
        </p>
        <ul class="about__links">
          <li><a href="https://astrona.io" target="_blank" rel="noopener noreferrer">astrona.io</a></li>
          <li><a href="https://cli.astrona.io" target="_blank" rel="noopener noreferrer">Documentation</a></li>
          <li><a href="https://github.com/astrona-io/astrona-cli" target="_blank" rel="noopener noreferrer">Source code</a></li>
        </ul>
        <p>
          © Astrona.io. Licensed under the
          <a href="https://www.apache.org/licenses/LICENSE-2.0" target="_blank" rel="noopener noreferrer">Apache License 2.0</a>.
        </p>
        <div>
          <h3 class="about__heading">Built with</h3>
          <ul class="about__credits">
            <li v-for="item in thirdParty" :key="item.name">
              <a :href="item.url" target="_blank" rel="noopener noreferrer">{{ item.name }}</a>
              <span class="muted"> · {{ item.license }}</span>
            </li>
          </ul>
        </div>
      </div>
      <div class="dialog__actions">
        <button ref="closeButton" type="button" class="btn btn--ghost" @click="close">Close</button>
      </div>
    </div>
  </dialog>
</template>
