import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  ApiError,
  streamConversation,
  type StreamHandlers,
  type StreamMeta,
} from './client.ts'

// These tests drive the real streamConversation over a controllable
// ReadableStream: they assert the promise result and the callback counts, not
// just how the parser frames bytes. fetch and sessionStorage are read at call
// time, so each test swaps them out before calling in.

const encoder = new TextEncoder()

const META =
  'event: meta\ndata: {"status":"ok","skill":"数学","citations":[],"tool_calls":[]}\n\n'
const DELTA_ONE = 'event: delta\ndata: {"text":"第一段"}\n\n'
const DELTA_TWO = 'event: delta\ndata: {"text":"第二段"}\n\n'

function doneFrame(messageID: number | string = 7): string {
  return `event: done\ndata: ${JSON.stringify({ message_id: messageID })}\n\n`
}

function errorFrame(message = '回答生成中断，请重试'): string {
  return `event: error\ndata: ${JSON.stringify({ message })}\n\n`
}

interface StreamHarness {
  response: Response
  state: { canceled: boolean }
  push(chunk: string | Uint8Array): void
  close(): void
  fail(err: unknown): void
}

// newStream returns a stream that stays open until the test closes or errors
// it. A request that never receives a valid terminal therefore hangs unless
// the implementation cancels it — which is exactly what the "no EOF wait"
// tests assert.
function newStream(): StreamHarness {
  const state = { canceled: false }
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c
    },
    cancel() {
      state.canceled = true
    },
  })
  return {
    response: new Response(body, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
    }),
    state,
    push(chunk) {
      controller.enqueue(typeof chunk === 'string' ? encoder.encode(chunk) : chunk)
    },
    close() {
      controller.close()
    },
    fail(err) {
      controller.error(err)
    },
  }
}

function stubFetch(response: Response | ((init?: RequestInit) => Response)): void {
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) =>
    typeof response === 'function' ? response(init) : response) as typeof fetch
}

function stubStorage(): void {
  const entries = new Map<string, string>()
  entries.set('campusclaw_token', 'test-token')
  globalThis.sessionStorage = {
    getItem: (key) => entries.get(key) ?? null,
    setItem: (key, value) => {
      entries.set(key, value)
    },
    removeItem: (key) => {
      entries.delete(key)
    },
    clear: () => {
      entries.clear()
    },
    key: (index) => [...entries.keys()][index] ?? null,
    get length() {
      return entries.size
    },
  }
}

interface Recorder {
  handlers: StreamHandlers
  metas: StreamMeta[]
  deltas: string[]
  dones: number[]
  errors: string[]
}

function recorder(): Recorder {
  const rec: Recorder = {
    metas: [],
    deltas: [],
    dones: [],
    errors: [],
    handlers: {
      onMeta: (meta) => {
        rec.metas.push(meta)
      },
      onDelta: (text) => {
        rec.deltas.push(text)
      },
      onDone: (messageID) => {
        rec.dones.push(messageID)
      },
      onError: (message) => {
        rec.errors.push(message)
      },
    },
  }
  return rec
}

// assertInterrupted checks the single generic interruption exit: an ApiError
// with status 0 (never a fake server status) and the fixed user text.
async function assertInterrupted(promise: Promise<void>): Promise<void> {
  let resolved = false
  let caught: unknown
  try {
    await promise
    resolved = true
  } catch (err) {
    caught = err
  }
  if (resolved) assert.fail('expected the stream to reject, but it resolved')
  assert.ok(caught instanceof ApiError, `expected ApiError, got ${String(caught)}`)
  assert.equal((caught as ApiError).status, 0)
  assert.equal((caught as ApiError).message, '回答生成中断，请重试')
}

function setup(): StreamHarness {
  const stream = newStream()
  stubFetch(stream.response)
  stubStorage()
  return stream
}

test('valid done completes once and ends consumption without waiting for EOF', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)
  stream.push(doneFrame(7))

  await streamConversation(1, '问题', rec.handlers)

  assert.deepEqual(rec.dones, [7])
  assert.deepEqual(rec.errors, [])
  assert.deepEqual(rec.deltas, ['第一段'])
  assert.equal(rec.metas.length, 1)
  assert.equal(stream.state.canceled, true)
})

test('duplicate terminals in one chunk are ignored after the first valid done', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)
  stream.push(DELTA_TWO + doneFrame(9) + doneFrame(10) + errorFrame('不该出现') + DELTA_ONE)

  await streamConversation(2, '问题', rec.handlers)

  assert.deepEqual(rec.dones, [9])
  assert.deepEqual(rec.errors, [])
  assert.deepEqual(rec.deltas, ['第一段', '第二段'])
  assert.equal(stream.state.canceled, true)
})

test('meta and partial deltas followed by EOF reject as interrupted', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)
  stream.push(DELTA_TWO)
  stream.close()

  await assertInterrupted(streamConversation(3, '问题', rec.handlers))

  assert.deepEqual(rec.dones, [])
  assert.deepEqual(rec.errors, [])
  assert.deepEqual(rec.deltas, ['第一段', '第二段'])
  assert.equal(rec.metas.length, 1)
})

