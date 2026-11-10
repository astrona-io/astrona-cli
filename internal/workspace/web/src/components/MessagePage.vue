<script setup lang="ts">
import BrandMark from './BrandMark.vue'

defineProps<{ title: string; body: string; command?: string }>()
</script>

<template>
  <main class="center">
    <section class="panel message" aria-labelledby="message-title">
      <div class="brand">
        <BrandMark />
        <p class="eyebrow eyebrow--page">Astrona workspace</p>
      </div>
      <h1 id="message-title" class="message__title">{{ title }}</h1>
      <p class="muted">{{ body }}</p>
      <pre v-if="command" class="message__command"><code>{{ command }}</code></pre>
    </section>
  </main>
</template>
