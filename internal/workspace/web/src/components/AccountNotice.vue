<script setup lang="ts">
import { computed } from 'vue'
import type { AccountStatus } from '../api'

// Whose Astrona account this run is saved to, as `astrona run` decided it
// (docs/guides/browser-workspace.md). The preparing page shows the full
// card; the lab briefing one line. A lab from the student's own files
// ("untracked") shows nothing: no account applies to it.

const props = defineProps<{ account?: AccountStatus | null; compact?: boolean; playground?: boolean }>()

/** The CLI's own site, only as an http(s) origin; null otherwise. */
const site = computed<URL | null>(() => {
  const raw = props.account?.site
  if (!raw) return null
  try {
    const u = new URL(raw)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u : null
  } catch {
    return null
  }
})

const host = computed(() => site.value?.host ?? '')
const signupHref = computed(() => (site.value ? new URL('/signup', site.value.origin).href : ''))
const state = computed(() => props.account?.state)
const signedInAs = computed(() => {
  const user = props.account?.username
  if (user && host.value) return `Signed in as ${user} on ${host.value}`
  if (user) return `Signed in as ${user}`
  return host.value ? `Signed in on ${host.value}` : 'Signed in to Astrona'
})
</script>

<template>
  <template v-if="compact">
    <p v-if="state === 'signed-in'" class="account-line small">
      <span class="readiness readiness--ok"><span class="dot" aria-hidden="true" /> Saved to your Astrona account</span>
      <span class="muted">{{ signedInAs }}.</span>
    </p>
    <p v-else-if="state === 'signed-out'" class="account-line small">
      <span class="readiness readiness--info"><span class="dot" aria-hidden="true" /> Not signed in</span>
      <span class="muted">This run is not saved to an Astrona account.</span>
      <a v-if="signupHref" :href="signupHref" target="_blank" rel="noopener noreferrer">Create a free account</a>
    </p>
    <p v-else-if="state === 'local-only'" class="account-line small">
      <span class="readiness readiness--info"><span class="dot" aria-hidden="true" /> Local only</span>
      <span class="muted">Nothing from this run is sent to your Astrona account.</span>
    </p>
  </template>

  <section v-else-if="state === 'signed-in'" class="account" aria-labelledby="account-title">
    <h2 class="eyebrow">Astrona account</h2>
    <p id="account-title" class="account__title readiness readiness--ok">
      <span class="dot" aria-hidden="true" /> <span class="account__name">{{ signedInAs }}</span>
    </p>
    <p v-if="playground" class="muted">
      This run is linked to your Astrona account. The time you spend in this playground is saved to your account.
    </p>
    <p v-else class="muted">
      This run is linked to your Astrona account. The time you spend and your submitted result, pass or fail, are saved
      there. Submitting ends the attempt and removes the lab.
    </p>
  </section>

  <section v-else-if="state === 'signed-out'" class="account" aria-labelledby="account-title">
    <h2 class="eyebrow">Astrona account</h2>
    <p id="account-title" class="account__title readiness readiness--info">
      <span class="dot" aria-hidden="true" /> This run is not saved to an Astrona account
    </p>
    <p v-if="playground" class="muted">
      You are not signed in, so the time you spend in this playground stays on this computer. That is fine for trying
      it out.
    </p>
    <p v-else class="muted">
      You are not signed in, so the time you spend and your result stay on this computer. That is fine for trying a lab
      out.
    </p>
    <p v-if="playground">
      Become an Astrona student to keep your work: with a free account, the time you spend in a playground is saved to
      your account.
    </p>
    <p v-else>
      Become an Astrona student to keep your work: with a free account, the time you spend on a lab and your submitted
      result are saved to your account.
    </p>
    <div v-if="signupHref" class="account__actions">
      <a class="btn btn--primary btn--sm" :href="signupHref" target="_blank" rel="noopener noreferrer">
        Create a free Astrona account <span aria-hidden="true">↗</span>
      </a>
    </div>
    <p class="muted">
      Then run <code>astrona login</code> in your terminal and start the {{ playground ? 'playground' : 'lab' }} again: that run is saved to your
      account.
    </p>
    <p class="small muted">
      Signing in now does not link this run. A run is linked to an account when <code>astrona run</code> starts, and
      this one started without one.
    </p>
  </section>

  <section v-else-if="state === 'local-only'" class="account" aria-labelledby="account-title">
    <h2 class="eyebrow">Astrona account</h2>
    <p id="account-title" class="account__title readiness readiness--info">
      <span class="dot" aria-hidden="true" /> Local only
    </p>
    <p class="muted">
      You started this run with <code>--local-only</code>: nothing from it is sent to your Astrona account.
      {{ playground ? 'The time you spend stays on this computer.' : 'The time you spend and your result stay on this computer.' }}
    </p>
  </section>
</template>
