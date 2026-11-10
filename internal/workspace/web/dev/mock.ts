// Development only: a stand-in for the workspace server so the page can be
// worked on with `npm run dev` and no lab. Never built into dist/.
//
//   npm run dev                       mock server (exam lab)
//   MOCK_KIND=playground npm run dev  mock playground
//   open http://127.0.0.1:5173/#code=dev&lab=demo   (code "expired" shows the 401 page)
//
// To develop against a real workspace server instead, set
// ASTRONA_WORKSPACE=http://127.0.0.1:<port> (the port is in
// <profile home>/workspace/server.json): /api and /ws are proxied there with
// the Origin and Host rewritten to the server's own. Take the launch code
// from the URL astrona prints and open it on port 5173.

import { createHash, randomUUID } from 'node:crypto'
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'
import type { Duplex } from 'node:stream'
import type { Plugin, PreviewServer, ViteDevServer } from 'vite'

const realServer = process.env.ASTRONA_WORKSPACE

export function workspaceDevServer(): Plugin {
  if (realServer) return proxyPlugin(realServer)
  return mockPlugin()
}

// ---------------------------------------------------------------- proxy

function proxyPlugin(target: string): Plugin {
  const origin = new URL(target).origin
  return {
    name: 'astrona-workspace-proxy',
    config: () => ({
      server: {
        proxy: {
          '/api': { target, changeOrigin: true, headers: { origin } },
          '/ws': { target, ws: true, changeOrigin: true, headers: { origin } },
        },
      },
    }),
  }
}

// ---------------------------------------------------------------- mock

const playground = process.env.MOCK_KIND === 'playground'

// Shapes follow docs/reference/workspace-api.md.
const TIME_LIMIT_SECONDS = 45 * 60
const TARGET = 'demo'
const LINKED = 'demo-edge'

// A stand-in for a page the lab serves on a port forward. It runs on its own
// port: the workspace never frames its own origin.
export const MOCK_APP_ORIGIN = 'http://127.0.0.1:4199'

// MOCK_TRACKING=account|local-only|none picks where a result goes (account).
const tracking = process.env.MOCK_TRACKING ?? 'account'

const state = {
  startedAt: null as string | null,
  deadlineAt: null as string | null,
  tokens: new Set<string>(),
  tickets: new Set<string>(),
  grading: false,
  removed: false,
  attempts: 0,
  submission: null as Record<string, unknown> | null,
  keys: new Map<string, Record<string, unknown>>(),
}

const started = () => playground || state.startedAt !== null

function status(): string {
  if (state.removed) return 'stopped'
  if (playground) return 'running'
  if (!state.startedAt) return 'ready'
  return state.deadlineAt && Date.parse(state.deadlineAt) <= Date.now() ? 'expired' : 'running'
}

function run() {
  return {
    lab: 'demo',
    runId: 'f'.repeat(64),
    title: playground ? 'Kubernetes playground' : 'Restrict ingress to the API',
    mode: playground ? 'learning' : 'exam',
    playground,
    status: status(),
    startedAt: state.startedAt,
    deadlineAt: state.deadlineAt,
    timeLimitSeconds: playground ? null : TIME_LIMIT_SECONDS,
    serverNow: new Date().toISOString(),
    rules: { strict: !playground, hintsHidden: !playground },
    intro: {
      prerequisitesHtml: playground ? '' : '<p>Know how NetworkPolicy <code>podSelector</code> and <code>from</code> rules combine.</p>\n',
      estimatedDuration: playground ? '' : '30m',
    },
    questions: [{ id: 'q1', title: 'Restrict ingress to the API', points: null }],
    targets: [
      { name: TARGET, label: 'main', kind: 'kind', views: ['terminal'] },
      { name: LINKED, label: 'edge', kind: 'kind', views: ['terminal'] },
    ],
    tabs: TABS,
    canSubmit: submitBlocked() === '',
    submit: {
      tracking,
      keepEnvironment: false,
      grading: state.grading,
      submitted: state.submission !== null,
      blocked: submitBlocked(),
    },
    runAgain: 'astrona run astrona.io/ATS014/section-010/module-01/lab-02',
  }
}

function submitBlocked(): string {
  if (state.removed) return 'the lab is no longer running'
  if (playground) return 'a playground has no checks to grade'
  if (state.grading) return 'the lab is being submitted'
  if (!state.startedAt) return 'start the lab first'
  return ''
}

