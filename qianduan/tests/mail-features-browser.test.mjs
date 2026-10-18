import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, readFile, realpath, rm } from 'node:fs/promises'
import { basename, dirname, extname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { build } from 'vite'
import { connectDebugger, waitFor } from './helpers/browser.mjs'

const frontendRoot = fileURLToPath(new URL('../', import.meta.url))
const browserExecutable = [
  process.env.MAILPLUS_BROWSER_EXECUTABLE,
  join(process.env['PROGRAMFILES(X86)'] || '', 'Microsoft/Edge/Application/msedge.exe'),
  join(process.env.PROGRAMFILES || '', 'Google/Chrome/Application/chrome.exe'),
  '/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/google-chrome',
].find((path) => path && existsSync(path))

test('mail readers prefetch, prioritize, notify and cancel; renewal rendering matches Outlook', {
  timeout: 120_000,
  skip: !browserExecutable && 'Set MAILPLUS_BROWSER_EXECUTABLE to an Edge or Chromium executable',
}, async (t) => {
  const temporaryRoot = resolve(frontendRoot, 'tmp')
  await mkdir(temporaryRoot, { recursive: true })
  const testRoot = await mkdtemp(join(temporaryRoot, 'mail-features-browser-'))
  const webRoot = process.env.MAILPLUS_TEST_WEB_ROOT ? resolve(process.env.MAILPLUS_TEST_WEB_ROOT) : join(testRoot, 'web')
  let browser, cdp, server
  const calls = []
  const active = new Map(), peak = new Map()
  let recoverFailures = false
  t.after(async () => {
    for (const call of calls) if (!call.response.writableEnded) call.response.destroy()
    if (cdp) { await cdp.send('Browser.close').catch(() => {}); cdp.close() }
    if (browser && browser.exitCode === null) browser.kill()
    if (server) { server.closeAllConnections(); await new Promise((done) => server.close(done)) }
    const actual = await realpath(testRoot)
    if (dirname(actual) === await realpath(temporaryRoot) && basename(actual).startsWith('mail-features-browser-')) {
      await rm(actual, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 })
    }
  })
  if (!process.env.MAILPLUS_TEST_WEB_ROOT) {
    await build({ root: frontendRoot, logLevel: 'warn', build: { outDir: webRoot, emptyOutDir: false } })
  }
  const user = { id: 1, username: 'fixture-admin', role: 'admin', email: 'fixture@example.invalid', balance: 0, status: 'active', avatar_url: '' }
  const settings = { site_name: 'MailPlus fixture', site_logo: '/logo.png', table_default_page_size: 20, table_page_size_options: [10, 20, 50] }
  const accounts = [1, 2].map((id) => ({
    id, group_id: 2, group_name: 'Fixture', email: 'fixture-' + id + '@example.invalid',
    server_id: 0, server_name: 'Fixture', imap_host: 'imap.example.invalid', smtp_host: 'smtp.example.invalid',
    imap_protocol: 'IMAP', imap_port: 993, imap_ssl: true, smtp_protocol: 'SMTP', smtp_port: 465, smtp_ssl: true,
    created_at: '2020/01/01 01:02:03', last_renewed_at: id === 1 ? '2020/02/02 02:03:04' : '',
    status: 'normal', status_reason: '', remark: '',
  }))
  function mail(mode, id) {
    return {
      id: mode + ':' + id, source: mode, uid: id, uid_validity: 42, message_id: String(id),
      mailbox: 'INBOX', folder: 'inbox', subject: 'Message ' + id,
      from: 'sender@example.invalid', to: 'reader@example.invalid', time: '2020/02/02 02:03:04',
      timestamp: id, body: '', html: '', body_loaded: false,
    }
  }
  function sendJSON(response, data, status = 200, message = 'ok') {
    if (response.destroyed || response.writableEnded) return
    response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
    response.end(JSON.stringify({ code: status === 200 ? 0 : status, data, msg: message }))
  }
  function finish(call) {
    sendJSON(call.response, { ...mail(call.mode, call.id), body: 'Body ' + call.id, body_loaded: true })
  }
  server = createServer(async (request, response) => {
    try {
      const path = new URL(request.url, 'http://127.0.0.1').pathname
      if (path === '/favicon.ico') { response.writeHead(204); response.end(); return }
      if (path === '/fixture-blank') { response.writeHead(200, { 'Content-Type': 'text/html' }); response.end('<!doctype html><body>fixture</body>'); return }
      if (path === '/api/settings/bootstrap.js') {
        response.writeHead(200, { 'Content-Type': 'text/javascript' })
        response.end('window.__APP_PUBLIC_SETTINGS__=' + JSON.stringify(settings)); return
      }
      const quick = path.match(/^\/api\/admin\/quick-mail\/(imap|outlook)\/(receive|detail)$/)
      if (quick) {
        let raw = ''; for await (const chunk of request) raw += chunk
        const payload = JSON.parse(raw)
        const mode = quick[1]
        if (quick[2] === 'receive') {
          assert.equal(payload.include_body, false)
          return sendJSON(response, { inbox: [1, 2, 3, 4].map((id) => mail(mode, id)), trash: [] })
        }
        const id = Number(payload.uid || payload.message_id)
        const call = { mode, id, email: payload.email, response }
        calls.push(call)
        const count = (active.get(mode) || 0) + 1
        active.set(mode, count)
        peak.set(mode, Math.max(peak.get(mode) || 0, count))
        response.once('close', () => active.set(mode, active.get(mode) - 1))
        if (payload.email.startsWith('fail')) {
          if (id === 2 && !recoverFailures) return sendJSON(response, null, 400, 'fixture body timeout')
          return finish(call)
        }
        return
      }
      if (path.startsWith('/api/')) {
        request.resume()
        if (path === '/api/settings/public' || path === '/api/admin/settings') return sendJSON(response, settings)
        if (path === '/api/user/profile') return sendJSON(response, user)
        if (path === '/api/user/quick-mail-key') return sendJSON(response, { configured: false })
        if (path === '/api/admin/tasks' || path === '/api/admin/mail-servers') return sendJSON(response, [])
        if (path === '/api/admin/mail-groups' || path === '/api/admin/outlook-groups') return sendJSON(response, [{ id: 1, name: '全部邮箱', parent_id: 0, system: true }, { id: 2, name: 'Fixture', parent_id: 0, system: true }])
        if (path === '/api/admin/update-check') return sendJSON(response, { status: 'latest', current_version: 'v1.0.1' })
        const items = path === '/api/admin/outlook-accounts'
          ? accounts.map((item) => ({ ...item, client_id: 'fixture-client', last_token_refresh_at: item.last_renewed_at }))
          : accounts
        return sendJSON(response, { items, total: items.length, page: 1, pages: 1, page_size: 20, normal: 2, error: 0 })
      }
      let filename = resolve(webRoot, '.' + decodeURIComponent(path))
      if (!filename.startsWith(webRoot + sep) || !extname(filename)) filename = join(webRoot, 'index.html')
      const content = await readFile(filename)
      response.writeHead(200, { 'Content-Type': { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png' }[extname(filename)] || 'application/octet-stream' })
      response.end(content)
    } catch (error) {
      if (!response.destroyed && !response.writableEnded) { response.writeHead(500); response.end() }
      t.diagnostic(String(error))
    }
  })
  await new Promise((yes, no) => { server.once('error', no); server.listen(0, '127.0.0.1', yes) })
  const base = 'http://127.0.0.1:' + server.address().port
  const profile = join(testRoot, 'profile')
  browser = spawn(browserExecutable, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', '--user-data-dir=' + profile, 'about:blank'], { windowsHide: true, stdio: 'ignore' })
  await waitFor(() => existsSync(join(profile, 'DevToolsActivePort')), 'isolated browser')
  const port = (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0].trim()
  const info = await (await fetch('http://127.0.0.1:' + port + '/json/version')).json()
  cdp = await connectDebugger(info.webSocketDebuggerUrl)
  const run = (page, fn, args = null) => cdp.evaluate(page, '(' + fn.toString() + ')(' + JSON.stringify(args) + ')')
  const blank = await cdp.page(base + '/fixture-blank')
  await waitFor(() => run(blank, () => location.pathname === '/fixture-blank' && !!document.body), 'fixture')
  const session = { auth_token: 'fixture-login', refresh_token: 'fixture-refresh', auth_user: JSON.stringify(user), token_expires_at: String(Date.now() + 7200000), must_change_password: 'false' }
  await run(blank, (session) => { localStorage.setItem('auth_session_v1', JSON.stringify(session)) }, session)

  async function fetchMail(page, email) {
    await run(page, async (email) => {
      for (const [selector, value] of [['.quick-mail-email-field input', email], ['.quick-mail-limit-field input', '4']]) {
        const input = document.querySelector(selector)
        input.value = value
        input.dispatchEvent(new Event('input', { bubbles: true }))
      }
      await new Promise((done) => setTimeout(done, 0))
      document.querySelector('.quick-mail-query-card').requestSubmit()
    }, email)
  }
  for (const mode of ['imap', 'outlook']) {
    const page = await cdp.page(base + '/' + mode + '/mail/')
    await waitFor(() => run(page, () => !!document.querySelector('.quick-mail-fetch-button')), mode + ' reader')
    await fetchMail(page, mode + '@example.invalid')
    await waitFor(() => calls.filter((call) => call.mode === mode).length === 2, mode + ' first two bodies')
    await waitFor(() => run(page, () => document.querySelectorAll('.quick-mail-table tbody tr').length === 4), 'summary list')
    assert.deepEqual(calls.filter((call) => call.mode === mode).map((call) => call.id).sort(), [3, 4])
    await run(page, () => [...document.querySelectorAll('.quick-mail-table tbody tr')].find((row) => row.textContent.includes('Message 1')).click())
    finish(calls.find((call) => call.mode === mode && call.id === 4))
    await waitFor(() => calls.filter((call) => call.mode === mode).length === 3, 'clicked body priority')
    assert.equal(calls.filter((call) => call.mode === mode)[2].id, 1)
    finish(calls.find((call) => call.mode === mode && call.id === 3))
    await waitFor(() => calls.filter((call) => call.mode === mode).length === 4, 'next free worker')
    finish(calls.find((call) => call.mode === mode && call.id === 1))
    finish(calls.find((call) => call.mode === mode && call.id === 2))
    await waitFor(() => run(page, () => document.querySelector('[aria-live] .border-green-500')?.parentElement.textContent.includes('邮件正文已全部缓存完成')), 'green completion notification')
    await waitFor(() => run(page, () => document.querySelector('.quick-mail-detail-content iframe')?.srcdoc.includes('Body 1')), 'opened body')
    assert.equal(peak.get(mode), 2)
    assert.equal(calls.filter((call) => call.mode === mode).length, 4)
    await run(page, () => document.querySelector('.quick-mail-back-button').click())
    await run(page, () => [...document.querySelectorAll('.quick-mail-table tbody tr')].find((row) => row.textContent.includes('Message 1')).click())
    assert.equal(calls.filter((call) => call.mode === mode).length, 4, 'cached bodies must not be requested again')

    recoverFailures = false
    await fetchMail(page, 'fail-' + mode + '@example.invalid')
    await waitFor(() => run(page, () => document.querySelector('[aria-live] .border-yellow-500')?.textContent.includes('已缓存 3/4 封，1 封正文加载失败')), 'partial cache notification')
    const failures = calls.filter((call) => call.mode === mode && call.email.startsWith('fail') && call.id === 2)
    assert.equal(failures.length, 3)
    recoverFailures = true
    await run(page, () => [...document.querySelectorAll('.quick-mail-table tbody tr')].find((row) => row.textContent.includes('Message 2')).click())
    await waitFor(() => run(page, () => document.querySelector('.quick-mail-detail-content iframe')?.srcdoc.includes('Body 2')), 'manual retry')
    assert.equal(calls.filter((call) => call.mode === mode && call.email.startsWith('fail') && call.id === 2).length, 4)

    await fetchMail(page, 'cancel-' + mode + '@example.invalid')
    await waitFor(() => calls.filter((call) => call.mode === mode && call.email.startsWith('cancel')).length === 2, 'cancellable background requests')
    await run(page, () => {
      const input = document.querySelector('.quick-mail-email-field input')
      input.value = 'changed@example.invalid'
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await waitFor(() => calls.filter((call) => call.mode === mode && call.email.startsWith('cancel')).every((call) => call.response.destroyed), 'mailbox switch abort')
    assert.equal(await run(page, () => document.querySelector('.quick-mail-display-card').textContent.includes('Message ')), false)
  }

  const imapPage = await cdp.page(base + '/admin/accounts')
  const outlookPage = await cdp.page(base + '/admin/outlook-accounts')
  await waitFor(() => run(imapPage, () => !!document.querySelector('.mail-renewal-line')), 'IMAP renewal')
  await waitFor(() => run(outlookPage, () => !!document.querySelector('.outlook-token-refresh-line')), 'Outlook renewal')
  const styles = []
  for (const dark of [false, true]) {
    for (const page of [imapPage, outlookPage]) await run(page, (dark) => document.documentElement.classList.toggle('dark', dark), dark)
    const inspect = (selector) => {
      const line = document.querySelector(selector)
      const css = getComputedStyle(line)
      return { fontSize: css.fontSize, fontWeight: css.fontWeight, color: css.color, lineHeight: css.lineHeight }
    }
    const imap = await run(imapPage, inspect, '.mail-renewal-line')
    const outlook = await run(outlookPage, inspect, '.outlook-token-refresh-line')
    assert.deepEqual(imap, outlook, 'renewal styling differs between account pages')
    styles.push(imap)
  }
  assert.notEqual(styles[0].color, styles[1].color)
  const layout = await run(imapPage, () => {
    const renewal = document.querySelector('.mail-renewal-line')
    const created = renewal.previousElementSibling
    return {
      count: document.querySelectorAll('.mail-renewal-line').length,
      time: renewal.textContent.trim(),
      smaller: parseFloat(getComputedStyle(renewal).fontSize) < parseFloat(getComputedStyle(created).fontSize),
      below: renewal.getBoundingClientRect().top >= created.getBoundingClientRect().bottom,
      rowHeight: renewal.closest('tr').getBoundingClientRect().height,
      fits: renewal.scrollWidth <= renewal.clientWidth,
    }
  })
  assert.equal(layout.count, 1, 'accounts without renewal records must not display a renewal')
  assert.equal(layout.time, '续期: 2020/02/02 02:03:04')
  assert.equal(layout.smaller, true)
  assert.equal(layout.below, true)
  assert.equal(layout.fits, true)
  assert.ok(layout.rowHeight <= 75, 'a second time line broke the virtual row height')
  assert.deepEqual(cdp.exceptions, [])
  t.diagnostic(JSON.stringify({ renewal: styles, rowHeight: layout.rowHeight }))
})
