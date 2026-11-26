<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getServer } from '../api'
import BrandMark from './BrandMark.vue'

// About the workspace: the CLI version, the licence, where Astrona lives and
// the open-source work the page is built on. A native <dialog>, like
// ConfirmDialog: showModal() keeps the focus inside and Esc closes it.
// lab: the run the workspace shows, for the footer.
defineProps<{ lab?: string }>()
const emit = defineEmits<{ close: [] }>()

// Where Astrona lives, as the astrona.io footer links them.
const links = [
  { label: 'Website', sub: 'astrona.io', href: 'https://astrona.io' },
  { label: 'GitHub', sub: 'astrona-io', href: 'https://github.com/astrona-io' },
  { label: 'YouTube', sub: '@astrona-io', href: 'https://www.youtube.com/@astrona-io' },
  { label: 'Support us', sub: 'Open Collective', href: 'https://opencollective.com/astrona' },
]

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
  <dialog ref="dialog" class="dialog about" aria-labelledby="about-title" aria-describedby="about-mission" @cancel="onCancel">
    <div class="dialog__card">
      <div class="about__head">
        <BrandMark />
        <div>
          <h2 id="about-title" class="dialog__title">Astrona.io</h2>
          <p class="muted small about__sub">Hands-on Kubernetes labs and courses</p>
        </div>
      </div>
      <p id="about-mission" class="about__mission">
        Free, open source learning for the Cloud Native community, covering the CNCF and wider Linux Foundation
        ecosystem.
      </p>
      <p class="muted small">
        Astrona is free, open source training for Kubernetes and Cloud Native certifications. This workspace runs on
        your machine and talks only to the <code>astrona</code> command that started it.
      </p>
      <ul class="about__links">
        <li v-for="l in links" :key="l.href">
          <a class="about__link" :href="l.href" target="_blank" rel="noopener noreferrer">
            <span class="about__link-label">{{ l.label }} <span aria-hidden="true">↗</span></span>
            <span class="about__link-sub small muted">{{ l.sub }}</span>
          </a>
        </li>
      </ul>
      <div class="about__more">
        <p class="small">
          <a href="https://cli.astrona.io" target="_blank" rel="noopener noreferrer">Documentation</a> ·
          <a href="https://github.com/astrona-io/astrona-cli" target="_blank" rel="noopener noreferrer">Source code</a> ·
          © Astrona.io, licensed under the
          <a href="https://www.apache.org/licenses/LICENSE-2.0" target="_blank" rel="noopener noreferrer">Apache License 2.0</a>.
        </p>
        <p class="small muted">
          Built with
          <template v-for="(item, i) in thirdParty" :key="item.name">
            <a :href="item.url" target="_blank" rel="noopener noreferrer">{{ item.name }}</a>
            <span> ({{ item.license }})</span><template v-if="i < thirdParty.length - 1">, </template>
          </template>.
        </p>
      </div>
      <div class="about__foot">
        <p class="small muted mono">
          astrona-cli {{ version ?? '' }}<template v-if="lab"> · lab {{ lab }}</template>
        </p>
        <button ref="closeButton" type="button" class="btn btn--ghost btn--sm" @click="close">Close</button>
      </div>
    </div>
  </dialog>
</template>