// A sample graded result: names, pass/fail, points and an allowed hint only.
// The first attempt fails (the attempt stays open); later ones pass.
function grade(): Record<string, unknown> {
  state.attempts++
  const checks = [
    { name: 'NetworkPolicy api exists in shop', pass: true, points: 2 },
    { name: 'Frontend pods reach the API on 8080', pass: true, points: 4 },
    { name: 'Other pods cannot reach the API', pass: state.attempts > 1, points: 4 },
  ]
  const earned = checks.filter((c) => c.pass).reduce((n, c) => n + c.points, 0)
  const pass = checks.every((c) => c.pass)
  const sent = tracking === 'account'
  const finished = pass && (sent || tracking === 'local-only')
  const messages: string[] = []
  if (sent) messages.push(`Sent to your lab page (attempt ${state.attempts}): https://astrona.io/labs/demo`)
  if (tracking === 'local-only') messages.push('This run is local only; the result was not sent to your account.')
  if (finished) messages.push('Lab demo deleted.')
  if (finished) state.removed = true
  return {
    lab: 'demo',
    runId: 'f'.repeat(64),
    result: {
      pass,
      earned,
      max: 10,
      percent: earned * 10,
      passPercent: 66,
      overTime: false,
      attempt: state.attempts,
      time: new Date().toISOString(),
      checks,
    },
    recorded: true,
    sent,
    queued: false,
    finished,
    environmentRemoved: finished,
    messages,
  }
}

const TABS = [
  { name: 'main', title: 'Terminal', kind: 'terminal', focus: true, target: TARGET },
  { name: 'cluster-edge', title: 'edge', kind: 'terminal', focus: false, target: LINKED },
  { name: 'shop', title: 'Shop', kind: 'browser', focus: false, url: `${MOCK_APP_ORIGIN}/` },
  { name: 'k8s-docs', title: 'Kubernetes docs', kind: 'docs', focus: false, url: 'https://kubernetes.io/docs/concepts/services-networking/network-policies/' },
  { name: 'notes', title: 'Notes', kind: 'docs', focus: false, document: true },
]

const notesHTML = `<h1>Notes</h1>
<p>A document shipped with the lab, rendered like the task.</p>
<pre><code>kubectl -n shop describe networkpolicy api</code></pre>
<script>alert('never runs')</script>`

const questionHTML = `
<p>The <code>shop</code> namespace runs an API that every pod in the cluster can reach today.</p>
<h2>Requirements</h2>
<ul>
<li>Create a NetworkPolicy named <code>api</code> in namespace <code>shop</code>.</li>
<li>Only pods labelled <code>role=frontend</code> may reach pods labelled <code>app=api</code> on TCP 8080.</li>
<li>All other ingress to the API is denied.</li>
</ul>
<pre><code>kubectl -n shop get pods --show-labels</code></pre>
<blockquote><p>Run <code>stty size</code> to see the terminal follow the divider.</p></blockquote>
<script>alert('never runs')</script>`

function send(res: ServerResponse, code: number, body: unknown): void {
  res.statusCode = code
  res.setHeader('Content-Type', 'application/json; charset=utf-8')
  res.end(JSON.stringify(body))
}

function fail(res: ServerResponse, code: number, errorCode: string, message: string): void {
  send(res, code, { error: message, code: errorCode })
}

async function readJSON(req: IncomingMessage): Promise<Record<string, unknown>> {
  let raw = ''
  for await (const chunk of req) raw += chunk
  try {
    return raw ? JSON.parse(raw) : {}
  } catch {
    return {}
  }
}

function authed(req: IncomingMessage): boolean {
  const h = req.headers.authorization ?? ''
  return h.startsWith('Bearer ') && state.tokens.has(h.slice(7))
}

