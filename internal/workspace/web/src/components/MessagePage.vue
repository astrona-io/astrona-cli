<script setup lang="ts">
import BrandMark from './BrandMark.vue'

defineProps<{ title: string; body: string; command?: string; home?: boolean }>()
defineEmits<{ home: [] }>()
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
      <p v-if="home" class="message__actions">
        <button type="button" class="btn btn--sm btn--ghost" @click="$emit('home')">See all labs on this computer</button>
      </p>
    </section>
  </main>
</template>
