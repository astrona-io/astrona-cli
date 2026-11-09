// The only file that knows the workspace server's API: paths, JSON shapes,
// the bearer token and the terminal WebSocket frame format. Everything else
// imports the types and functions below. The contract is documented in
// docs/reference/workspace-api.md.

// ---------------------------------------------------------------- types

export type RunMode = 'exam' | 'learning'
/** provisioning and failed are reserved by the server for later milestones. */
export type RunStatus = 'provisioning' | 'ready' | 'running' | 'expired' | 'stopped' | 'failed'

/** What a target can show in the right panel. "desktop" comes later. */
export type ViewKind = 'terminal' | 'desktop'

export interface Target {
  /** What POST /terminals takes. */
  name: string
  /** Display name: "main", or a linked lab's name. */
  label: string
  kind: string
  views: ViewKind[]
}

export interface QuestionSummary {
  id: string
  title: string
  points: number | null
}

export interface RunIntro {
  /** Rendered and sanitised on the server; "" when the lab has none. */
  prerequisitesHtml: string
  /** "" when the task does not say. */
  estimatedDuration: string
}

export interface Run {
  lab: string
  /** Opaque; sent back on every mutating request. */
  runId: string
  title: string
  mode: RunMode
  /** A playground has no intro and no Start. */
  playground: boolean
  status: RunStatus
  startedAt: string | null
  /** RFC 3339; null when there is no clock. */
  deadlineAt: string | null
  timeLimitSeconds: number | null
  /** The server's clock when it wrote this response, RFC 3339. */
  serverNow: string
  rules: { strict: boolean; hintsHidden: boolean }
  intro: RunIntro
  /** Summaries only, never the text. */
  questions: QuestionSummary[]
  targets: Target[]
  canSubmit: boolean
}

export interface RunListItem {
  lab: string
  title: string
  mode: RunMode
  playground: boolean
  status: RunStatus
}

export interface Question {
  id: string
  title: string
  /** Rendered and sanitised on the server. Inserted as given, never evaluated. */
  html: string
  points: number | null
  target: string
  hints: string[]
}

export interface Terminal {
  id: string
  target: string
  status: 'running' | 'ended'
  /** Set once ended; -1 when killed by a signal. */
  exitCode: number | null
  createdAt: string
  attached: boolean
}

// ---------------------------------------------------------------- errors

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    /** The server's error code, such as "not_started" or "run_changed". */
    readonly code: string = '',
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

/** The token is missing, expired or the server restarted. */
export function isUnauthorized(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401
}

/** The run was restarted, replaced or stopped: reload it. */
export function isRunGone(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409 && (err.code === 'run_changed' || err.code === 'run_stopped')
}

// ---------------------------------------------------------------- token

// sessionStorage is per tab and per origin, and the origin includes the
// port, so no other local server ever sees the token. No cookies.
const TOKEN_KEY = 'astrona.workspace.token'

function storage(): Storage | null {
  try {
    return window.sessionStorage
  } catch {
    return null
  }
}

let memoryToken: string | null = null

export function getToken(): string | null {
  return storage()?.getItem(TOKEN_KEY) ?? memoryToken
}

function setToken(token: string): void {
  memoryToken = token
  try {
    storage()?.setItem(TOKEN_KEY, token)
  } catch {
    // Storage disabled: the token lives in memory until the page reloads.
  }
}

export function clearToken(): void {
  memoryToken = null
  try {
    storage()?.removeItem(TOKEN_KEY)
  } catch {
    // nothing stored
  }
}

// ---------------------------------------------------------------- HTTP

const API = '/api/v1'

/**
 * How long a quick call waits before it counts as unreachable. A server that
 * is paused (or a laptop that slept) accepts the connection and never
 * answers; without a limit the page would wait forever.
 */
const QUICK_TIMEOUT_MS = 10_000

async function request<T>(method: string, path: string, body?: unknown, auth = true, timeoutMs?: number): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (auth) {
    const token = getToken()
    if (!token) throw new ApiError(401, 'no session token', 'unauthorized')
    headers.Authorization = `Bearer ${token}`
  }
  let res: Response
  let text: string
  try {
    res = await fetch(API + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'omit',
      cache: 'no-store',
      redirect: 'error',
      signal: timeoutMs === undefined ? undefined : AbortSignal.timeout(timeoutMs),
    })
    text = await res.text()
  } catch {
    throw new ApiError(0, 'The workspace server cannot be reached.')
  }
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }
  if (!res.ok) {
    const err = data as { error?: string; code?: string } | null
    throw new ApiError(res.status, err?.error ?? `${res.status} ${res.statusText}`, err?.code ?? '')
  }
  return data as T
}

