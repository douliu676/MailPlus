import test from 'node:test'
import assert from 'node:assert/strict'
import { setImmediate as tick } from 'node:timers/promises'
import { useQuickMailReader } from '../src/composables/useQuickMailReader.ts'

const query = (mode = 'imap') => ({ mode, email: 'fixture@example.invalid', limit: 50, admin_key: 'fixture' })
const message = (id, extra = {}) => ({
  id: String(id), source: 'imap', uid: id, uid_validity: 42, folder: 'inbox', mailbox: 'INBOX',
  subject: 'Message ' + id, from: 'sender@example.invalid', to: 'fixture@example.invalid',
  time: '', timestamp: id, body: '', html: '', body_loaded: false, ...extra,
})
const result = (messages) => ({
  inbox: messages.filter((item) => item.folder !== 'trash'),
  trash: messages.filter((item) => item.folder === 'trash'),
})
function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function controlled(items) {
  const fixture = { items, calls: [], notices: [], active: 0, peak: 0 }
  fixture.reader = useQuickMailReader({
    receive: async () => result(fixture.items),
    detail(mode, identity, item, signal) {
      const gate = deferred()
      fixture.active++
      fixture.peak = Math.max(fixture.peak, fixture.active)
      const call = {
        mode, identity, item, signal, settled: false,
        resolve(extra = {}) {
          assert.equal(call.settled, false)
          call.settled = true
          gate.resolve({ ...item, body: 'Body ' + item.id, ...extra })
        },
        reject() {
          assert.equal(call.settled, false)
          call.settled = true
          gate.reject(new Error('fixture timeout'))
        },
      }
      fixture.calls.push(call)
      return gate.promise.finally(() => { fixture.active-- })
    },
  }, { onCacheComplete: (notice) => fixture.notices.push(notice) })
  return fixture
}
async function drain(fixture, fail = () => false) {
  for (let step = 0; step < 200; step++) {
    const pending = fixture.calls.filter((call) => !call.settled)
    if (!pending.length) return
    for (const call of pending) {
      if (fail(call)) call.reject()
      else call.resolve()
    }
    await tick()
  }
  assert.fail('queue did not settle')
}

for (const mode of ['imap', 'outlook']) {
  test(mode + ': caches newest first with two workers, prioritizes clicks and fills each free slot', { timeout: 5000 }, async () => {
    const fixture = controlled(Array.from({ length: 50 }, (_, i) => message(i + 1, { source: mode })))
    const { reader, calls, notices } = fixture
    await reader.receive(query(mode))
    assert.equal(reader.messages.value.length, 50)
    assert.deepEqual(calls.map((call) => call.item.id), ['50', '49'])
    assert.equal(reader.messages.value.every((item) => !item.body_loaded), true)

    const opening = reader.select(reader.messages.value.find((item) => item.id === '13'))
    assert.equal(calls.length, 2, 'a click must not create a third worker')
    calls[0].resolve()
    await tick()
    assert.deepEqual(calls.map((call) => call.item.id), ['50', '49', '13'])
    calls[1].resolve()
    await tick()
    assert.equal(calls[3].item.id, '48', 'the other worker must not download message 13 again')
    calls[2].resolve()
    await opening
    assert.equal(reader.selectedMessage.value.body, 'Body 13')
    await drain(fixture)

    assert.equal(fixture.peak, 2)
    assert.equal(calls.length, 50)
    assert.equal(new Set(calls.map((call) => call.item.id)).size, 50)
    assert.equal(calls.every((call) => call.mode === mode), true)
    assert.deepEqual(reader.messages.value.map((item) => Number(item.id)), Array.from({ length: 50 }, (_, i) => 50 - i))
    assert.deepEqual(notices, [{ total: 50, cached: 50, failed: 0 }])
    reader.closeDetail()
    await reader.select(reader.messages.value.find((item) => item.id === '13'))
    assert.equal(calls.length, 50)
    assert.equal(reader.detailLoading.value, false)
  })
}

