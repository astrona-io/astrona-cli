<script setup lang="ts">
// "Relevant resources" above the task: the official documentation pages the
// lab's astrona.yaml lists for it, as small badges. A name reads "Product:
// page" — the product is a short label inside the badge, the host is in its
// tooltip. A badge opens its page as a tab of the right panel (or shows that
// tab when it is already open), not as a browser tab. Nothing shows when the
// lab lists none, and the server sends none in an exam.
import { computed } from 'vue'
import type { DocLink } from '../api'
import { shownDocLinks, type ShownDocLink } from '../docLinks'

const props = defineProps<{ links: DocLink[] }>()
const emit = defineEmits<{ open: [ShownDocLink] }>()

const shown = computed(() => shownDocLinks(props.links))
</script>

<template>
  <section v-if="shown.length" class="doclinks" aria-labelledby="doclinks-heading">
    <p id="doclinks-heading" class="doclinks__heading">Relevant resources</p>
    <ul class="doclinks__list">
      <li v-for="link in shown" :key="link.index">
        <button
          type="button"
          class="doclinks__badge"
          :title="`${link.name} — ${link.host}. Opens as a tab.`"
          @click="emit('open', link)"
        >
          <span v-if="link.product" class="doclinks__product">{{ link.product }}</span>
          <span class="doclinks__title">{{ link.title }}</span>
          <span class="visually-hidden">({{ link.host }}, opens as a tab next to the terminal)</span>
        </button>
      </li>
    </ul>
  </section>
</template>
