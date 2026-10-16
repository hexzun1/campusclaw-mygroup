import { test } from 'node:test'
import assert from 'node:assert/strict'

import { login } from './client.ts'

// The client reads sessionStorage and fetch at call time, so this test swaps
// both out before calling in; no import-time hooks are involved.
function stubEnvironment(storedToken: string | null): { url: string; headers: Headers }[] {
  const calls: { url: string; headers: Headers }[] = []

  const fetchStub: typeof fetch = async (input, init) => {
    calls.push({ url: String(input), headers: new Headers(init?.headers) })
    return new Response(JSON.stringify({ token: 'new-token', username: 'teacher_a', role: 'teacher' }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }
  globalThis.fetch = fetchStub

  const entries = new Map<string, string>()
  if (storedToken !== null) entries.set('campusclaw_token', storedToken)
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

  return calls
}

test('login carries the already stored token as a Bearer header', async () => {
  const calls = stubEnvironment('old-token')

  await login('teacher_a', 'pw')

  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, '/api/login')
  assert.equal(calls[0].headers.get('Authorization'), 'Bearer old-token')
})

test('login without a stored token sends no Authorization header', async () => {
  const calls = stubEnvironment(null)

  await login('teacher_a', 'pw')

  assert.equal(calls.length, 1)
  assert.equal(calls[0].headers.get('Authorization'), null)
})
