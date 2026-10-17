import test from 'node:test'
import assert from 'node:assert/strict'
import { receiveQuickMail, receiveQuickMailDetail } from '../src/api/quickMail.ts'

const identity = { email: 'fixture@example.invalid', admin_key: 'fixture' }
const summary = { id: 'fixture', source: 'imap', uid: 7, uid_validity: 42, mailbox: 'INBOX', folder: 'inbox' }
const ok = (data) => new Response(JSON.stringify({ code: 0, data }), { status: 200 })

function replaceFetch(t, implementation) {
  const original = globalThis.fetch
  globalThis.fetch = implementation
  t.after(() => { globalThis.fetch = original })
}

test('list requests explicitly ask for summaries and preserve the requested count', async (t) => {
  replaceFetch(t, async (url, init) => {
    assert.equal(url, '/api/admin/quick-mail/imap/receive')
    assert.deepEqual(JSON.parse(init.body), { ...identity, limit: 50, include_body: false })
    return ok({ inbox: [], trash: [] })
  })
  await receiveQuickMail('imap', { ...identity, limit: 50 })
})

test('details retain mailbox identity and use POST for the existing authorization key', async (t) => {
  replaceFetch(t, async (url, init) => {
    assert.equal(url, '/api/admin/quick-mail/imap/detail')
    assert.equal(init.method, 'POST')
    assert.deepEqual(JSON.parse(init.body), { ...identity, uid: 7, uid_validity: 42, mailbox: 'INBOX', folder: 'inbox' })
    return ok({ ...summary, body: 'body', body_loaded: true })
  })
  const response = await receiveQuickMailDetail('imap', identity, summary)
  assert.equal(response.body_loaded, true)
})

test('a pre-cancelled request does not reach the network', async (t) => {
  let calls = 0
  replaceFetch(t, async () => { calls++; return ok({}) })
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(receiveQuickMail('imap', { ...identity, limit: 50 }, controller.signal), { name: 'AbortError' })
  assert.equal(calls, 0)
})

test('caller cancellation aborts an in-flight network request', async (t) => {
  let networkSignal
  replaceFetch(t, (_url, init) => new Promise((_resolve, reject) => {
    networkSignal = init.signal
    init.signal.addEventListener('abort', () => reject(init.signal.reason), { once: true })
  }))
  const controller = new AbortController()
  const pending = receiveQuickMail('imap', { ...identity, limit: 50 }, controller.signal)
  controller.abort()
  await assert.rejects(pending, { name: 'AbortError' })
  assert.equal(networkSignal.aborted, true)
})

test('the browser deadline also bounds a stalled response body', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let bodyStarted
  const started = new Promise((resolve) => { bodyStarted = resolve })
  replaceFetch(t, async (_url, init) => ({
    ok: true, status: 200,
    json: () => new Promise((_resolve, reject) => {
      bodyStarted()
      init.signal.addEventListener('abort', () => reject(init.signal.reason), { once: true })
    }),
  }))
  const pending = receiveQuickMail('imap', { ...identity, limit: 50 })
  await started
  t.mock.timers.tick(120_000)
  await assert.rejects(pending, /读取邮件超时/)
})

test('the existing proxy timeout is explained in Chinese', async (t) => {
  replaceFetch(t, async () => new Response(JSON.stringify({ code: 503, msg: 'Backend service is temporarily unavailable: backend request timeout' }), { status: 503 }))
  await assert.rejects(receiveQuickMail('imap', { ...identity, limit: 50 }), /读取邮件超时/)
})
