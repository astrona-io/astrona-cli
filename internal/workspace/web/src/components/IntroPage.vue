<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { Run } from '../api'
import BrandMark from './BrandMark.vue'

// Before Start there is no task text and no terminal: reading time is not
// free working time. The clock starts on the server when Start is clicked.

const props = defineProps<{ run: Run; starting: boolean; error: string | null }>()
defineEmits<{ start: [] }>()

const heading = ref<HTMLHeadingElement>()
onMounted(() => heading.value?.focus())

const ready = computed(() => props.run.status === 'ready')
const playground = computed(() => props.run.playground)
const points = computed(() => props.run.questions.reduce((sum, q) => sum + (q.points ?? 0), 0))
const facts = computed(() => {
  const list: { label: string; value: string }[] = []
  const seconds = props.run.timeLimitSeconds
  if (seconds) list.push({ label: 'Time allowed', value: `${Math.round(seconds / 60)} min` })
  else if (props.run.intro.estimatedDuration) list.push({ label: 'Takes about', value: props.run.intro.estimatedDuration })
  if (props.run.questions.length) {
    list.push({ label: props.run.questions.length === 1 ? 'Question' : 'Questions', value: String(props.run.questions.length) })
  }
  if (points.value) list.push({ label: 'Points', value: String(points.value) })
  list.push({ label: 'Mode', value: props.run.mode === 'exam' ? 'Exam' : 'Learning' })
  return list
})
const rules = computed(() => {
  const list: string[] = []
  if (props.run.mode === 'exam') list.push('The timer keeps running if you close this page or lose the connection.')
  if (props.run.rules.strict)
    list.push('A submission after the deadline cannot pass, and the terminal stops taking input when time is up.')
  if (props.run.rules.hintsHidden) list.push('Hints are not shown.')
  list.push('Nothing is submitted automatically, not even when time is up.')
  list.push(
    `When you are done, use ${props.run.mode === 'exam' ? 'Submit exam' : 'Submit lab'} at the top of the workspace (or astrona submit in a terminal).`,
  )
  return list
})
</script>

<template>
  <main class="intro">
    <section class="panel intro__card" aria-labelledby="intro-title">
      <div class="brand">
        <BrandMark />
        <p class="eyebrow eyebrow--page">{{ playground ? 'Playground' : 'Lab briefing' }}</p>
      </div>
      <h1 id="intro-title" ref="heading" class="intro__title" tabindex="-1">{{ run.title }}</h1>
      <dl v-if="!playground" class="intro__facts">
        <div v-for="f in facts" :key="f.label" class="stat">
          <dt class="stat__label">{{ f.label }}</dt>
          <dd class="stat__value">{{ f.value }}</dd>
        </div>
      </dl>

      <div v-if="run.intro.prerequisitesHtml" class="intro__block">
        <h2 class="eyebrow">Before you start</h2>
        <!-- Rendered and sanitised on the server; CSP blocks inline script anyway. -->
        <div class="prose" v-html="run.intro.prerequisitesHtml" />
      </div>

      <div v-if="!playground" class="intro__block">
        <h2 class="eyebrow">Rules</h2>
        <ul>
          <li v-for="r in rules" :key="r">{{ r }}</li>
        </ul>
      </div>

      <hr class="rule" />

      <div class="intro__actions">
        <p class="readiness" :class="ready ? 'readiness--ok' : 'readiness--busy'" role="status">
          <span class="dot" aria-hidden="true" />
          {{ ready ? 'Environment ready' : 'Preparing the environment…' }}
        </p>
        <template v-if="!playground">
          <p class="small muted">The timer starts when you click Start, not before.</p>
          <button
            type="button"
            class="btn btn--primary btn--lg"
            :disabled="!ready || starting"
            @click="$emit('start')"
          >
            {{ starting ? 'Starting…' : 'Start' }}
          </button>
        </template>
        <p v-if="error" class="error" role="alert">Could not start: {{ error }}</p>
      </div>
    </section>
  </main>
</template>
