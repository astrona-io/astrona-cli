import { computed, onBeforeUnmount, ref, type Ref } from 'vue'

// The server owns the deadline. The page only displays it, using the offset
// between the server's clock (serverNow in each response) and the local one,
// so a wrong local clock cannot stretch or shrink the time left. Date.now()
// keeps counting while a laptop sleeps, so waking up shows the right value.

let offsetMs = 0

/** Records the server's clock from a response's serverNow. */
export function syncServerClock(serverNow: string | undefined): void {
  if (!serverNow) return
  const server = Date.parse(serverNow)
  if (!Number.isNaN(server)) offsetMs = server - Date.now()
}

export function serverNow(): number {
  return Date.now() + offsetMs
}

/** Milliseconds left until deadlineAt, ticking once a second; null without a deadline. */
export function useCountdown(deadlineAt: Ref<string | null>) {
  const now = ref(serverNow())
  const timer = window.setInterval(() => {
    now.value = serverNow()
  }, 1000)
  onBeforeUnmount(() => window.clearInterval(timer))

  return computed<number | null>(() => {
    if (!deadlineAt.value) return null
    const deadline = Date.parse(deadlineAt.value)
    if (Number.isNaN(deadline)) return null
    return Math.max(0, deadline - now.value)
  })
}

/** Milliseconds since startedAt, ticking once a second; null without a start. */
export function useElapsed(startedAt: Ref<string | null>) {
  const now = ref(serverNow())
  const timer = window.setInterval(() => {
    now.value = serverNow()
  }, 1000)
  onBeforeUnmount(() => window.clearInterval(timer))

  return computed<number | null>(() => {
    if (!startedAt.value) return null
    const started = Date.parse(startedAt.value)
    if (Number.isNaN(started)) return null
    return Math.max(0, now.value - started)
  })
}

export function formatDuration(ms: number): string {
  const total = Math.ceil(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`
}
