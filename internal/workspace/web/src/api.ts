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
  /** Display name: "main", a linked lab's name, or a VM's name. */
  label: string
  /** "kind" (a Kind cluster) or "vm" (one VM of a QEMU lab). */
  kind: string
  views: ViewKind[]
}

/** What a tab of the right panel shows. */
export type TabKind = 'terminal' | 'browser' | 'docs'

/**
 * One tab of the right panel as the lab declares it: the tabs the page
 * opens with. The learner can close them and open more from the tab bar
 * (src/components/LabTabs.vue). The first (focus: true) is the terminal on
 * the lab's own cluster.
 */
export interface RunTab {
  name: string
  title: string
  kind: TabKind
  focus: boolean
  /** terminal: the target its shell runs on. */
  target?: string
  /** browser: http(s)://127.0.0.1:<port>/… of a port forward; docs: an https URL. */
  url?: string
  /** docs shipped with the lab: GET /tabs/{name}/doc renders it. */
  document?: boolean
  /** docs: the document is the lab's solution; ask before showing it. */
  solution?: boolean
}

export interface TabDoc {
  name: string
  title: string
  /** Rendered and sanitised on the server. Inserted as given, never evaluated. */
  html: string
}

/**
 * A documentation link of the lab (its astrona.yaml entry's resources):
 * an https URL and a name that reads "Product: page" by convention.
 */
export interface DocLink {
  name: string
  url: string
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
  /** The right panel's tabs, the focus tab first. */
  tabs: RunTab[]
  /** GET /solution has a solution guide (after Start); never in an exam or a playground. */
  solution: boolean
  /** Badges above the task; each opens as a tab. Empty in an exam and when the lab lists none. */
  docLinks: DocLink[]
  /** True when POST /submit would grade now; submit.blocked says why not. */
  canSubmit: boolean
  submit: SubmitInfo
  /** The command that starts this lab again; "" when its source is unknown. */
  runAgain: string
  /** The account this run is saved to; absent when unknown. */
  account?: AccountStatus
}

/**
 * Whose account a run is saved to, as `astrona run` decided it: "signed-in"
 * (a lab session on that account), "signed-out" (a catalog lab run without
 * one), "local-only" (--local-only) or "untracked" (a lab from the student's
 * own files). Never a token: username and site only.
 */
export type AccountState = 'signed-in' | 'signed-out' | 'local-only' | 'untracked'

export interface AccountStatus {
  state: AccountState
  /** The signed-in student; may be absent. */
  username?: string
  /** The CLI's own Astrona site (scheme://host[:port]); never from lab content. */
  site?: string
}

/**
 * Where a result goes: "account" (the run has an Astrona lab session; sent
 * when astrona is signed in to its site), "local-only" (never sent) or
 * "none" (no lab session: it stays on this machine).
 */
export type Tracking = 'account' | 'local-only' | 'none'

export interface SubmitInfo {
  tracking: Tracking
  /** A submission of this run is being graded now. */
  grading: boolean
  /** While grading: each check's progress in grading order; absent until the checks start. */
  checks?: GradeCheck[]
  /** This run has a saved result (GET /submission). */
  submitted: boolean
  /** Why Submit is not possible now; "" when it is. */
  blocked: string
}

/** One check while a submission is graded: never its command or output. */
export interface GradeCheck {
  name: string
  state: 'waiting' | 'running' | 'passed' | 'failed'
  /** Set once the check runs. */
  startedAt?: string
  /** How long it took, once it is done. */
  seconds?: number
}

export interface SubmissionCheck {
  name: string
  pass: boolean
  points: number
  /** Only for a failed check, and only when the lab shows hints. */
  hint?: string
}

