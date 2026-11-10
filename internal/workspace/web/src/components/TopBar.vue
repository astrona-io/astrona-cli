<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import type { Run } from '../api'
import { formatDuration, useCountdown, useElapsed } from '../clock'
import type { ViewStatus } from '../views/types'
import { endSessionCopy } from '../endSession'
import BrandMark from './BrandMark.vue'

const props = defineProps<{ run: Run; status: ViewStatus }>()
const emit = defineEmits<{ end: [] }>()

// Submit for a lab or exam, End for a playground. A playground can always be
// ended while it runs; a lab only when the server says it can be graded now,
// and the reason shows when it cannot.
const endLabel = computed(() => endSessionCopy(props.run, false).button)
const endBlocked = computed(() => {
  if (props.run.submit.grading) return 'Grading this lab…'
  if (props.run.playground) return ''
  return props.run.canSubmit ? '' : props.run.submit.blocked || 'Submit is not available right now.'
})
const showEnd = computed(() => props.run.status !== 'stopped' && props.run.status !== 'failed')

// The timer shows only while a clock runs: the server sets deadlineAt for a
// started exam and for a playground session with a deadline. A started lab
// without one shows the time since Start instead.
const deadline = toRef(() => props.run.deadlineAt)
const left = useCountdown(deadline)
const lowTime = computed(() => left.value !== null && left.value <= 5 * 60_000)
// A started lab without a deadline (learning mode) counts up from Start instead.
const startedAt = toRef(() =>
  !props.run.playground && !props.run.deadlineAt && props.run.status === 'running' ? props.run.startedAt : null,
)
const elapsed = useElapsed(startedAt)

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
      <BrandMark size="sm" />
      <span class="eyebrow eyebrow--page topbar__kind">{{ run.playground ? 'Playground' : run.mode === 'exam' ? 'Exam' : 'Lab' }}</span>
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
      <p v-else-if="elapsed !== null" class="timer timer--elapsed" title="Time since you clicked Start; learning labs have no time limit">
        <span class="visually-hidden">Time spent: </span>
        <span class="timer__value" aria-hidden="true">{{ formatDuration(elapsed) }}</span>
        <span class="visually-hidden">{{ Math.floor(elapsed / 60_000) }} minutes</span>
      </p>
      <div v-if="showEnd" class="topbar__end">
        <p v-if="endBlocked" id="end-blocked" class="topbar__blocked small muted">{{ endBlocked }}</p>
        <button
          type="button"
          class="btn btn--sm"
          :class="run.playground ? 'btn--ghost' : 'btn--primary'"
          :disabled="!!endBlocked"
          :aria-describedby="endBlocked ? 'end-blocked' : undefined"
          aria-haspopup="dialog"
          @click="emit('end')"
        >
          {{ endLabel }}
        </button>
      </div>
    </div>
    <div class="visually-hidden" aria-live="polite">{{ politeAnnouncement }}</div>
    <div class="visually-hidden" aria-live="assertive">{{ assertiveAnnouncement }}</div>
  </header>
</template>
