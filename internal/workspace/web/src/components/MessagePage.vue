<script setup lang="ts">
defineProps<{ title: string; body: string; command?: string }>()
</script>

<template>
  <main class="center">
    <section class="panel message" aria-labelledby="message-title">
      <p class="eyebrow eyebrow--page">Astrona workspace</p>
      <h1 id="message-title" class="message__title">{{ title }}</h1>
      <p class="muted">{{ body }}</p>
      <pre v-if="command" class="message__command"><code>{{ command }}</code></pre>
    </section>
  </main>
</template>
