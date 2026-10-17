import test from 'node:test'
import assert from 'node:assert/strict'
import { useQuickMailReader } from '../src/composables/useQuickMailReader.ts'

const query = () => ({ mode: 'imap', email: 'fixture@example.invalid', limit: 50, admin_key: 'fixture' })
const message = (id, extra = {}) => ({
  id: String(id), source: 'imap', uid: id, uid_validity: 42, folder: 'inbox', mailbox: 'INBOX',
  subject: `Message ${id}`, from: 'sender@example.invalid', to: 'fixture@example.invalid',
  time: '', timestamp: id, body: '', html: '', body_loaded: false, ...extra,
})
const result = (messages) => ({ inbox: messages, trash: [] })
function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

test('50 summaries are usable without fetching any bodies; opened bodies are cached', async () => {
  const calls = []
  const reader = useQuickMailReader({
    receive: async () => result(Array.from({ length: 50 }, (_, i) => message(i + 1))),
    detail: async (mode, identity, item) => { calls.push({ mode, identity, item }); return { ...item, body: 'loaded' } },
  })
  await reader.receive(query())
  assert.equal(reader.messages.value.length, 50)
  assert.equal(calls.length, 0)
  await reader.select(reader.messages.value[12])
  assert.equal(calls.length, 1)
  assert.equal(reader.selectedMessage.value.body, 'loaded')
  reader.closeDetail()
  await reader.select(reader.messages.value[12])
  assert.equal(calls.length, 1)
  assert.equal(reader.messages.value.length, 50)
})

test('repeated clicks share one pending body request, including legitimately empty bodies', async () => {
  const body = deferred()
  let calls = 0
  const reader = useQuickMailReader({ receive: async () => result([message(1)]), detail: () => { calls++; return body.promise } })
  await reader.receive(query())
  const first = reader.select(reader.messages.value[0])
  const second = reader.select(reader.messages.value[0])
  body.resolve(message(1))
  await Promise.all([first, second])
  await reader.select(reader.messages.value[0])
  assert.equal(calls, 1)
  assert.equal(reader.selectedMessage.value.body_loaded, true)
  assert.equal(reader.detailLoading.value, false)
})

test('a failed body read keeps the list and can be retried', async () => {
  let calls = 0
  const reader = useQuickMailReader({
    receive: async () => result([message(1), message(2)]),
    detail: async () => { if (++calls === 1) throw new Error('fixture timeout'); return message(1, { body: 'retried' }) },
  })
  await reader.receive(query())
  await reader.select(reader.messages.value[0])
  assert.equal(reader.messages.value.length, 2)
  assert.match(reader.detailError.value, /timeout/)
  assert.equal(reader.selectedMessage.value.body_loaded, false)
  await reader.select(reader.selectedMessage.value)
  assert.equal(reader.detailError.value, '')
  assert.equal(reader.selectedMessage.value.body, 'retried')
  assert.equal(calls, 2)
})

test('cancelled and superseded list responses cannot replace a newer mailbox', async () => {
  const older = deferred()
  let calls = 0, firstSignal
  const reader = useQuickMailReader({
    receive: async (_mode, _identity, signal) => {
      if (++calls === 1) { firstSignal = signal; return older.promise }
      return result([message(2)])
    },
    detail: async () => { throw new Error('unexpected body request') },
  })
  const pending = reader.receive(query())
  reader.cancelReceive()
  assert.equal(firstSignal.aborted, true)
  await reader.receive({ ...query(), email: 'new@example.invalid' })
  older.resolve(result([message(1)]))
  assert.equal(await pending, null)
  assert.equal(reader.lastQueryEmail.value, 'new@example.invalid')
  assert.deepEqual(reader.messages.value.map((item) => item.id), ['2'])
  assert.equal(reader.fetching.value, false)
})

test('detail requests use the successful query snapshot rather than mutable form values', async () => {
  const pending = deferred()
  const form = query()
  let requestedIdentity
  const reader = useQuickMailReader({
    receive: () => pending.promise,
    detail: async (_mode, identity, item) => { requestedIdentity = identity; return item },
  })
  const receiving = reader.receive(form)
  form.email = 'different@example.invalid'
  form.admin_key = 'different-fixture'
  pending.resolve(result([message(1)]))
  await receiving
  await reader.select(reader.messages.value[0])
  assert.equal(reader.lastQueryEmail.value, 'fixture@example.invalid')
  assert.deepEqual(requestedIdentity, { email: 'fixture@example.invalid', admin_key: 'fixture' })
})

test('reset aborts pending detail reads and prevents old content reappearing', async () => {
  const body = deferred()
  let signal
  const reader = useQuickMailReader({
    receive: async () => result([message(1)]),
    detail: (_mode, _identity, _item, currentSignal) => { signal = currentSignal; return body.promise },
  })
  await reader.receive(query())
  const loading = reader.select(reader.messages.value[0])
  reader.reset()
  body.resolve(message(1, { body: 'stale content' }))
  await loading
  assert.equal(signal.aborted, true)
  assert.equal(reader.messages.value.length, 0)
  assert.equal(reader.selectedMessage.value, null)
  assert.equal(reader.lastQueryEmail.value, '')
  assert.equal(reader.detailLoading.value, false)
})

test('selecting a different message cancels the previous read and ignores its late result', async () => {
  const first = deferred(), second = deferred()
  let firstSignal
  const reader = useQuickMailReader({
    receive: async () => result([message(1), message(2)]),
    detail: (_mode, _identity, item, signal) => {
      if (item.id === '1') { firstSignal = signal; return first.promise }
      return second.promise
    },
  })
  await reader.receive(query())
  const loadingFirst = reader.select(reader.messages.value[0])
  const loadingSecond = reader.select(reader.messages.value[1])
  assert.equal(firstSignal.aborted, true)
  second.resolve(message(2, { body: 'second' }))
  await loadingSecond
  first.resolve(message(1, { body: 'late first' }))
  await loadingFirst
  assert.equal(reader.selectedMessage.value.id, '2')
  assert.equal(reader.selectedMessage.value.body, 'second')
  assert.equal(reader.messages.value[0].body_loaded, false)
})

test('failed refresh preserves previously obtained messages', async () => {
  let calls = 0
  const reader = useQuickMailReader({
    receive: async () => { if (++calls > 1) throw new Error('offline'); return result([message(1)]) },
    detail: async (_mode, _identity, item) => item,
  })
  await reader.receive(query())
  await assert.rejects(reader.receive(query()), /offline/)
  assert.equal(reader.messages.value.length, 1)
  assert.equal(reader.lastQueryEmail.value, 'fixture@example.invalid')
  assert.equal(reader.fetching.value, false)
})

test('older complete-response backends remain usable without the new detail route', async () => {
  let detailCalls = 0
  const reader = useQuickMailReader({
    receive: async () => result([message(1, { body_loaded: undefined, body: 'legacy body' })]),
    detail: async () => { detailCalls++; throw new Error('unsupported') },
  })
  await reader.receive(query())
  await reader.select(reader.messages.value[0])
  assert.equal(detailCalls, 0)
  assert.equal(reader.selectedMessage.value.body, 'legacy body')
})
