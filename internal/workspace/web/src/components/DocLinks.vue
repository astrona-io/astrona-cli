<script setup lang="ts">
// "Relevant resources" under the task: the official documentation pages the
// lab's astrona.yaml lists for it. A name reads "Product: page" — the product
// is a small label above the page's title, the URL's host sits below. Each
// link opens in a new tab, without an opener or a referrer. Nothing shows
// when the lab lists none, and the server sends none in an exam.
import { computed } from 'vue'
import type { DocLink } from '../api'

const props = defineProps<{ links: DocLink[] }>()

interface ShownLink {
  url: string
  product: string
  title: string
  host: string
}

const shown = computed<ShownLink[]>(() =>
  props.links.flatMap((link) => {
    let url: URL
    try {
      url = new URL(link.url)
    } catch {
      return []
    }
    if (url.protocol !== 'https:') return []
    const at = link.name.indexOf(': ')
    const product = at > 0 ? link.name.slice(0, at).trim() : ''
    const title = (at > 0 ? link.name.slice(at + 2).trim() : '') || link.name
    return [{ url: url.href, product, title, host: url.hostname.replace(/^www\./, '') }]
  }),
)
</script>

<template>
  <section v-if="shown.length" class="doclinks" aria-labelledby="doclinks-heading">
    <p id="doclinks-heading" class="eyebrow doclinks__heading">Relevant resources</p>
    <ul class="doclinks__list">
      <li v-for="link in shown" :key="link.url + link.title">
        <a class="doclinks__link" :href="link.url" target="_blank" rel="noopener noreferrer">
          <span v-if="link.product" class="doclinks__product">{{ link.product }}</span>
          <span class="doclinks__title">
            {{ link.title }}<span class="doclinks__out" aria-hidden="true">↗</span>
          </span>
          <span class="doclinks__host">{{ link.host }}</span>
          <span class="visually-hidden">(opens in a new tab)</span>
        </a>
      </li>
    </ul>
  </section>
</template>
