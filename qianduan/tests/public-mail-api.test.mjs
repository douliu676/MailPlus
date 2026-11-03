import test from 'node:test'
import assert from 'node:assert/strict'
import { getPublicMailInfo, getPublicMailSendStatus, newPublicMailRequestID, PublicMailApiError, sendPublicMailMessage } from '../src/api/publicMail.ts'

const payload = { request_id: 'fixture-request-00000001', recipient: 'recipient@example.invalid', nickname: '', subject: '', body: '' }
const ok = (data) => new Response(JSON.stringify({ code: 0, data }), { status: 200 })
function replaceFetch(t, implementation) {
  const saved = globalThis.fetch
  globalThis.fetch = implementation
  t.after(() => { globalThis.fetch = saved })
}

test('card send uses a stable request identifier and accepts all optional fields empty', async (t) => {
  replaceFetch(t, async (url, init) => {
    assert.equal(url, '/api/public/mail/keys%3Dfixture-card/send')
    assert.equal(init.method, 'POST')
    assert.deepEqual(JSON.parse(init.body), payload)
    return ok({ status: 'success', used_count: 1, remaining: 0 })
  })
  assert.equal((await sendPublicMailMessage('fixture-card', payload)).remaining, 0)
})

test('explicit failures retain the server quota and result for the notification', async (t) => {
  const data = { status: 'failed', used_count: 0, remaining: 1, reserved_count: 0 }
  replaceFetch(t, async () => new Response(JSON.stringify({ code: 400, msg: '发送失败，未扣次数', data }), { status: 400 }))
  await assert.rejects(sendPublicMailMessage('fixture-card', payload), (error) => {
    assert.ok(error instanceof PublicMailApiError)
    assert.deepEqual(error.data, data)
    return true
  })
})

test('ambiguous delivery retains its reservation and is never converted into success', async (t) => {
  replaceFetch(t, async () => ok({ status: 'unknown', used_count: 0, reserved_count: 1, remaining: 0 }))
  const result = await sendPublicMailMessage('fixture-card', payload)
  assert.equal(result.status, 'unknown')
  assert.equal(result.reserved_count, 1)
})

test('network failures do not automatically repeat a send', async (t) => {
  let calls = 0
  replaceFetch(t, async () => { calls++; throw new TypeError('fixture response lost') })
  await assert.rejects(sendPublicMailMessage('fixture-card', payload), TypeError)
  assert.equal(calls, 1)
})

test('status recovery is a read request scoped to the original card and request', async (t) => {
  replaceFetch(t, async (url, init) => {
    assert.equal(url, '/api/public/mail/keys%3Dfixture-card/send/fixture-request-00000001')
    assert.equal(init.method, undefined)
    assert.equal(init.body, undefined)
    return ok({ status: 'success', replayed: true })
  })
  assert.equal((await getPublicMailSendStatus('keys=fixture-card', payload.request_id)).status, 'success')
})

test('legacy card metadata without the additive fields remains readable', async (t) => {
  const legacy = { key: 'fixture-card', usage_limit: 5, used_count: 2, remaining: 3 }
  replaceFetch(t, async () => ok(legacy))
  assert.deepEqual(await getPublicMailInfo('fixture-card'), legacy)
})

test('request identifiers work without secure-context-only crypto.randomUUID', () => {
  const first = newPublicMailRequestID(), second = newPublicMailRequestID()
  assert.match(first, /^[a-f0-9]{32}$/)
  assert.notEqual(first, second)
})