test('repeated clicks share the active request, including legitimately empty bodies', async () => {
  const fixture = controlled([message(1)])
  const { reader, calls } = fixture
  await reader.receive(query())
  const first = reader.select(reader.messages.value[0])
  const second = reader.select(reader.messages.value[0])
  calls[0].resolve({ body: '', html: '' })
  await Promise.all([first, second])
  await reader.select(reader.messages.value[0])
  assert.equal(calls.length, 1)
  assert.equal(reader.selectedMessage.value.body_loaded, true)
  assert.equal(reader.selectedMessage.value.body, '')
  assert.equal(reader.detailLoading.value, false)
})

test('clicking another mail or closing the viewer keeps background caching and cannot replace the selection', async () => {
  const fixture = controlled([message(1), message(2), message(3)])
  const { reader, calls } = fixture
  await reader.receive(query())
  const first = reader.select(reader.messages.value.find((item) => item.id === '3'))
  const second = reader.select(reader.messages.value.find((item) => item.id === '2'))
  calls[1].resolve()
  await second
  assert.equal(reader.selectedMessage.value.id, '2')
  calls[0].resolve()
  await first
  assert.equal(reader.selectedMessage.value.id, '2')
  assert.equal(reader.messages.value.find((item) => item.id === '3').body_loaded, true)
  reader.closeDetail()
  assert.equal(calls.every((call) => !call.signal.aborted), true)
  await drain(fixture)
  assert.equal(reader.selectedMessage.value, null)
  assert.deepEqual(fixture.notices, [{ total: 3, cached: 3, failed: 0 }])
})

test('failed reads go behind untouched mail, retry twice, and report partial completion once', { timeout: 5000 }, async () => {
  const fixture = controlled([message(1), message(2), message(3), message(4)])
  const { reader, calls, notices } = fixture
  await reader.receive(query())
  const opening = reader.select(reader.messages.value[0])
  calls[0].reject()
  await tick()
  assert.equal(calls[2].item.id, '2', 'a failure must not block the next mail')
  await drain(fixture, (call) => call.item.id === '4')
  await opening
  assert.equal(calls.filter((call) => call.item.id === '4').length, 3)
  assert.equal(calls.filter((call) => call.item.id !== '4').length, 3)
  assert.equal(reader.messages.value.length, 4)
  assert.match(reader.detailError.value, /timeout/)
  assert.equal(reader.detailLoading.value, false)
  assert.deepEqual(notices, [{ total: 4, cached: 3, failed: 1 }])

  const retry = reader.select(reader.selectedMessage.value)
  assert.equal(reader.detailError.value, '')
  assert.equal(calls.at(-1).item.id, '4')
  calls.at(-1).resolve()
  await retry
  await tick()
  assert.equal(reader.selectedMessage.value.body, 'Body 4')
  assert.equal(reader.messages.value.every((item) => item.body_loaded), true)
  assert.equal(notices.length, 1, 'manual retries must not repeat the batch completion notice')
})

test('a transient failure recovers within the automatic retry budget', async () => {
  const fixture = controlled([message(1), message(2), message(3)])
  await fixture.reader.receive(query())
  fixture.calls[0].reject()
  await tick()
  await drain(fixture)
  assert.equal(fixture.calls.filter((call) => call.item.id === '3').length, 2)
  assert.deepEqual(fixture.notices, [{ total: 3, cached: 3, failed: 0 }])
})

test('reset settles waiting clicks, aborts all workers, and ignores old bodies and notices', async () => {
  const fixture = controlled([message(1), message(2), message(3)])
  const { reader, calls, notices } = fixture
  await reader.receive(query())
  const opening = reader.select(reader.messages.value[2])
  reader.reset()
  await opening
  assert.equal(calls.every((call) => call.signal.aborted), true)
  assert.equal(reader.messages.value.length, 0)
  assert.equal(reader.lastQueryEmail.value, '')

  fixture.items = [message(1, { subject: 'New mailbox' })]
  await reader.receive({ ...query('outlook'), email: 'new@example.invalid', admin_key: 'new-fixture' })
  calls[0].resolve({ body: 'stale' })
  calls[1].reject()
  await tick()
  assert.equal(notices.length, 0)
  assert.equal(reader.messages.value[0].body_loaded, false)
  assert.equal(calls.length, 3, 'old queued work must never restart')
  assert.equal(calls[2].mode, 'outlook')
  assert.deepEqual(calls[2].identity, { email: 'new@example.invalid', admin_key: 'new-fixture' })
  calls[2].resolve({ body: 'New content' })
  await tick()
  assert.equal(reader.messages.value[0].body, 'New content')
  assert.equal(reader.messages.value[0].subject, 'New mailbox')
  assert.deepEqual(notices, [{ total: 1, cached: 1, failed: 0 }])
})

