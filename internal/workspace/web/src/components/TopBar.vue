<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import type { Run } from '../api'
import { formatDuration, useCountdown } from '../clock'
import type { ViewStatus } from '../views/types'

const props = defineProps<{ run: Run; status: ViewStatus }>()

// The timer shows only while a clock runs: the server sets deadlineAt for a
// started exam and for a playground session with a deadline, never otherwise.
const deadline = toRef(() => props.run.deadlineAt)
const left = useCountdown(deadline)
const lowTime = computed(() => left.value !== null && left.value <= 5 * 60_000)

// Screen readers hear the time at a few milestones only, never every second.
const politeAnnouncement = ref('')
const assertiveAnnouncement = ref('')
const milestones = [30, 15, 5, 1]
let announced = new Set<number>()
watch(deadline, () => {
  announced = new Set()
})
watch(left, (ms) => {
  if (ms === null) return
  if (ms === 0) {
    if (!announced.has(0)) {
      announced.add(0)
      assertiveAnnouncement.value = 'Time is up.'
    }
    return
  }
  const minutes = Math.ceil(ms / 60_000)
  for (const m of milestones) {
    // Only announce a milestone reached while the page is open.
    if (minutes === m && !announced.has(m)) {
      announced.add(m)
      politeAnnouncement.value = `${m} ${m === 1 ? 'minute' : 'minutes'} left.`
    }
    if (minutes < m) announced.add(m)
  }
})
</script>

<template>
  <header class="topbar">
    <div class="topbar__brand">
      <span class="topbar__mark" aria-hidden="true">A</span>
      <span class="eyebrow eyebrow--page topbar__kind">{{ run.playground ? 'Playground' : run.mode === 'exam' ? 'Exam' : 'Lab' }}</span>
      <h1 class="topbar__title">{{ run.title }}</h1>
    </div>
    <div class="topbar__meta">
      <p class="conn" :class="`conn--${status.tone}`">
        <span class="dot" aria-hidden="true" />
        <span>{{ status.label }}</span>
      </p>
      <p v-if="left !== null" class="timer" :class="{ 'timer--low': lowTime }">
        <span class="visually-hidden">Time left: </span>
        <span class="timer__value" aria-hidden="true">{{ formatDuration(left) }}</span>
        <span class="visually-hidden">{{ Math.ceil(left / 60_000) }} minutes</span>
      </p>
    </div>
    <div class="visually-hidden" aria-live="polite">{{ politeAnnouncement }}</div>
    <div class="visually-hidden" aria-live="assertive">{{ assertiveAnnouncement }}</div>
  </header>
</template>