export interface Submission {
  lab: string
  runId: string
  result: {
    pass: boolean
    earned: number
    max: number
    percent: number
    passPercent?: number
    /** A strict exam submitted after the time limit: it cannot pass. */
    overTime: boolean
    /** Time spent on the run until this submission, when known. */
    elapsedSeconds?: number
    attempt: number
    time: string
    checks: SubmissionCheck[]
  }
  /** Saved in this machine's attempt history. */
  recorded: boolean
  /** Recorded on the Astrona lab page. */
  sent: boolean
  /** Saved to send once Astrona is reachable again. */
  queued: boolean
  /**
   * The attempt is over: a pass that was sent (or queued), or a local-only
   * run's pass saved here. A failed result never finishes it.
   */
  finished: boolean
  environmentRemoved: boolean
  /** What astrona reported while sending and cleaning up, as plain text. */
  messages: string[]
  /** The result stands, but removing the environment failed. */
  cleanupError?: string
}

export type SubmissionState =
  | { state: 'grading' }
  | { state: 'failed'; error: { code: string; message: string } }
  | { state: 'done'; submission: Submission }

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
  /** The terminal tab it belongs to. */
  tab: string
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

interface RequestOptions {
  /** Abort after this many milliseconds; no limit when unset. */
  timeoutMs?: number
  headers?: Record<string, string>
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  auth = true,
  { timeoutMs, headers: extraHeaders = {} }: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', ...extraHeaders }
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

/** A fresh single-use code for entering a run with an updated document policy. */
export async function getNavigationCode(): Promise<string> {
  const res = await request<{ code: string }>('POST', '/auth/code', undefined, true, { timeoutMs: QUICK_TIMEOUT_MS })
  return res.code
}

/** A single-use WebSocket ticket, valid for 30 seconds. */
export async function getTicket(): Promise<string> {
  const res = await request<{ ticket: string; expiresAt: string }>('POST', '/auth/ticket', undefined, true, { timeoutMs: QUICK_TIMEOUT_MS })
  return res.ticket
}

/** What the workspace server says about itself; the About dialog shows its version. */
export interface ServerInfo {
  version: string
  profile: string
}

export function getServer(): Promise<ServerInfo> {
  return request<ServerInfo>('GET', '/server', undefined, true, { timeoutMs: QUICK_TIMEOUT_MS })
}

export type LaunchStage = 'terms' | 'checking' | 'preparing' | 'ready' | 'failed' | 'declined' | 'ended'

export interface LaunchEvent {
  at: string
  kind: 'section' | 'step' | 'note' | 'ok' | 'skip' | 'fail' | 'warn'
  label: string
  detail?: string
  seconds?: number
}

/** An `astrona run` that opened the page before its lab exists. */
export interface Launch {
  id: string
  stage: LaunchStage
  message?: string
  terms?: { revision: string; sha256: string; text: string }
  title?: string
  lab?: string
  events: LaunchEvent[]
  status?: string
  error?: string
  logPath?: string
  /** The account the run is saved to, once astrona run knows it. */
  account?: AccountStatus
  /** Set with account: the lab is a playground (time, no attempts). */
  playground?: boolean
  /** False once the command in the terminal has exited. */
  alive: boolean
  answered?: 'accepted' | 'declined'
}

export function isLaunchGone(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404 && err.code === 'launch_gone'
}

export function getLaunch(id: string): Promise<Launch> {
  return request<Launch>('GET', `/launches/${encodeURIComponent(id)}`, undefined, true, { timeoutMs: QUICK_TIMEOUT_MS })
}

/** Accepts or declines the terms shown by launch id; sha256 is the notice the page showed. */
export function answerTerms(id: string, accept: boolean, sha256: string): Promise<{ accepted: boolean }> {
  return request('POST', `/launches/${encodeURIComponent(id)}/terms`, { accept, sha256 }, true, { timeoutMs: QUICK_TIMEOUT_MS })
}

/** One astrona lab on this machine (GET /labs), opened in the browser or not. */
export interface MachineLab {
  lab: string
  name: string
  title?: string
  runtime: 'kind' | 'qemu' | string
  state: 'running' | 'stopped' | 'degraded' | 'unknown'
  uptime?: string
  vms?: number
  mode?: string
  playground: boolean
  deadlineAt?: string
  /** The workspace has this run: the page can open, end or remove it. */
  workspace: boolean
  runId?: string
  runStatus?: string
}

export interface MachineLabs {
  labs: MachineLab[]
  /** Set when the container engine does not answer. */
  engine?: string
}

export async function listMachineLabs(): Promise<MachineLabs> {
  const res = await request<MachineLabs>('GET', '/labs', undefined, true, { timeoutMs: 30_000 })
  return { labs: res.labs ?? [], engine: res.engine }
}

export async function listRuns(): Promise<RunListItem[]> {
  const res = await request<{ runs: RunListItem[] }>('GET', '/runs')
  return res.runs ?? []
}

export function getRun(id: string): Promise<Run> {
  return request<Run>('GET', lab(id), undefined, true, { timeoutMs: QUICK_TIMEOUT_MS })
}

/** 409 not_started before Start (never for a playground). */
export function getQuestion(id: string, questionId: string): Promise<Question> {
  return request<Question>('GET', `${lab(id)}/questions/${encodeURIComponent(questionId)}`)
}

/** Starts the clock. Idempotent: a second call returns the same deadline. */
export function startRun(id: string, runId: string): Promise<Run> {
  return request<Run>('POST', `${lab(id)}/start`, { runId })
}

/**
 * Grades the run, records the attempt and sends it where the run is tracked
 * (see Run.submit). Can take minutes. The same key grades once: a retry
 * with it waits for, or returns, that result.
 */
export function submitRun(id: string, runId: string, idempotencyKey: string): Promise<Submission> {
  return request<Submission>('POST', `${lab(id)}/submit`, { runId }, true, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
}

/** The run's latest submission; 404 no_submission when there is none. */
export function getSubmission(id: string): Promise<SubmissionState> {
  return request<SubmissionState>('GET', `${lab(id)}/submission`)
}

/** Removes the run's environment without grading, as astrona destroy does. */
export function destroyRun(id: string, runId: string): Promise<{ environmentRemoved: boolean; messages: string[] }> {
  return request('POST', `${lab(id)}/destroy`, { runId })
}

/** A fresh Idempotency-Key for one submission. */
export function newIdempotencyKey(): string {
  const bytes = new Uint8Array(16)
  window.crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

export async function listTerminals(id: string): Promise<Terminal[]> {
  const res = await request<{ terminals: Terminal[] }>('GET', `${lab(id)}/terminals`)
  return res.terminals ?? []
}

/**
 * Opens the shell of a terminal tab, or returns the one still running.
 * Replaces an ended one. Can take a minute the first time, while the
 * terminal image is built. A tab the learner opened (term-<id>) names its
 * target; a tab the lab declared does not.
 */
export function openTerminal(id: string, runId: string, tab: string, target?: string): Promise<Terminal> {
  return request<Terminal>('POST', `${lab(id)}/terminals`, target ? { runId, tab, target } : { runId, tab })
}

/** Ends the shell of a terminal tab the learner closed. */
export function closeTerminalTab(id: string, runId: string, tab: string): Promise<{ closed: number }> {
  return request<{ closed: number }>('POST', `${lab(id)}/terminals/close`, { runId, tab }, true, { timeoutMs: QUICK_TIMEOUT_MS })
}

/** The lab's solution guide, rendered. 404 no_solution in an exam or a playground. */
/**
 * Whether a documentation link can be shown in a frame, as the server read
 * it from the site's headers: "refused" (X-Frame-Options or CSP
 * frame-ancestors), "allowed", or "unknown" when the check failed.
 */
export type FrameCheck = 'allowed' | 'refused' | 'unknown'

export function getDocLinkFrame(id: string, url: string): Promise<{ frame: FrameCheck; reason?: string }> {
  return request<{ frame: FrameCheck; reason?: string }>('GET', `${lab(id)}/doclinks/frame?url=${encodeURIComponent(url)}`)
}

export function getSolution(id: string): Promise<{ title: string; html: string }> {
  return request<{ title: string; html: string }>('GET', `${lab(id)}/solution`)
}

/** Another request is starting this tab's terminal: try again shortly. */
export function isTerminalStarting(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409 && err.code === 'terminal_starting'
}

/** A docs tab's Markdown, rendered. 409 not_started before Start. */
export function getTabDoc(id: string, tab: string): Promise<TabDoc> {
  return request<TabDoc>('GET', `${lab(id)}/tabs/${encodeURIComponent(tab)}/doc`)
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