const lab = (id: string) => `/runs/${encodeURIComponent(id)}`

/** Exchanges the launch code for a session token and stores it. */
export async function exchangeCode(code: string): Promise<void> {
  const res = await request<{ token: string }>('POST', '/auth/exchange', { code }, false)
  setToken(res.token)
}

/** A single-use WebSocket ticket, valid for 30 seconds. */
export async function getTicket(): Promise<string> {
  const res = await request<{ ticket: string; expiresAt: string }>('POST', '/auth/ticket', undefined, true, QUICK_TIMEOUT_MS)
  return res.ticket
}

export async function listRuns(): Promise<RunListItem[]> {
  const res = await request<{ runs: RunListItem[] }>('GET', '/runs')
  return res.runs ?? []
}

export function getRun(id: string): Promise<Run> {
  return request<Run>('GET', lab(id), undefined, true, QUICK_TIMEOUT_MS)
}

/** 409 not_started before Start (never for a playground). */
export function getQuestion(id: string, questionId: string): Promise<Question> {
  return request<Question>('GET', `${lab(id)}/questions/${encodeURIComponent(questionId)}`)
}

/** Starts the clock. Idempotent: a second call returns the same deadline. */
export function startRun(id: string, runId: string): Promise<Run> {
  return request<Run>('POST', `${lab(id)}/start`, { runId })
}

export async function listTerminals(id: string): Promise<Terminal[]> {
  const res = await request<{ terminals: Terminal[] }>('GET', `${lab(id)}/terminals`)
  return res.terminals ?? []
}

/** Can take a minute the first time, while the terminal image is built. */
export function openTerminal(id: string, runId: string, target: string): Promise<Terminal> {
  return request<Terminal>('POST', `${lab(id)}/terminals`, { runId, target })
}

/** Ends the shell and forgets the terminal. */
export async function closeTerminal(id: string, terminalId: string): Promise<void> {
  await request<null>('DELETE', `${lab(id)}/terminals/${encodeURIComponent(terminalId)}`)
}

// ---------------------------------------------------------------- terminal frames

/** WebSocket URL for attaching to a terminal, with a single-use ticket. */
export function terminalSocketURL(terminalId: string, ticket: string): string {
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${window.location.host}/ws/v1/terminals/${encodeURIComponent(terminalId)}?ticket=${encodeURIComponent(ticket)}`
}

// Terminal bytes travel as binary frames in both directions (input at most
// 64 KiB per frame). Control messages are JSON text frames and never mixed
// into the byte stream.

/** Largest input frame the server accepts. */
export const MAX_INPUT_FRAME = 64 * 1024

/** Control frames the server sends. */
export type ServerControl =
  | { type: 'attached'; terminal: Terminal }
  | { type: 'ended'; exitCode: number }
  | { type: 'takenOver' }
  | { type: 'overrun' }
  | { type: 'pong' }
  | { type: 'error'; message: string }

/** Close codes the server uses when it ends an attachment. */
export const CloseCode = {
  /** Normal close; follows an "ended" frame when the shell exited. */
  normal: 1000,
  /** The server shut down or the terminal was deleted. */
  goingAway: 1001,
  /** A frame was too large or not understood. */
  policy: 1008,
  /** The terminal was opened in another tab or view. Do not reconnect. */
  takenOver: 4001,
  /** This view fell behind the buffer. Reconnect for a fresh replay. */
  overrun: 4002,
} as const

export function parseServerControl(text: string): ServerControl | null {
  try {
    const msg = JSON.parse(text) as { type?: unknown }
    if (typeof msg?.type === 'string') return msg as ServerControl
  } catch {
    // not a control frame
  }
  return null
}

export function resizeFrame(cols: number, rows: number): string {
  return JSON.stringify({ type: 'resize', cols, rows })
}

export function pingFrame(): string {
  return JSON.stringify({ type: 'ping' })
}
