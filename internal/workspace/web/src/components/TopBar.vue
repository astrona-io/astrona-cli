<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import type { Run } from '../api'
import { formatDuration, useCountdown, useElapsed } from '../clock'
import type { ViewStatus } from '../views/types'
import { endSessionCopy } from '../endSession'
import { LOW_TIME_MS } from '../lowTime'
import BrandMark from './BrandMark.vue'

// extending: POST /extend is on its way; extendError: why the last one failed.
const props = defineProps<{ run: Run; status: ViewStatus; extending?: boolean; extendError?: string | null }>()
const emit = defineEmits<{ end: []; extend: [] }>()

const kind = computed(() => (props.run.playground ? 'Playground' : props.run.mode === 'exam' ? 'Exam' : 'Lab'))
// Only when the server says so: an older server never sends canExtend.
const canExtend = computed(() => props.run.canExtend === true)

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
const lowTime = computed(() => left.value !== null && left.value <= LOW_TIME_MS)
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
      <p class="crumbs">
        <span class="crumbs__kind">{{ kind }}</span>
        <span class="crumbs__sep" aria-hidden="true">/</span>
        <span class="crumbs__lab mono">{{ run.lab }}</span>
      </p>
    </div>
    <div class="topbar__meta">
      <div class="seg">
        <p class="seg__item conn" :class="`conn--${status.tone}`">
          <span class="dot" aria-hidden="true" />
          <span>{{ status.label }}</span>
        </p>
        <p v-if="left !== null" class="seg__item timer" :class="{ 'timer--low': lowTime }">
          <span class="timer__label">Time left</span>
          <span class="timer__value mono" aria-hidden="true">{{ formatDuration(left, { hours: true }) }}</span>
          <span class="visually-hidden">{{ Math.ceil(left / 60_000) }} minutes</span>
        </p>
        <p v-else-if="elapsed !== null" class="seg__item timer timer--elapsed" title="Time since you clicked Start; learning labs have no time limit">
          <span class="timer__label">Time spent</span>
          <span class="timer__value mono" aria-hidden="true">{{ formatDuration(elapsed, { hours: true }) }}</span>
          <span class="visually-hidden">{{ Math.floor(elapsed / 60_000) }} minutes</span>
        </p>
        <button
          v-if="canExtend"
          type="button"
          class="seg__item seg__button"
          :disabled="extending"
          :aria-describedby="extendError ? 'extend-error' : undefined"
          :title="run.extendMinutes ? `Add ${run.extendMinutes} minutes` : 'Add time'"
          @click="emit('extend')"
        >
          {{ extending ? 'Extending…' : '+ Extend' }}
        </button>
      </div>
      <p v-if="extendError" id="extend-error" class="topbar__blocked small error" role="alert">
        Could not extend: {{ extendError }}
      </p>
      <div v-if="showEnd" class="topbar__end">
        <p v-if="endBlocked" id="end-blocked" class="topbar__blocked small muted">{{ endBlocked }}</p>
        <button
          type="button"
          class="btn btn--sm"
          :class="run.playground ? 'btn--danger' : 'btn--primary'"
          :disabled="!!endBlocked"
          :aria-describedby="endBlocked ? 'end-blocked' : undefined"
          aria-haspopup="dialog"
          @click="emit('end')"
        >
          <span v-if="run.playground" class="btn__stop" aria-hidden="true" />
          {{ endLabel }}
        </button>
      </div>
    </div>
    <div class="visually-hidden" aria-live="polite">{{ politeAnnouncement }}</div>
    <div class="visually-hidden" aria-live="assertive">{{ assertiveAnnouncement }}</div>
  </header>
</template>