const noTerminalCases: [string, string][] = [
  ['empty response', ''],
  ['comment only', ': keep-alive\n\n'],
  ['unknown event only', 'event: heartbeat\ndata: {"tick":1}\n\n'],
  ['meta only', META],
]

for (const [name, body] of noTerminalCases) {
  test(`EOF without a terminal rejects: ${name}`, { timeout: 2000 }, async () => {
    const stream = setup()
    const rec = recorder()
    if (body !== '') stream.push(body)
    stream.close()

    await assertInterrupted(streamConversation(4, '问题', rec.handlers))

    assert.deepEqual(rec.dones, [])
    assert.deepEqual(rec.errors, [])
  })
}

test('a valid error notifies once and EOF neither rejects nor notifies again', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)
  stream.push(errorFrame('回答生成中断，请重试') + errorFrame('重复错误') + doneFrame(11))
  stream.close()

  await streamConversation(5, '问题', rec.handlers)

  assert.deepEqual(rec.errors, ['回答生成中断，请重试'])
  assert.deepEqual(rec.dones, [])
  assert.deepEqual(rec.deltas, ['第一段'])
})

test('a done split at a CRLF boundary with no final blank line is accepted', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)

  const frame = encoder.encode('event: done\r\ndata: {"message_id":42}\r\n')
  const crlf = frame.indexOf(0x0d)
  stream.push(frame.slice(0, crlf + 1)) // ends with \r
  stream.push(frame.slice(crlf + 1, 20)) // starts with \n
  stream.push(frame.slice(20))
  stream.close()

  await streamConversation(6, '问题', rec.handlers)

  assert.deepEqual(rec.dones, [42])
  assert.deepEqual(rec.errors, [])
})

test('an error with a Chinese message split inside UTF-8 and at CRLF is accepted', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)

  const frame = encoder.encode('event: error\r\ndata: {"message":"回答生成中断，请重试"}\r\n')
  const insideChar = encoder.encode('event: error\r\ndata: {"message":"').length + 1 // inside 回
  const crlf = frame.indexOf(0x0d)
  const points = [...new Set([crlf + 1, insideChar, frame.length])].sort((a, b) => a - b)
  let prev = 0
  for (const point of points) {
    stream.push(frame.slice(prev, point))
    prev = point
  }
  stream.close()

  await streamConversation(7, '问题', rec.handlers)

  assert.deepEqual(rec.errors, ['回答生成中断，请重试'])
  assert.deepEqual(rec.dones, [])
})

const invalidTerminalCases: [string, string][] = [
  ['truncated done JSON', 'event: done\ndata: {"message_id":\n\n'],
  ['done without message_id', 'event: done\ndata: {}\n\n'],
  ['done with message_id 0', doneFrame(0)],
  ['done with negative message_id', 'event: done\ndata: {"message_id":-3}\n\n'],
  ['done with string message_id', doneFrame('7')],
  ['error without message', 'event: error\ndata: {}\n\n'],
  ['error with empty message', 'event: error\ndata: {"message":""}\n\n'],
]

for (const [name, frame] of invalidTerminalCases) {
  test(`invalid terminal rejects: ${name}`, { timeout: 2000 }, async () => {
    const stream = setup()
    const rec = recorder()
    stream.push(META)
    stream.push(DELTA_ONE)
    stream.push(frame)
    stream.close()

    await assertInterrupted(streamConversation(8, '问题', rec.handlers))

    assert.deepEqual(rec.dones, [])
    assert.deepEqual(rec.errors, [])
    assert.deepEqual(rec.deltas, ['第一段'])
  })
}

test('done before meta is not accepted', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(doneFrame(12))
  stream.close()

  await assertInterrupted(streamConversation(9, '问题', rec.handlers))

  assert.deepEqual(rec.dones, [])
  assert.deepEqual(rec.errors, [])
  assert.deepEqual(rec.metas, [])
})

test('a reader error rejects with the generic interruption, not the read error', { timeout: 2000 }, async () => {
  const stream = setup()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)

  const promise = streamConversation(10, '问题', rec.handlers)
  setTimeout(() => stream.fail(new Error('socket reset')), 0)

  await assertInterrupted(promise)

  assert.deepEqual(rec.dones, [])
  assert.deepEqual(rec.errors, [])
  assert.deepEqual(rec.deltas, ['第一段'])
})

test('an active abort stays an AbortError and is not reported as a missing terminal', { timeout: 2000 }, async () => {
  const stream = newStream()
  stubFetch((init) => {
    init?.signal?.addEventListener('abort', () => {
      stream.fail(new DOMException('The operation was aborted.', 'AbortError'))
    })
    return stream.response
  })
  stubStorage()
  const rec = recorder()
  stream.push(META)
  stream.push(DELTA_ONE)

  const controller = new AbortController()
  const promise = streamConversation(11, '问题', rec.handlers, controller.signal)
  setTimeout(() => controller.abort(), 0)

  await assert.rejects(promise, (err: unknown) => {
    assert.equal((err as { name?: string }).name, 'AbortError')
    return true
  })
  assert.deepEqual(rec.dones, [])
  assert.deepEqual(rec.errors, [])
  assert.deepEqual(rec.deltas, ['第一段'])
})
