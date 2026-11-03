const test = require('node:test')
const assert = require('node:assert/strict')
const http = require('node:http')
const { spawn } = require('node:child_process')
const { once } = require('node:events')
const path = require('node:path')
const { setTimeout: delay } = require('node:timers/promises')

async function fixture(t, overrides = {}) {
  const closed = []
  const backend = http.createServer(async (req, res) => {
    let body = ''
    for await (const chunk of req) body += chunk
    res.on('close', () => {
      if (!res.writableFinished) closed.push(req.url)
    })
    await delay(400)
    if (res.destroyed) return
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ path: req.url, method: req.method, body }))
  })
  backend.listen(0, '127.0.0.1')
  await once(backend, 'listening')
  const reservation = http.createServer()
  reservation.listen(0, '127.0.0.1')
  await once(reservation, 'listening')
  const port = reservation.address().port
  await new Promise((resolve) => reservation.close(resolve))
  const env = { ...process.env }
  for (const key of ['FRONTEND_REQUEST_TIMEOUT_MS', 'FRONTEND_PUBLIC_MAIL_REQUEST_TIMEOUT_MS', 'FRONTEND_QUICK_MAIL_REQUEST_TIMEOUT_MS']) delete env[key]
  Object.assign(env, {
    FRONTEND_HOST: '127.0.0.1', FRONTEND_PORT: String(port),
    BACKEND_URL: `http://127.0.0.1:${backend.address().port}`,
    FRONTEND_REQUEST_TIMEOUT_MS: '120',
    FRONTEND_PUBLIC_MAIL_REQUEST_TIMEOUT_MS: '2000',
    FRONTEND_QUICK_MAIL_REQUEST_TIMEOUT_MS: '2000',
  }, overrides)
  for (const [key, value] of Object.entries(env)) if (value === undefined) delete env[key]
  const child = spawn(process.execPath, [path.join(__dirname, 'frontend-server.js')], {
    env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
  })
  let output = ''
  child.stdout.on('data', (chunk) => { output += chunk })
  child.stderr.on('data', (chunk) => { output += chunk })
  t.after(async () => {
    if (child.exitCode === null) {
      const exited = once(child, 'exit')
      child.kill()
      await exited
    }
    backend.closeAllConnections()
    await new Promise((resolve) => backend.close(resolve))
  })
  const deadline = Date.now() + 10_000
  while (!output.includes('frontend listening on')) {
    if (child.exitCode !== null || Date.now() > deadline) throw new Error('Proxy failed to start: ' + output)
    await delay(20)
  }
  return { base: `http://127.0.0.1:${port}`, closed }
}

test('card send and receive wait for the configured operation timeout through all entry points', async (t) => {
  const { base } = await fixture(t)
  const paths = [
    ['/api/public/mail/keys=fixture-card/send', 'POST'],
    ['/api/public/mail/keys=fixture-card/messages', 'POST'],
    ['/api/public/mail/keys=fixture-card/all', 'GET'],
    ['/api/public/mail/keys=fixture-card/all/sender%40example.invalid', 'GET'],
    ['/mail/keys=fixture-card/all', 'GET'],
    ['/mail/keys=fixture-card/all/sender%40example.invalid', 'GET'],
  ]
  for (const [route, method] of paths) {
    const body = JSON.stringify({ request_id: 'synthetic-proxy-request', recipient: 'recipient@example.invalid' })
    const response = await fetch(base + route + '?fixture=1', { method, ...(method === 'POST' ? { body, headers: { 'content-type': 'application/json' } } : {}) })
    assert.equal(response.status, 200, route)
    const result = await response.json()
    assert.equal(result.path, route.replace(/^\/mail\//, '/api/public/mail/') + '?fixture=1')
    assert.equal(result.method, method)
    if (method === 'POST') assert.equal(result.body, body)
  }
})

test('ordinary APIs and send status polling retain the general timeout', async (t) => {
  const { base } = await fixture(t)
  for (const route of ['/api/settings/public', '/api/public/mail/keys=fixture-card', '/api/public/mail/keys=fixture-card/send/synthetic-proxy-request']) {
    const response = await fetch(base + route)
    assert.equal(response.status, 502, route)
    assert.match((await response.json()).msg, /backend request timeout/)
  }
})

test('an explicit public operation timeout is enforced', async (t) => {
  const { base } = await fixture(t, { FRONTEND_PUBLIC_MAIL_REQUEST_TIMEOUT_MS: '120' })
  const response = await fetch(base + '/api/public/mail/keys=fixture-card/send', { method: 'POST', body: '{}' })
  assert.equal(response.status, 502)
  assert.match((await response.json()).msg, /backend request timeout/)
})

test('the global override also applies to public operations when their override is absent', async (t) => {
  const { base } = await fixture(t, { FRONTEND_PUBLIC_MAIL_REQUEST_TIMEOUT_MS: undefined })
  const response = await fetch(base + '/api/public/mail/keys=fixture-card/messages', { method: 'POST', body: '{}' })
  assert.equal(response.status, 502)
})

test('public operations use the long default when no timeout overrides are set', async (t) => {
  const { base } = await fixture(t, { FRONTEND_REQUEST_TIMEOUT_MS: undefined, FRONTEND_PUBLIC_MAIL_REQUEST_TIMEOUT_MS: undefined })
  const response = await fetch(base + '/api/public/mail/keys=fixture-card/send', { method: 'POST', body: '{}' })
  assert.equal(response.status, 200)
})

test('the existing quick mail forwarding timeout is preserved', async (t) => {
  const { base } = await fixture(t)
  for (const route of ['/api/admin/quick-mail/imap/receive', '/api/admin/quick-mail/outlook/detail', '/imap/mail/fixture/all/sender%40example.invalid']) {
    const response = await fetch(base + route)
    assert.equal(response.status, 200, route)
  }
})

test('client cancellation closes the public operation proxy request', async (t) => {
  const { base, closed } = await fixture(t)
  const route = '/api/public/mail/keys=fixture-card/send'
  const controller = new AbortController()
  const request = fetch(base + route, { method: 'POST', body: '{}', signal: controller.signal })
  await delay(80)
  controller.abort()
  await assert.rejects(request, { name: 'AbortError' })
  const deadline = Date.now() + 2000
  while (!closed.includes(route) && Date.now() < deadline) await delay(20)
  assert.ok(closed.includes(route), 'the proxy must release the upstream connection')
})