// The same mock serves `npm run dev` and `npm run preview` (the built dist/
// under the server's real Content Security Policy, see vite.config.ts).
function mockPlugin(): Plugin {
  const install = (server: ViteDevServer | PreviewServer): void => {
      server.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url ?? '/', 'http://localhost')
        if (!url.pathname.startsWith('/api/v1/')) return next()
        const p = url.pathname.slice('/api/v1'.length)
        const m = req.method

        if (m === 'POST' && p === '/auth/exchange') {
          const { code } = await readJSON(req)
          if (!code || code === 'expired') return fail(res, 401, 'unauthorized', 'invalid or expired launch code')
          const token = randomUUID()
          state.tokens.add(token)
          return send(res, 200, { token })
        }
        if (!authed(req)) return fail(res, 401, 'unauthorized', 'missing or invalid token')

        if (m === 'POST' && p === '/auth/ticket') {
          const ticket = randomUUID()
          state.tickets.add(ticket)
          return send(res, 200, { ticket, expiresAt: new Date(Date.now() + 30_000).toISOString() })
        }
        if (m === 'GET' && p === '/server') {
          return send(res, 200, {
            version: 'dev',
            profile: 'mock',
            startedAt: new Date().toISOString(),
            serverNow: new Date().toISOString(),
            features: ['runs', 'terminals', 'tabs', 'submit'],
            assets: 'built',
          })
        }
        if (m === 'GET' && p === '/runs') {
          const r = run()
          return send(res, 200, { runs: [{ lab: r.lab, title: r.title, mode: r.mode, playground: r.playground, status: r.status }] })
        }
        if (m === 'GET' && p === '/runs/demo') return send(res, 200, run())
        if (m === 'POST' && p === '/runs/demo/start') {
          const { runId } = await readJSON(req)
          if (runId !== run().runId) return fail(res, 409, 'run_changed', 'the lab was restarted; reload the run')
          if (!playground && !state.startedAt) {
            state.startedAt = new Date().toISOString()
            state.deadlineAt = new Date(Date.now() + TIME_LIMIT_SECONDS * 1000).toISOString()
          }
          return send(res, 200, run())
        }
        if (m === 'GET' && p === '/runs/demo/questions/q1') {
          if (!started()) return fail(res, 409, 'not_started', 'click Start first')
          return send(res, 200, { id: 'q1', title: 'Restrict ingress to the API', html: questionHTML, points: null, target: TARGET, hints: [] })
        }
        if (m === 'GET' && p === '/runs/demo/tabs/notes/doc') {
          if (!started()) return fail(res, 409, 'not_started', 'click Start first')
          return send(res, 200, { name: 'notes', title: 'Notes', html: notesHTML })
        }
        if (m === 'GET' && p === '/runs/demo/terminals') {
          return send(res, 200, { terminals: [...shells.values()].map((sh) => sh.info()) })
        }
        if (m === 'POST' && p === '/runs/demo/terminals') {
          if (!started()) return fail(res, 409, 'not_started', 'click Start first')
          const body = await readJSON(req)
          const name = typeof body.tab === 'string' && body.tab ? body.tab : 'main'
          const tab = TABS.find((t) => t.name === name && t.kind === 'terminal')
          if (!tab) return fail(res, 400, 'unknown_tab', `this lab has no terminal tab ${name}`)
          const existing = shells.get(name)
          if (existing?.alive) return send(res, 200, existing.info())
          if (starting.has(name)) return fail(res, 409, 'terminal_starting', 'the terminal of this tab is starting')
          starting.add(name)
          await new Promise((r) => setTimeout(r, 1500)) // the real one builds an image the first time
          starting.delete(name)
          existing?.forget()
          const sh = newShell(name, tab.target!)
          shells.set(name, sh)
          return send(res, 201, sh.info())
        }
        if (m === 'POST' && p === '/runs/demo/submit') {
          const key = String(req.headers['idempotency-key'] ?? '')
          if (!/^[A-Za-z0-9_-]{8,128}$/.test(key))
            return fail(res, 400, 'invalid_idempotency_key', 'an Idempotency-Key header is required')
          const { runId } = await readJSON(req)
          if (runId !== run().runId) return fail(res, 409, 'run_changed', 'this is not the lab’s current run; reload')
          const replay = state.keys.get(key)
          if (replay) return send(res, 200, replay)
          if (state.grading) return fail(res, 409, 'submit_in_progress', 'this lab is already being submitted')
          const blocked = submitBlocked()
          if (blocked) return fail(res, 409, playground ? 'no_grading' : 'not_started', blocked)
          state.grading = true
          await new Promise((r) => setTimeout(r, 3000)) // checks take a while
          state.grading = false
          const sub = grade()
          state.submission = sub
          state.keys.set(key, sub)
          forgetShells()
          return send(res, 200, sub)
        }
        if (m === 'GET' && p === '/runs/demo/submission') {
          if (state.grading) return send(res, 200, { state: 'grading' })
          if (!state.submission) return fail(res, 404, 'no_submission', 'this run has no submission')
          return send(res, 200, { state: 'done', submission: state.submission })
        }
        if (m === 'POST' && p === '/runs/demo/destroy') {
          const { runId } = await readJSON(req)
          if (runId !== run().runId || state.removed) return fail(res, 409, 'run_changed', 'this is not the lab’s current run; reload')
          if (state.grading) return fail(res, 409, 'submit_in_progress', 'this lab is being submitted')
          await new Promise((r) => setTimeout(r, 1500))
          state.removed = true
          forgetShells()
          return send(res, 200, { environmentRemoved: true, messages: ['Lab demo deleted.'] })
        }
        return fail(res, 404, 'not_found', 'not found')
      })

      server.httpServer?.on('upgrade', (req: IncomingMessage, socket: Duplex) => {
        const url = new URL(req.url ?? '/', 'http://localhost')
        const match = /^\/ws\/v1\/terminals\/([^/]+)$/.exec(url.pathname)
        if (!match) return // Vite's own HMR socket
        const ticket = url.searchParams.get('ticket') ?? ''
        if (!state.tickets.delete(ticket)) {
          socket.end('HTTP/1.1 401 Unauthorized\r\n\r\n')
          return
        }
        const shell = [...shells.values()].find((x) => x.id === match[1])
        if (!shell) {
          socket.end('HTTP/1.1 404 Not Found\r\n\r\n')
          return
        }
        const accept = createHash('sha1')
          .update(`${req.headers['sec-websocket-key']}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
          .digest('base64')
        socket.write(
          `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
        )
        shell.attach(socket)
      })
  }
  return {
    name: 'astrona-workspace-mock',
    configureServer: (server) => {
      install(server)
      startMockApp()
    },
    configurePreviewServer: (server) => {
      install(server)
      startMockApp()
    },
  }
}

