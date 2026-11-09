// One attachment to a server-held terminal over a WebSocket. The server owns
// the shell and its scrollback; closing this connection never ends the shell.
// On a drop it reconnects with backoff, fetching a fresh single-use ticket
// for every attempt.

import {
  CloseCode,
  MAX_INPUT_FRAME,
  getTicket,
  isUnauthorized,
  parseServerControl,
  pingFrame,
  resizeFrame,
  terminalSocketURL,
} from '../api'

export type LinkState =
  | { kind: 'connecting' }
  | { kind: 'connected' }
  | { kind: 'reconnecting'; attempt: number }
  | { kind: 'takenOver' }
  | { kind: 'ended'; exitCode?: number }
  | { kind: 'gone' }
  | { kind: 'unauthorized' }

export interface ConnectionEvents {
  onData(bytes: Uint8Array): void
  onState(state: LinkState): void
  /** Called on the server's "attached" frame; the scrollback replay follows. */
  onAttach(): void
}

const MAX_BACKOFF_MS = 10_000
/** Consecutive failed attempts before the terminal is reported gone. */
const MAX_ATTEMPTS_WITHOUT_OPEN = 8
const PING_INTERVAL_MS = 25_000

export class TerminalConnection {
  private ws: WebSocket | null = null
  private attempt = 0
  private timer: number | undefined
  private pinger: number | undefined
  private closed = false
  private size: { cols: number; rows: number } | null = null
  private readonly encoder = new TextEncoder()

  constructor(
    private readonly terminalId: string,
    private readonly events: ConnectionEvents,
  ) {}

  connect(): void {
    this.closed = false
    window.clearTimeout(this.timer)
    this.events.onState(this.attempt === 0 ? { kind: 'connecting' } : { kind: 'reconnecting', attempt: this.attempt })
    void this.open()
  }

  /** Sends keyboard input. Dropped while not attached. */
  sendInput(data: string): void {
    this.sendBinary(this.encoder.encode(data))
  }

  sendBinary(bytes: Uint8Array<ArrayBuffer>): void {
    const ws = this.ws
    if (ws?.readyState !== WebSocket.OPEN) return
    // A large paste is split: the server refuses input frames over 64 KiB.
    for (let i = 0; i < bytes.length; i += MAX_INPUT_FRAME) ws.send(bytes.subarray(i, i + MAX_INPUT_FRAME))
  }

  resize(cols: number, rows: number): void {
    this.size = { cols, rows }
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(resizeFrame(cols, rows))
  }

  /** Detaches. The shell keeps running on the server. */
  close(): void {
    this.closed = true
    window.clearTimeout(this.timer)
    window.clearInterval(this.pinger)
    const ws = this.ws
    this.ws = null
    ws?.close(1000, 'detached')
  }

  private async open(): Promise<void> {
    let ticket: string
    try {
      ticket = await getTicket()
    } catch (err) {
      if (isUnauthorized(err)) {
        this.closed = true
        this.events.onState({ kind: 'unauthorized' })
        return
      }
      this.retry()
      return
    }
    if (this.closed) return

    const ws = new WebSocket(terminalSocketURL(this.terminalId, ticket))
    ws.binaryType = 'arraybuffer'
    this.ws = ws
    let attached = false
    let outcome: LinkState | null = null
    let overrun = false

    ws.onopen = () => {
      window.clearInterval(this.pinger)
      this.pinger = window.setInterval(() => {
        if (ws.readyState === WebSocket.OPEN) ws.send(pingFrame())
      }, PING_INTERVAL_MS)
    }
    ws.onmessage = (ev: MessageEvent<ArrayBuffer | string>) => {
      if (typeof ev.data !== 'string') {
        if (attached) this.events.onData(new Uint8Array(ev.data))
        return
      }
      const msg = parseServerControl(ev.data)
      if (!msg) return
      switch (msg.type) {
        case 'attached':
          attached = true
          this.attempt = 0
          // Clear before the replay that follows, then size the shell.
          this.events.onAttach()
          this.events.onState({ kind: 'connected' })
          if (this.size) ws.send(resizeFrame(this.size.cols, this.size.rows))
          break
        case 'ended':
          outcome = { kind: 'ended', exitCode: msg.exitCode }
          break
        case 'takenOver':
          outcome = { kind: 'takenOver' }
          break
        case 'overrun':
          overrun = true
          break
        default:
          // pong and error: the connection stays open.
          break
      }
    }
    ws.onclose = (ev: CloseEvent) => {
      window.clearInterval(this.pinger)
      if (this.ws !== ws) return
      this.ws = null
      if (this.closed) return
      if (ev.code === CloseCode.takenOver) outcome = { kind: 'takenOver' }
      if (outcome) {
        this.closed = true
        this.events.onState(outcome)
        return
      }
      if (overrun || ev.code === CloseCode.overrun) {
        // Fell behind: reattach at once for a fresh replay.
        this.attempt = 0
        this.connect()
        return
      }
      if (!attached && this.attempt >= MAX_ATTEMPTS_WITHOUT_OPEN) {
        this.closed = true
        this.events.onState({ kind: 'gone' })
        return
      }
      this.retry()
    }
  }

  private retry(): void {
    if (this.closed) return
    this.attempt += 1
    this.events.onState({ kind: 'reconnecting', attempt: this.attempt })
    const delay = Math.min(MAX_BACKOFF_MS, 500 * 2 ** (this.attempt - 1))
    this.timer = window.setTimeout(() => void this.open(), delay)
  }
}
