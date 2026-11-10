<script setup lang="ts">
// The Astrona mark, as the astrona.io site header shows it: the logo in a
// round panel with an orange glow. assets/astrona-logo.svg is the site's
// public/astrona-dark-logo.svg, unchanged. Vite emits it as a file of its
// own (never a data: URI), which img-src 'self' allows.
import logo from '../assets/astrona-logo.svg'

withDefaults(defineProps<{ size?: 'sm' | 'md' }>(), { size: 'md' })
</script>

<template>
  <span class="brand-mark" :class="`brand-mark--${size}`" aria-hidden="true">
    <img :src="logo" alt="" class="brand-mark__logo" />
  </span>
</template>
