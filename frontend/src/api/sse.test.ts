import { test } from 'node:test'
import assert from 'node:assert/strict'

import { createSSEParser, type SSEEvent } from './sse.ts'

const encoder = new TextEncoder()

function collect() {
  const events: SSEEvent[] = []
  const parser = createSSEParser((event) => events.push(event))
  return { events, parser }
}

test('a frame split across three chunks is delivered once', () => {
  const { events, parser } = collect()

  parser.feed(encoder.encode('event: delta\nda'))
  parser.feed(encoder.encode('ta: {"text":'))
  parser.feed(encoder.encode('"你好"}\n\n'))
  parser.end()

  assert.deepEqual(events, [{ event: 'delta', data: '{"text":"你好"}' }])
})

test('two frames in one chunk are delivered in order', () => {
  const { events, parser } = collect()

  parser.feed(encoder.encode('event: delta\ndata: 一\n\nevent: done\ndata: {"message_id":7}\n\n'))

  assert.deepEqual(events, [
    { event: 'delta', data: '一' },
    { event: 'done', data: '{"message_id":7}' },
  ])
})

test('\\r\\n line endings work even when the pair is split across chunks', () => {
  const { events, parser } = collect()

  parser.feed(encoder.encode('event: meta\r'))
  parser.feed(encoder.encode('\ndata: {"skill":null}\r\n\r\n'))

  assert.deepEqual(events, [{ event: 'meta', data: '{"skill":null}' }])
})

test('multiple data lines in one frame join with a newline', () => {
  const { events, parser } = collect()

  parser.feed(encoder.encode('event: delta\ndata: 第一行\ndata: 第二行\n\n'))

  assert.deepEqual(events, [{ event: 'delta', data: '第一行\n第二行' }])
})

test('a multi-byte character split across chunks survives decoding', () => {
  const { events, parser } = collect()

  const bytes = encoder.encode('event: delta\ndata: 数学\n\n')
  parser.feed(bytes.slice(0, 21)) // cuts inside 数's UTF-8 bytes
  parser.feed(bytes.slice(21))
  parser.end()

  assert.deepEqual(events, [{ event: 'delta', data: '数学' }])
})

test('frames without data and comment lines are ignored', () => {
  const { events, parser } = collect()

  parser.feed(encoder.encode(': keep-alive comment\nevent: empty\n\nevent: delta\ndata: ok\n\n'))

  assert.deepEqual(events, [{ event: 'delta', data: 'ok' }])
})