test('cancelled and superseded list responses cannot replace a newer mailbox', async () => {
  const older = deferred()
  let calls = 0, firstSignal
  const reader = useQuickMailReader({
    receive: async (_mode, _identity, signal) => {
      if (++calls === 1) { firstSignal = signal; return older.promise }
      return result([message(2, { body_loaded: true })])
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

test('all background and clicked reads retain the successful query snapshot', async () => {
  const pending = deferred()
  const form = query()
  const identities = []
  const reader = useQuickMailReader({
    receive: () => pending.promise,
    detail: async (_mode, identity, item) => { identities.push(identity); return item },
  })
  const receiving = reader.receive(form)
  form.email = 'different@example.invalid'
  form.admin_key = 'different-fixture'
  pending.resolve(result([message(1), message(2), message(3)]))
  await receiving
  await reader.select(reader.messages.value.find((item) => item.id === '1'))
  await tick()
  assert.equal(identities.length, 3)
  assert.equal(reader.lastQueryEmail.value, 'fixture@example.invalid')
  for (const identity of identities) assert.deepEqual(identity, { email: 'fixture@example.invalid', admin_key: 'fixture' })
})

test('failed refresh retains the old list but does not resume its cancelled background queue', async () => {
  const fixture = controlled([message(1), message(2), message(3)])
  const body = deferred()
  let calls = 0, signal, details = 0
  const reader = useQuickMailReader({
    receive: async () => { if (++calls > 1) throw new Error('offline'); return result(fixture.items) },
    detail: async (_mode, _identity, _item, currentSignal) => { details++; signal = currentSignal; return body.promise },
  })
  await reader.receive(query())
  await assert.rejects(reader.receive(query()), /offline/)
  assert.equal(signal.aborted, true)
  assert.equal(reader.messages.value.length, 3)
  assert.equal(reader.lastQueryEmail.value, 'fixture@example.invalid')
  assert.equal(reader.fetching.value, false)
  body.resolve(message(1, { body: 'late' }))
  await tick()
  assert.equal(details, 2)
  assert.equal(reader.messages.value.every((item) => !item.body_loaded), true)
})

test('cached-body backends skip detail requests and emit completion after the list', async () => {
  let detailCalls = 0
  const events = []
  const reader = useQuickMailReader({
    receive: async () => result([message(1, { body_loaded: undefined, body: 'legacy body' })]),
    detail: async () => { detailCalls++; throw new Error('unsupported') },
  }, { onCacheComplete: () => events.push('complete') })
  await reader.receive(query())
  events.push('list')
  await reader.select(reader.messages.value[0])
  await tick()
  assert.equal(detailCalls, 0)
  assert.equal(reader.selectedMessage.value.body, 'legacy body')
  assert.deepEqual(events, ['list', 'complete'])
})

test('empty results and a reset before the completion notice do not notify', async () => {
  for (const items of [[], [message(1, { body_loaded: true })]]) {
    const fixture = controlled(items)
    await fixture.reader.receive(query())
    fixture.reader.reset()
    await tick()
    assert.equal(fixture.notices.length, 0)
    assert.equal(fixture.calls.length, 0)
  }
})

test('inbox and trash share the two-worker queue while keeping distinct message identities', async () => {
  const fixture = controlled([
    message(7, { id: 'inbox:7', timestamp: 10 }),
    message(7, { id: 'trash:7', timestamp: 30, folder: 'trash', mailbox: 'Junk' }),
    message(8, { id: 'inbox:8', timestamp: 20 }),
  ])
  await fixture.reader.receive(query())
  assert.deepEqual(fixture.calls.map((call) => call.item.id), ['trash:7', 'inbox:8'])
  await drain(fixture)
  assert.equal(fixture.peak, 2)
  assert.equal(fixture.reader.messages.value.find((item) => item.id === 'trash:7').folder, 'trash')
  assert.equal(fixture.reader.messages.value.find((item) => item.id === 'inbox:7').body, 'Body inbox:7')
  assert.deepEqual(fixture.notices, [{ total: 3, cached: 3, failed: 0 }])
})
