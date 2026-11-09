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
import type { IncomingMessage, ServerResponse } from 'node:http'
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

const state = {
  startedAt: null as string | null,
  deadlineAt: null as string | null,
  tokens: new Set<string>(),
  tickets: new Set<string>(),
}

const started = () => playground || state.startedAt !== null

function status(): string {
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
    targets: [{ name: TARGET, label: 'main', kind: 'kind', views: ['terminal'] }],
    canSubmit: false,
  }
}

function terminal() {
  return {
    id: shell.id,
    target: TARGET,
    status: shell.alive ? 'running' : 'ended',
    exitCode: shell.alive ? null : 0,
    createdAt: shell.createdAt,
    attached: shell.socket !== null,
  }
}

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
            features: ['runs', 'terminals'],
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
        if (m === 'GET' && p === '/runs/demo/terminals') {
          return send(res, 200, { terminals: shell.id ? [terminal()] : [] })
        }
        if (m === 'POST' && p === '/runs/demo/terminals') {
          if (!started()) return fail(res, 409, 'not_started', 'click Start first')
          await new Promise((r) => setTimeout(r, 1500)) // the real one builds an image the first time
          shell.reset()
          return send(res, 201, terminal())
        }
        const del = /^\/runs\/demo\/terminals\/([^/]+)$/.exec(p)
        if (m === 'DELETE' && del) {
          if (del[1] !== shell.id) return fail(res, 404, 'not_found', 'unknown terminal')
          shell.forget()
          res.statusCode = 204
          return res.end()
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
        if (match[1] !== shell.id) {
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
    configureServer: install,
    configurePreviewServer: install,
  }
}

// ---------------------------------------------------------------- fake shell

const PROMPT = '\x1b[38;5;208mastrona-cli\x1b[0m > '

const shell = {
  id: '',
  alive: false,
  createdAt: '',
  buffer: '',
  line: '',
  cols: 80,
  rows: 24,
  socket: null as Duplex | null,

  reset() {
    this.id = randomUUID().replace(/-/g, '')
    this.alive = true
    this.createdAt = new Date().toISOString()
    this.buffer = ''
    this.line = ''
    this.out('\x1b[32mMock terminal.\x1b[0m Try: stty size, ls /, echo hi, exit\r\n' + PROMPT)
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
    frame(socket, 1, Buffer.from(JSON.stringify({ type: 'attached', terminal: terminal() })))
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
