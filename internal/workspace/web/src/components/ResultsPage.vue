<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { Run, Submission } from '../api'
import { runAgainCommand } from '../endSession'

// The graded result of a submission and what happened to it. Shows only
// what the server returns: names, pass/fail, points and allowed hints —
// never a check's command, output or expected values.
const props = defineProps<{ run: Run | null; submission: Submission; lab: string }>()
const emit = defineEmits<{ back: []; remove: [] }>()

const heading = ref<HTMLElement>()
onMounted(() => heading.value?.focus())

const r = computed(() => props.submission.result)
const passedCount = computed(() => r.value.checks.filter((c) => c.pass).length)
const environmentRunning = computed(() => !props.submission.environmentRemoved && props.run?.status !== 'stopped')
const canRemove = computed(() => environmentRunning.value && !!props.run && !props.run.submit.keepEnvironment)

const savedLine = computed(() =>
  props.submission.recorded
    ? `Saved on this machine as attempt ${r.value.attempt}.`
    : 'This attempt could not be saved in this machine’s history (see the workspace server log).',
)
const sentLine = computed(() => {
  if (props.submission.sent) return 'Sent to your Astrona lab page. This attempt is finished.'
  if (props.submission.finished) return 'Not sent: this run is local only. This attempt is finished.'
  return 'Not sent to your Astrona account.'
})
const environmentLine = computed(() => {
  if (props.submission.environmentRemoved) return 'The environment was removed.'
  if (props.submission.cleanupError) return 'The environment could not be removed.'
  if (props.run?.status === 'stopped') return 'The environment is no longer running.'
  if (props.run?.submit.keepEnvironment)
    return `The environment keeps running (teardown.keepCluster). Remove it with astrona destroy ${props.lab}.`
  return 'The environment is still running: you can keep working and submit again, or remove it.'
})
const time = computed(() => {
  const t = new Date(r.value.time)
  return Number.isNaN(t.getTime()) ? '' : t.toLocaleString()
})
</script>

<template>
  <main class="center results-page">
    <section class="panel results" aria-labelledby="results-title">
      <p class="eyebrow eyebrow--page">Result · {{ run?.title || lab }}</p>
      <h1 id="results-title" ref="heading" class="results__title" tabindex="-1">
        <span class="verdict" :class="r.pass ? 'verdict--pass' : 'verdict--fail'">{{ r.pass ? 'PASS' : 'FAIL' }}</span>
        <span>{{ r.earned }} / {{ r.max }} points</span>
      </h1>
      <dl class="results__facts">
        <div class="stat">
          <dt class="stat__label">Score</dt>
          <dd class="stat__value">{{ Math.round(r.percent) }}%</dd>
        </div>
        <div v-if="r.passPercent" class="stat">
          <dt class="stat__label">Needed to pass</dt>
          <dd class="stat__value">{{ r.passPercent }}%</dd>
        </div>
        <div class="stat">
          <dt class="stat__label">Checks passed</dt>
          <dd class="stat__value">{{ passedCount }} / {{ r.checks.length }}</dd>
        </div>
        <div class="stat">
          <dt class="stat__label">Attempt</dt>
          <dd class="stat__value">{{ r.attempt }}</dd>
        </div>
      </dl>
      <p v-if="r.overTime" class="results__warn" role="note">
        Submitted after the time limit: in this strict exam it does not count as a pass.
      </p>

      <h2 class="results__sub">Checks</h2>
      <ul class="checks">
        <li v-for="(c, i) in r.checks" :key="i" class="check" :class="c.pass ? 'check--pass' : 'check--fail'">
          <span class="check__mark" aria-hidden="true">{{ c.pass ? '✓' : '✗' }}</span>
          <span class="check__name">
            <span class="visually-hidden">{{ c.pass ? 'Passed:' : 'Failed:' }}</span>
            {{ c.name }}
            <span v-if="c.hint" class="check__hint small muted">Hint: {{ c.hint }}</span>
          </span>
          <span class="check__points">{{ c.pass ? c.points : 0 }} / {{ c.points }}</span>
        </li>
      </ul>

      <h2 class="results__sub">What happened</h2>
      <ul class="results__status">
        <li>{{ savedLine }}<template v-if="time"> ({{ time }})</template></li>
        <li>{{ sentLine }}</li>
        <li>{{ environmentLine }}</li>
      </ul>
      <p v-if="submission.cleanupError" class="error" role="alert">
        Removing the environment failed: {{ submission.cleanupError }}
      </p>
      <details v-if="submission.messages.length" class="results__log">
        <summary>Messages from astrona ({{ submission.messages.length }})</summary>
        <ul>
          <li v-for="(m, i) in submission.messages" :key="i"><code>{{ m }}</code></li>
        </ul>
      </details>

      <h2 class="results__sub">Next</h2>
      <div v-if="environmentRunning" class="results__actions">
        <button v-if="environmentRunning" type="button" class="btn btn--primary" @click="emit('back')">
          Back to the workspace
        </button>
        <button v-if="canRemove" type="button" class="btn btn--ghost" @click="emit('remove')">Remove environment…</button>
      </div>
      <p class="small muted">To start over with a fresh environment, run this in your terminal:</p>
      <pre class="message__command"><code>{{ runAgainCommand(run) }}</code></pre>
      <p class="small muted">You can close this tab when you are done.</p>
    </section>
  </main>
</template>
