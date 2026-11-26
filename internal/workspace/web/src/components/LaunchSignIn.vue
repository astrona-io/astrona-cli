<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { cancelSignIn, getSignIn, isUnauthorized, startSignIn, type SignIn } from '../api'

// "Sign in to save your time and result" on the launch page of a signed-out
// `astrona run` (docs/guides/browser-workspace.md). The workspace server
// runs the same sign-in as `astrona login`: this shows the code and a link
// to the site's authorize page, which the student opens in a new tab, and
// polls until the sign-in is done. Begin then links the run (LaunchPage).

const props = defineProps<{ launchId: string; site?: string; playground?: boolean }>()
const emit = defineEmits<{ signedIn: [signIn: SignIn]; unauthorized: [] }>()

const POLL_MS = 2000
const signIn = shallowRef<SignIn | null>(null)
const busy = ref(false)
const error = ref<string | null>(null)
let timer: number | undefined
let stopped = false

/** The run's site, only as an http(s) origin. */
const siteURL = computed<URL | null>(() => {
  try {
    const u = new URL(props.site ?? '')
    return u.protocol === 'https:' || u.protocol === 'http:' ? u : null
  } catch {
    return null
  }
})
const host = computed(() => siteURL.value?.host ?? 'Astrona')

/** The authorize page, only when it is on the run's own site. */
const authorizeHref = computed(() => {
  const raw = signIn.value?.verificationUrl
  if (!raw || !siteURL.value) return ''
  try {
    const u = new URL(raw)
    return (u.protocol === 'https:' || u.protocol === 'http:') && u.origin === siteURL.value.origin ? u.href : ''
  } catch {
    return ''
  }
})

const pending = computed(() => signIn.value?.state === 'pending')
const ended = computed(() => {
  const s = signIn.value?.state
  return s === 'denied' || s === 'expired' || s === 'device_limit' || s === 'cancelled' || s === 'failed'
})

function fail(err: unknown): void {
  if (isUnauthorized(err)) {
    stopped = true
    emit('unauthorized')
    return
  }
  error.value = err instanceof Error ? err.message : String(err)
}

function show(s: SignIn): void {
  signIn.value = s
  window.clearTimeout(timer)
  if (s.state === 'signed-in') {
    stopped = true
    emit('signedIn', s)
    return
  }
  if (s.state === 'pending' && !stopped) timer = window.setTimeout(poll, POLL_MS)
}

async function poll(): Promise<void> {
  try {
    show(await getSignIn(props.launchId))
    error.value = null
  } catch (err) {
    fail(err)
    if (!stopped) timer = window.setTimeout(poll, POLL_MS)
  }
}

async function begin(): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = null
  try {
    show(await startSignIn(props.launchId))
  } catch (err) {
    fail(err)
  } finally {
    busy.value = false
  }
}

async function cancel(): Promise<void> {
  if (busy.value) return
  busy.value = true
  try {
    show(await cancelSignIn(props.launchId))
  } catch (err) {
    fail(err)
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  // A reload while signing in picks the sign-in up again.
  try {
    const s = await getSignIn(props.launchId)
    if (s.state !== 'none') show(s)
  } catch (err) {
    if (isUnauthorized(err)) emit('unauthorized')
  }
})
onBeforeUnmount(() => {
  stopped = true
  window.clearTimeout(timer)
})
</script>

<template>
  <div class="signin" aria-live="polite">
    <template v-if="pending">
      <p class="signin__step">
        <span class="eyebrow">Your code</span>
        <code class="signin__code mono">{{ signIn?.userCode }}</code>
      </p>
      <p class="muted">
        Open {{ host }} and check that it shows the same code, then click Authorize. If you are not signed in there, sign in
        first. This page continues by itself.
      </p>
      <div class="account__actions">
        <a v-if="authorizeHref" class="btn btn--primary btn--sm" :href="authorizeHref" target="_blank" rel="noopener noreferrer">
          Open {{ host }} to authorize <span aria-hidden="true">↗</span>
        </a>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="cancel">Cancel</button>
      </div>
      <p class="readiness readiness--busy small" role="status">
        <span class="dot" aria-hidden="true" /> Waiting for you to authorize this computer…
      </p>
    </template>

    <template v-else>
      <p v-if="ended && signIn?.message" class="small" role="alert">{{ signIn.message }}</p>
      <div class="account__actions">
        <button type="button" class="btn btn--primary btn--sm" :disabled="busy" @click="begin">
          {{ ended ? 'Sign in again' : playground ? 'Sign in to save your time' : 'Sign in to save your time and result' }}
        </button>
      </div>
      <p class="small muted">
        The same sign-in as <code>astrona login</code>, on {{ host }}. Signed in before you click Begin session, this run is
        saved to your account.
      </p>
    </template>

    <p v-if="error" class="error small" role="alert">{{ error }}</p>
  </div>
</template>
