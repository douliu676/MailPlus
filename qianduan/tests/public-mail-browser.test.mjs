import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises'
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

test('public card composer, quota, cross-page busy state, recovery and operation logs', {
  timeout: 180_000,
  skip: !browserExecutable && 'Set MAILPLUS_BROWSER_EXECUTABLE to an Edge or Chromium executable',
}, async (t) => {
  const temporaryRoot = resolve(frontendRoot, 'tmp')
  await mkdir(temporaryRoot, { recursive: true })
  const testRoot = await mkdtemp(join(temporaryRoot, 'public-mail-browser-'))
  const webRoot = process.env.MAILPLUS_TEST_WEB_ROOT ? resolve(process.env.MAILPLUS_TEST_WEB_ROOT) : join(testRoot, 'web')
  let browser, cdp, server
  const sends = [], reads = [], operations = new Map()
  const card = { key: 'fixture-card', status: 'unused', bound_email: 'sender@example.invalid', has_bound_email: true, usage_limit: 6, used_count: 0, reserved_count: 0, busy: false, operation: '', mail_days: 0, mail_keyword: '', cooldown_seconds: 1 }
  let dropNextSend = false
  const user = { id: 1, username: 'fixture-admin', role: 'admin', email: 'admin@example.invalid', balance: 0, status: 'active', avatar_url: '' }
  const settings = { site_name: 'MailPlus', site_logo: '/logo.png', table_default_page_size: 20, table_page_size_options: [10, 20, 50], card_key_log_cleanup_days: '' }
  const logs = [{ id: 1, card_key: card.key, bound_email: card.bound_email, mail_subject: 'Legacy message', user_ip: '192.0.2.1', used_at: '2020/01/02 03:04:05' }]
  const snapshot = () => ({ ...card, remaining: card.usage_limit - card.used_count - card.reserved_count })
  const result = (op) => ({ ...snapshot(), ...op.payload, operation_id: op.id, status: op.status, charged: op.status === 'success', replayed: false,
    message: op.status === 'success' ? `发送成功，扣除 1 次，剩余 ${snapshot().remaining} 次` : op.status === 'failed' ? '发送失败，未扣次数：fixture rejection' : op.status === 'unknown' ? '发送结果待确认，已预留 1 次，请勿重复发送' : '正在发送邮件，请勿重复提交', error: op.status === 'failed' ? 'fixture rejection' : '' })
  function json(response, data, status = 200, message = 'ok') {
    if (response.destroyed || response.writableEnded) return
    response.writeHead(status, { 'Content-Type': 'application/json' })
    response.end(JSON.stringify({ code: status >= 400 ? status : 0, msg: message, data }))
  }
  function finishSend(op, status) {
    if (op.status !== 'running' && op.status !== 'unknown') throw new Error('fixture settled twice')
    card.busy = false; card.operation = ''
    if (status !== 'unknown') card.reserved_count--
    if (status === 'success') card.used_count++
    op.status = status
    op.log.operation_status = status
    op.log.error_message = status === 'failed' ? 'fixture rejection' : ''
    const data = result(op)
    json(op.response, data, status === 'failed' ? 400 : 200, data.message)
  }
  t.after(async () => {
    for (const op of sends) if (!op.response.writableEnded) op.response.destroy()
    for (const response of reads) if (!response.writableEnded) response.destroy()
    if (cdp) { await cdp.send('Browser.close').catch(() => {}); cdp.close() }
    if (browser && browser.exitCode === null) browser.kill()
    if (server) { server.closeAllConnections(); await new Promise((done) => server.close(done)) }
    const actual = await realpath(testRoot)
    if (dirname(actual) === await realpath(temporaryRoot) && basename(actual).startsWith('public-mail-browser-')) {
      await rm(actual, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 })
    }
  })
  if (!process.env.MAILPLUS_TEST_WEB_ROOT) await build({ root: frontendRoot, logLevel: 'warn', build: { outDir: webRoot, emptyOutDir: false } })
  server = createServer(async (request, response) => {
    try {
      const path = new URL(request.url, 'http://fixture').pathname
      if (path === '/api/settings/bootstrap.js') {
        response.writeHead(200, { 'Content-Type': 'text/javascript' })
        response.end('window.__APP_PUBLIC_SETTINGS__=' + JSON.stringify(settings)); return
      }
      if (path === '/favicon.ico') { response.writeHead(204); response.end(); return }
      if (path === '/fixture-blank') { response.writeHead(200, { 'Content-Type': 'text/html' }); response.end('<!doctype html><title>Fixture</title>'); return }
      if (path.startsWith('/api/public/mail/')) {
        if (request.method === 'GET' && /\/send\//.test(path)) {
          const op = operations.get(decodeURIComponent(path.split('/').pop()))
          return op ? json(response, result(op)) : json(response, {}, 404, '发件请求尚未登记')
        }
        if (request.method === 'GET') return json(response, snapshot())
        let raw = ''
        for await (const chunk of request) raw += chunk
        const payload = JSON.parse(raw || '{}')
        if (path.endsWith('/send') && operations.has(payload.request_id)) return json(response, { ...result(operations.get(payload.request_id)), replayed: true })
        if (card.busy) return json(response, snapshot(), 409, '该卡密正在处理邮件，请稍后重试')
        if (path.endsWith('/messages')) {
          card.busy = true; card.operation = 'receive'; reads.push(response); return
        }
        if (path.endsWith('/send')) {
          card.busy = true; card.operation = 'send'; card.reserved_count++
          const op = { id: `fixture-op-${sends.length + 1}`, payload, status: 'running', response }
          op.log = { id: logs.length + 1, card_key: card.key, bound_email: card.bound_email, mail_subject: payload.subject, user_ip: '192.0.2.1', used_at: '2020/01/02 03:04:05', operation_type: 'send', operation_status: 'running', recipient: payload.recipient, operation_id: op.id }
          sends.push(op); operations.set(payload.request_id, op); logs.push(op.log)
          if (dropNextSend) { dropNextSend = false; response.destroy() }
          return
        }
      }
      if (path.startsWith('/api/')) {
        if (path.includes('/card-key-mail-operations/') && path.endsWith('/resolve')) {
          let raw = ''; for await (const chunk of request) raw += chunk
          const payload = JSON.parse(raw)
          const op = sends.find((item) => path.includes('/' + item.id + '/'))
          finishSend(op, payload.status)
          return json(response, result(op))
        }
        request.resume()
        if (path === '/api/settings/public' || path === '/api/admin/settings') return json(response, settings)
        if (path === '/api/user/profile') return json(response, user)
        if (path === '/api/admin/card-key-logs') return json(response, { items: logs, total: logs.length, page: 1, pages: 1, page_size: 20 })
        if (path === '/api/admin/update-check') return json(response, { status: 'latest', current_version: 'fixture' })
        return json(response, [])
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
  cdp = await connectDebugger((await (await fetch('http://127.0.0.1:' + port + '/json/version')).json()).webSocketDebuggerUrl)
  const run = (page, fn, args = null) => cdp.evaluate(page, '(' + fn.toString() + ')(' + JSON.stringify(args) + ')')
  const page = await cdp.page(base + '/mail/keys=fixture-card')
  await waitFor(() => run(page, () => !!document.querySelector('.public-mail-send')), 'public card page')
  assert.deepEqual(await run(page, () => [...document.querySelectorAll('.public-mail-actions button')].map((button) => button.textContent.trim())), ['复制邮箱', '获取邮件', '发送邮件'])

  async function openComposer(values = {}) {
    await run(page, () => document.querySelector('.public-mail-send').click())
    await waitFor(() => run(page, () => !!document.querySelector('[role=dialog][aria-label="发送邮件"]')), 'composer')
    await run(page, async (values) => {
      for (const [name, value] of Object.entries(values)) {
        const field = document.querySelector(`[role=dialog] [name="${name}"]`)
        field.value = value; field.dispatchEvent(new Event('input', { bubbles: true }))
      }
      await new Promise((done) => setTimeout(done, 0))
    }, values)
  }
  async function submit() {
    await run(page, () => document.querySelector('.mail-send-modal button[type=submit]').click())
  }
  async function capture(page, filename) {
    if (!process.env.MAILPLUS_TEST_SCREENSHOT_DIR) return
    await run(page, async () => {
      await document.fonts.ready
      await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)))
      await Promise.all(document.getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
        .map((animation) => animation.finished.catch(() => {})))
    })
    await mkdir(process.env.MAILPLUS_TEST_SCREENSHOT_DIR, { recursive: true })
    const result = await cdp.send('Page.captureScreenshot', { format: 'png' }, page)
    await writeFile(join(process.env.MAILPLUS_TEST_SCREENSHOT_DIR, filename), Buffer.from(result.data, 'base64'))
  }
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, page)
  await openComposer({ recipient: 'recipient@example.invalid' })
  assert.equal(sends.length, 0, 'opening the composer submitted mail')
  assert.deepEqual(await run(page, () => [...document.querySelectorAll('.mail-send-modal input, .mail-send-modal textarea')].map((field) => [field.name, field.value])), [['nickname', ''], ['recipient', 'recipient@example.invalid'], ['subject', ''], ['body', '']])
  await run(page, () => { if (document.documentElement.classList.contains('dark')) document.querySelector('.public-mail-theme-button').click() })
  await waitFor(() => run(page, () => !document.documentElement.classList.contains('dark')), 'light theme')
  await capture(page, 'public-mail-compose-light.png')
  await run(page, () => document.querySelector('.public-mail-theme-button').click())
  await capture(page, 'public-mail-compose-dark.png')
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true }, page)
  await capture(page, 'public-mail-compose-mobile.png')
  const geometry = await run(page, () => {
    const dialog = document.querySelector('.mail-send-modal').getBoundingClientRect()
    const actions = [...document.querySelectorAll('.public-mail-actions button')].map((button) => button.getBoundingClientRect())
    return { fits: dialog.left >= 0 && dialog.right <= innerWidth && dialog.top >= 0 && dialog.bottom <= innerHeight,
      buttonsFit: actions.every((rect) => rect.left >= 0 && rect.right <= innerWidth) }
  })
  assert.equal(geometry.fits, true); assert.equal(geometry.buttonsFit, true)
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, page)

  await submit()
  await waitFor(() => sends.length === 1, 'first send')
  await run(page, () => { document.querySelector('.mail-send-modal').requestSubmit(); document.querySelector('.public-mail-fetch:not(.public-mail-send)').click() })
  assert.equal(sends.length, 1); assert.equal(reads.length, 0)
  assert.equal(await run(page, () => document.querySelector('.public-mail-send').disabled && document.querySelector('.public-mail-fetch').disabled), true)
  await run(page, () => document.querySelector('[aria-label="关闭发件框"]').click())
  assert.equal(await run(page, () => document.querySelector('.public-mail-send').disabled), true, 'closing the modal unlocked the card')
  const other = await cdp.page(base + '/mail/keys=fixture-card')
  await waitFor(() => run(other, () => document.querySelector('.public-mail-send')?.disabled === true), 'other tab busy state')
  finishSend(sends[0], 'success')
  await waitFor(() => run(page, () => document.querySelector('[aria-live] .border-green-500')?.parentElement.textContent.includes('剩余 5 次')), 'success quota notice')
  await waitFor(() => run(other, () => document.querySelector('.public-mail-send')?.disabled === false), 'other tab unlock')
  assert.equal(await run(page, () => !!document.querySelector('.mail-send-modal')), false)

  await openComposer({ recipient: 'recipient@example.invalid', subject: 'Retry subject', body: 'Retained body' })
  await submit(); await waitFor(() => sends.length === 2, 'failed send')
  finishSend(sends[1], 'failed')
  await waitFor(() => run(page, () => document.querySelector('[aria-live] .border-red-500')?.parentElement.textContent.includes('未扣次数')), 'failure notice')
  assert.equal(await run(page, () => document.querySelector('.mail-send-modal [name=body]').value), 'Retained body')
  assert.equal(card.used_count, 1)
  await submit(); await waitFor(() => sends.length === 3, 'manual retry')
  assert.notEqual(sends[1].payload.request_id, sends[2].payload.request_id)
  finishSend(sends[2], 'success')
  await waitFor(() => run(page, () => !document.querySelector('.mail-send-modal')), 'close on successful retry')

  await run(page, () => document.querySelector('.public-mail-fetch:not(.public-mail-send)').click())
  await waitFor(() => reads.length === 1, 'receive')
  assert.equal(await run(page, () => document.querySelector('.public-mail-send').disabled), true, 'sending while receiving was enabled')
  card.busy = false; card.operation = ''; card.used_count++
  const received = { id: 'fixture-message', source: 'imap', folder: 'inbox', subject: 'Received message', from: 'source@example.invalid', to: card.bound_email, time: '2020/01/02 03:04:05', timestamp: 1577934245, body: 'Fixture received body', html: '', body_preview: '' }
  json(reads[0], { ...snapshot(), messages: [received], message_item: received, charged: true, repeated: false, cooldown_seconds: 1, message: '获取到新邮件，已扣除一次' })
  await waitFor(() => run(page, () => document.querySelector('.public-mail-send').disabled === false), 'receive completion')

  dropNextSend = true
  await openComposer({ recipient: 'recipient@example.invalid', subject: 'Lost response' })
  await submit(); await waitFor(() => sends.length === 4, 'lost response request')
  await waitFor(() => run(page, () => {
    const notice = document.querySelector('.mail-send-modal [role=status]')?.textContent || ''
    return notice.includes('查询发送结果') || notice.includes('正在发送邮件')
  }), 'lost response recovery').catch(async (error) => {
    t.diagnostic(JSON.stringify(await run(page, () => ({ notice: document.querySelector('[aria-live]')?.textContent, composer: document.querySelector('.mail-send-modal')?.textContent, stored: Object.keys(sessionStorage).filter((key) => key.startsWith('public_mail_send:')).length }))))
    await capture(page, 'public-mail-recovery-debug.png')
    throw error
  })
  assert.equal(sends.length, 4, 'network error retried the POST')
  await cdp.send('Page.reload', {}, page)
  await waitFor(() => run(page, () => document.querySelector('.public-mail-send')?.disabled === true), 'reload preserves active send')
  finishSend(sends[3], 'success')
  await waitFor(() => run(page, () => document.querySelector('.public-mail-remaining')?.textContent.includes('剩余 2/6')), 'recovered quota')
  await waitFor(() => run(page, () => document.querySelector('.public-mail-send')?.disabled === false), 'recovered send unlock').catch(async (error) => {
    t.diagnostic(JSON.stringify(await run(page, () => ({ text: document.querySelector('.public-mail-page')?.textContent, visibility: document.visibilityState, pending: Object.keys(sessionStorage).filter((key) => key.startsWith('public_mail_send:')).length }))))
    throw error
  })
  assert.equal(sends.length, 4)

  await openComposer({ recipient: 'recipient@example.invalid', subject: '待确认主题' })
  await submit(); await waitFor(() => sends.length === 5, 'ambiguous send')
  finishSend(sends[4], 'unknown')
  await waitFor(() => run(page, () => document.querySelector('.mail-send-modal [role=status]')?.textContent.includes('待确认')), 'ambiguous result retained')
  assert.equal(await run(page, () => document.querySelector('.mail-send-modal button[type=submit]').disabled), true)
  assert.equal(card.used_count, 4); assert.equal(card.reserved_count, 1)

  const blank = await cdp.page(base + '/fixture-blank')
  await waitFor(() => run(blank, () => location.pathname === '/fixture-blank' && !!document.body), 'login fixture')
  await run(blank, (user) => localStorage.setItem('auth_session_v1', JSON.stringify({ auth_token: 'fixture-login', refresh_token: 'fixture-refresh', auth_user: JSON.stringify(user), token_expires_at: String(Date.now() + 7200000), must_change_password: 'false' })), user)
  const logPage = await cdp.page(base + '/admin/card-key-logs')
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1720, height: 1000, deviceScaleFactor: 1, mobile: false }, logPage)
  await waitFor(() => run(logPage, () => document.querySelectorAll('.card-key-use-logs-table tbody tr').length === 6), 'operation logs')
  const rendered = await run(logPage, () => ({
    headers: [...document.querySelectorAll('.card-key-use-logs-table th')].map((cell) => cell.textContent.trim()),
    first: document.querySelector('.mail-operation-log').textContent.trim(),
    success: document.querySelector('.status-success').textContent.trim(),
    failed: document.querySelector('.status-failed').textContent.trim(),
    unknown: document.querySelector('.status-unknown').textContent.trim(),
    colors: ['success', 'failed', 'unknown'].map((kind) => getComputedStyle(document.querySelector('.status-' + kind)).color),
    send: [...document.querySelectorAll('.mail-operation-log')][1].textContent,
  }))
  assert.deepEqual(rendered.headers, ['卡密', '绑定邮箱', '邮件操作日志', '使用IP', '使用时间'])
  assert.equal(rendered.first, '【收取邮件】Legacy message')
  assert.equal(rendered.success, '成功'); assert.equal(rendered.failed, '失败'); assert.equal(rendered.unknown, '待确认')
  assert.match(rendered.send, /【发送邮件】recipient@example\.invalid，无主题，/)
  assert.equal(new Set(rendered.colors).size, 3)
  await run(logPage, () => document.querySelector('button[title="切换亮色模式"]')?.click())
  await capture(logPage, 'card-key-logs-light.png')
  await run(logPage, () => document.querySelector('button[title="切换暗色模式"]')?.click())
  await capture(logPage, 'card-key-logs-dark.png')
  await run(logPage, () => document.querySelector('.status-action').click())
  await waitFor(() => run(logPage, () => !!document.querySelector('[aria-label="确认发件结果"]')), 'result confirmation')
  await run(logPage, () => [...document.querySelectorAll('[aria-label="确认发件结果"] button')].find((button) => button.textContent === '确认失败').click())
  await waitFor(() => card.reserved_count === 0, 'release confirmed failed reservation')
  await waitFor(() => run(page, () => document.querySelector('.public-mail-remaining')?.textContent.includes('剩余 2/6') && !document.querySelector('.public-mail-send').disabled), 'public page updates after confirmation')
  assert.equal(sends.length, 5, 'confirming a result sent another email')
  assert.deepEqual(cdp.exceptions, [])
  t.diagnostic(JSON.stringify({ sends: sends.length, used: card.used_count, reserved: card.reserved_count, columns: rendered.headers.length, geometry }))
})