let mockApp = false

function startMockApp(): void {
  if (mockApp) return
  mockApp = true
  const page = `<!doctype html><meta charset="utf-8"><title>Shop</title>
<body style="font-family:system-ui;margin:2rem"><h1>Shop</h1><p>A page the lab serves on a port forward.</p>
<p>Loaded at ${new Date().toISOString()}</p></body>`
  createServer((_req, res) => {
    res.setHeader('Content-Type', 'text/html; charset=utf-8')
    res.end(page)
  })
    .on('error', () => {
      mockApp = false
    })
    .listen(Number(new URL(MOCK_APP_ORIGIN).port), '127.0.0.1')
}

// ---------------------------------------------------------------- fake shell

const PROMPT = '\x1b[38;5;208mastrona-cli\x1b[0m > '

const shells = new Map<string, Shell>()
const starting = new Set<string>()

type Shell = ReturnType<typeof newShell>

// A submitted or removed lab has no terminals left.
function forgetShells(): void {
  for (const sh of shells.values()) sh.forget()
  shells.clear()
}

function newShell(tab: string, target: string) {
  const sh = {
  tab,
  target,
  id: '',
  alive: false,
  createdAt: '',
  buffer: '',
  line: '',
  cols: 80,
  rows: 24,
  socket: null as Duplex | null,

  info() {
    return {
      id: this.id,
      tab: this.tab,
      target: this.target,
      status: this.alive ? 'running' : 'ended',
      exitCode: this.alive ? null : 0,
      createdAt: this.createdAt,
      attached: this.socket !== null,
    }
  },

  reset() {
    this.id = randomUUID().replace(/-/g, '')
    this.alive = true
    this.createdAt = new Date().toISOString()
    this.buffer = ''
    this.line = ''
    this.out(`\x1b[32mMock terminal on ${this.target}.\x1b[0m Try: stty size, ls /, echo hi, exit\r\n` + PROMPT)
  },

  forget() {
    if (this.socket) close(this.socket, 1001)
    this.socket = null
    this.id = ''
    this.alive = false
  },

  out(text: string) {
    this.buffer = (this.buffer + text).slice(-64_000)
    if (this.socket) frame(this.socket, 2, Buffer.from(text))
  },

  attach(socket: Duplex) {
    if (this.socket) {
      const old = this.socket
      frame(old, 1, Buffer.from(JSON.stringify({ type: 'takenOver' })))
      close(old, 4001)
    }
    this.socket = socket
    frame(socket, 1, Buffer.from(JSON.stringify({ type: 'attached', terminal: this.info() })))
    if (this.buffer) frame(socket, 2, Buffer.from(this.buffer))
    let pending = Buffer.alloc(0)
    socket.on('data', (chunk: Buffer) => {
      pending = Buffer.concat([pending, chunk])
      for (;;) {
        const msg = parseFrame(pending)
        if (!msg) break
        pending = pending.subarray(msg.length)
        if (msg.opcode === 8) {
          close(socket, 1000)
          return
        }
        if (msg.opcode === 1) this.control(msg.payload.toString())
        if (msg.opcode === 2) this.input(msg.payload.toString())
      }
    })
    socket.on('close', () => {
      if (this.socket === socket) this.socket = null
    })
    socket.on('error', () => socket.destroy())
  },

  control(text: string) {
    try {
      const msg = JSON.parse(text)
      if (msg.type === 'resize') {
        this.cols = msg.cols
        this.rows = msg.rows
      } else if (msg.type === 'ping' && this.socket) {
        frame(this.socket, 1, Buffer.from(JSON.stringify({ type: 'pong' })))
      }
    } catch {
      // ignore
    }
  },

  input(data: string) {
    for (const ch of data) {
      if (ch === '\r') {
        this.out('\r\n')
        this.run(this.line.trim())
        this.line = ''
      } else if (ch === '\x7f') {
        if (this.line) {
          this.line = this.line.slice(0, -1)
          this.out('\b \b')
        }
      } else if (ch >= ' ') {
        this.line += ch
        this.out(ch)
      }
    }
  },

  run(cmd: string) {
    if (cmd === 'exit') {
      this.alive = false
      if (this.socket) {
        frame(this.socket, 1, Buffer.from(JSON.stringify({ type: 'ended', exitCode: 0 })))
        close(this.socket, 1000)
      }
      return
    }
    const outputs: Record<string, string> = {
      'stty size': `${this.rows} ${this.cols}`,
      'ls /': 'bin  etc  home  lib  proc  root  tmp  usr  var  workspace',
      'echo $HOME': '/workspace',
    }
    if (cmd) {
      const echo = cmd.startsWith('echo ') ? cmd.slice(5) : undefined
      this.out((outputs[cmd] ?? echo ?? `${cmd.split(' ')[0]}: command not found (mock)`) + '\r\n')
    }
    this.out(PROMPT)
  },
  }
  sh.reset()
  return sh
}

