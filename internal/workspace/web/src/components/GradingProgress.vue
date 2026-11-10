<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import type { GradeCheck } from '../api'
import { serverNow } from '../clock'

// Each check while a submission is graded: waiting, running (with the time
// so far) or passed/failed with how long it took. Screen readers hear only
// how many checks are done, when that changes, never the ticking time.
const props = defineProps<{ checks: GradeCheck[] }>()

const now = ref(serverNow())
const timer = window.setInterval(() => {
  now.value = serverNow()
}, 1000)
onBeforeUnmount(() => window.clearInterval(timer))

const mark: Record<GradeCheck['state'], string> = { waiting: '·', running: '…', passed: '✓', failed: '✗' }

const done = computed(() => props.checks.filter((c) => c.state === 'passed' || c.state === 'failed').length)

function duration(s: number, precise: boolean): string {
  if (s < 60) return precise ? `${s.toFixed(s < 10 ? 2 : 1)}s` : `${Math.floor(s)}s`
  return `${Math.floor(s / 60)}m ${String(Math.floor(s % 60)).padStart(2, '0')}s`
}

function time(c: GradeCheck): string {
  if (c.seconds !== undefined) return duration(c.seconds, true)
  if (c.state !== 'running' || !c.startedAt) return ''
  const started = Date.parse(c.startedAt)
  return Number.isNaN(started) ? '' : duration(Math.max(0, now.value - started) / 1000, false)
}
</script>

<template>
  <div class="grading">
    <ol class="grading__checks" aria-label="Checks">
      <li v-for="(c, i) in checks" :key="i" class="grading__check" :class="`grading__check--${c.state}`">
        <span class="grading__mark" aria-hidden="true">{{ mark[c.state] }}</span>
        <span class="grading__name">{{ c.name }}<span class="visually-hidden">, {{ c.state }}</span></span>
        <span class="grading__time small">{{ time(c) }}</span>
      </li>
    </ol>
    <p class="visually-hidden" aria-live="polite">{{ done }} of {{ checks.length }} checks done.</p>
  </div>
</template>