function frame(socket: Duplex, opcode: number, payload: Buffer): void {
  const len = payload.length
  const head =
    len < 126
      ? Buffer.from([0x80 | opcode, len])
      : len < 65536
        ? Buffer.from([0x80 | opcode, 126, len >> 8, len & 0xff])
        : (() => {
            const b = Buffer.alloc(10)
            b[0] = 0x80 | opcode
            b[1] = 127
            b.writeBigUInt64BE(BigInt(len), 2)
            return b
          })()
  socket.write(Buffer.concat([head, payload]))
}

function close(socket: Duplex, code: number): void {
  const b = Buffer.alloc(2)
  b.writeUInt16BE(code)
  frame(socket, 8, b)
  socket.end()
}

function parseFrame(buf: Buffer): { opcode: number; payload: Buffer; length: number } | null {
  if (buf.length < 2) return null
  const opcode = buf[0]! & 0x0f
  const masked = (buf[1]! & 0x80) !== 0
  let len = buf[1]! & 0x7f
  let offset = 2
  if (len === 126) {
    if (buf.length < 4) return null
    len = buf.readUInt16BE(2)
    offset = 4
  } else if (len === 127) {
    if (buf.length < 10) return null
    len = Number(buf.readBigUInt64BE(2))
    offset = 10
  }
  const maskOffset = offset
  if (masked) offset += 4
  if (buf.length < offset + len) return null
  const payload = Buffer.from(buf.subarray(offset, offset + len))
  if (masked) for (let i = 0; i < len; i++) payload[i] = payload[i]! ^ buf[maskOffset + (i % 4)]!
  return { opcode, payload, length: offset + len }
}
