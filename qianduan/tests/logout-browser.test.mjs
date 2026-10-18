import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, readFile, realpath, rm } from 'node:fs/promises'
import { basename, dirname, extname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import { build } from 'vite'
import { connectDebugger, waitFor } from './helpers/browser.mjs'

const frontendRoot = fileURLToPath(new URL('../', import.meta.url))
const browserExecutable = [
  process.env.MAILPLUS_BROWSER_EXECUTABLE,
  join(process.env['PROGRAMFILES(X86)'] || '', 'Microsoft/Edge/Application/msedge.exe'),
  join(process.env.PROGRAMFILES || '', 'Google/Chrome/Application/chrome.exe'),
  '/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/google-chrome',
].find((path) => path && existsSync(path))

test('explicit logout is silent while old requests, page mounting and other tabs are active', {
  timeout: 120_000,
  skip: !browserExecutable && 'Set MAILPLUS_BROWSER_EXECUTABLE to an Edge or Chromium executable',
}, async (t) => {
  const temporaryRoot = resolve(frontendRoot, 'tmp')
  await mkdir(temporaryRoot, { recursive: true })
  const testRoot = await mkdtemp(join(temporaryRoot, 'logout-browser-'))
  const webRoot = process.env.MAILPLUS_AUTH_WEB_ROOT ? resolve(process.env.MAILPLUS_AUTH_WEB_ROOT) : join(testRoot, 'web')
  let browser, cdp, server
  const held = new Set()
  t.after(async () => {
    for (const response of held) response.destroy()
    if (cdp) { await cdp.send('Browser.close').catch(() => {}); cdp.close() }
    if (browser && browser.exitCode === null) browser.kill()
    if (server) { server.closeAllConnections(); await new Promise((done) => server.close(done)) }
    const actual = await realpath(testRoot)
    if (dirname(actual) === await realpath(temporaryRoot) && basename(actual).startsWith('logout-browser-')) {
      await rm(actual, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 })
    }
  })
  if (!process.env.MAILPLUS_AUTH_WEB_ROOT) {
    await build({ root: frontendRoot, logLevel: 'warn', build: { outDir: webRoot, emptyOutDir: false } })
  }
  const user = { id: 1, username: 'fixture-admin', role: 'admin', email: 'fixture@example.invalid', balance: 0, status: 'active', avatar_url: '' }
  const settings = { site_name: 'MailPlus fixture', site_logo: '/logo.png', table_default_page_size: 20, table_page_size_options: [10, 20, 50] }
  let activeToken = 'fixture-initial-login'
  let loginCount = 0
  let logoutCount = 0
  let holdLoads = true
  let rejectAccountLoad = false
  const anonymousProtectedRequests = []
  const requests = []
  const headers = { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }
  const sendJSON = (response, data, status = 200, message = 'ok') => {
    if (response.destroyed || response.writableEnded) return
    response.writeHead(status, { ...headers, ...(status === 401 ? { 'X-MailPlus-Auth-Error': 'session' } : {}) })
    response.end(JSON.stringify({ code: status === 200 ? 0 : status, data, msg: message }))
  }
  const loadRoutes = new Set(['/api/admin/mail-groups', '/api/admin/mail-servers', '/api/admin/mail-accounts'])
  server = createServer(async (request, response) => {
    try {
      const path = new URL(request.url, 'http://127.0.0.1').pathname
      requests.push(path)
      if (path === '/fixture-blank') { response.writeHead(200, { 'Content-Type': 'text/html' }); response.end('<!doctype html><body>fixture</body>'); return }
      if (path === '/api/settings/bootstrap.js') {
        response.writeHead(200, { 'Content-Type': 'text/javascript' })
        response.end('window.__APP_PUBLIC_SETTINGS__=' + JSON.stringify(settings)); return
      }
      if (path === '/api/settings/public') return sendJSON(response, settings)
      if (path === '/api/auth/login') {
        let raw = ''; for await (const chunk of request) raw += chunk
        if (JSON.parse(raw).password !== 'fixture-password') return sendJSON(response, null, 401, '账号或密码错误')
        activeToken = 'fixture-login-' + (++loginCount)
        return sendJSON(response, { access_token: activeToken, refresh_token: 'fixture-refresh', expires_in: 7200, must_change_password: false, user })
      }
      if (path === '/api/auth/logout') {
        request.resume(); activeToken = ''; logoutCount++
        return sendJSON(response, null)
      }
      if (path.startsWith('/api/')) {
        request.resume()
        if (!activeToken || request.headers.authorization !== 'Bearer ' + activeToken) {
          if (!request.headers.authorization) anonymousProtectedRequests.push(path)
          return sendJSON(response, null, 401, '请先登录')
        }
        if (holdLoads && loadRoutes.has(path)) { held.add(response); return }
        if (path === '/api/user/profile') return sendJSON(response, user)
        if (path === '/api/user/quick-mail-key') return sendJSON(response, { configured: false })
        if (path === '/api/admin/settings') return sendJSON(response, settings)
        if (path === '/api/admin/tasks') return sendJSON(response, [])
        if (path === '/api/admin/mail-groups' || path === '/api/admin/outlook-groups') return sendJSON(response, [{ id: 1, name: '全部邮箱', parent_id: 0, system: true }, { id: 2, name: '默认分组', parent_id: 0, system: true }])
        if (path === '/api/admin/mail-servers') return sendJSON(response, [])
        if (path === '/api/admin/update-check') return sendJSON(response, { status: 'latest', current_version: 'v1.0.1' })
        if (path === '/api/admin/mail-accounts' && rejectAccountLoad) { requests.push('fixture-account-error-400'); return sendJSON(response, null, 400, '当前操作失败，请重试') }
        return sendJSON(response, { items: [], total: 0, page: 1, pages: 0, page_size: 20, normal: 0, error: 0 })
      }
      let filename = resolve(webRoot, '.' + decodeURIComponent(path))
      if (!filename.startsWith(webRoot + sep) || !extname(filename)) filename = join(webRoot, 'index.html')
      // Force an asynchronous logout route transition before the login chunk is cached.
      if (/[/\\]login-[^/\\]+\.js$/.test(filename)) await delay(400)
      const content = await readFile(filename)
      response.writeHead(200, { 'Content-Type': { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png' }[extname(filename)] || 'application/octet-stream' })
      response.end(content)
    } catch { if (!response.destroyed) { response.writeHead(404); response.end() } }
  })
  await new Promise((yes, no) => { server.once('error', no); server.listen(0, '127.0.0.1', yes) })
  const base = 'http://127.0.0.1:' + server.address().port
  const profile = join(testRoot, 'profile')
  browser = spawn(browserExecutable, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', '--user-data-dir=' + profile, 'about:blank'], { windowsHide: true, stdio: 'ignore' })
  await waitFor(() => existsSync(join(profile, 'DevToolsActivePort')), 'isolated browser')
  const port = (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0].trim()
  const info = await (await fetch('http://127.0.0.1:' + port + '/json/version')).json()
  cdp = await connectDebugger(info.webSocketDebuggerUrl)
  const { evaluate } = cdp
  const page = await cdp.page(base + '/fixture-blank')
  await waitFor(() => evaluate(page, 'location.pathname === "/fixture-blank" && !!document.body'), 'blank fixture')
  const session = { auth_token: activeToken, refresh_token: 'fixture-refresh', auth_user: JSON.stringify(user), token_expires_at: String(Date.now() + 7200000), must_change_password: 'false' }
  await evaluate(page, 'localStorage.setItem("auth_session_v1", ' + JSON.stringify(JSON.stringify(session)) + ');true')
  await cdp.send('Page.navigate', { url: base + '/admin/accounts' }, page)
  await waitFor(() => evaluate(page, `!!document.querySelector('[aria-label="User Menu"]')`), 'admin menu')
  await waitFor(() => held.size >= 3, 'pending account requests')
  const readers = []
  for (const mode of ['imap', 'outlook']) {
    const reader = await cdp.page(base + '/' + mode + '/mail/')
    await waitFor(() => evaluate(reader, '!!document.querySelector(".quick-mail-secret-input")'), 'open reader')
    readers.push(reader)
  }
  async function observeErrors() {
    await evaluate(page, `window.fixtureErrors=[];window.fixtureObserver?.disconnect();window.fixtureObserver=new MutationObserver(()=>{for(const e of document.querySelectorAll('[aria-live] .border-red-500')){const text=e.textContent.trim();if(!window.fixtureErrors.includes(text))window.fixtureErrors.push(text)}});window.fixtureObserver.observe(document.body,{subtree:true,childList:true,characterData:true});true`)
  }
  async function logout() {
    await cdp.send('Page.bringToFront', {}, page)
    await evaluate(page, `document.querySelector('[aria-label="User Menu"]').click();true`)
    await waitFor(() => evaluate(page, `Array.from(document.querySelectorAll('button')).some(e=>e.textContent.includes('退出登录'))`), 'logout item')
    await evaluate(page, `Array.from(document.querySelectorAll('button')).find(e=>e.textContent.includes('退出登录')).click();true`)
    await waitFor(() => evaluate(page, 'location.pathname === "/login" && !!document.querySelector("#account")'), 'logout route')
  }
  async function signIn(password = 'fixture-password') {
    await evaluate(page, `(()=>{for(const [id,value] of [['account','fixture-admin'],['password',${JSON.stringify(password)}]]){const e=document.getElementById(id);e.value=value;e.dispatchEvent(new Event('input',{bubbles:true}))}document.querySelector('form').requestSubmit()})()`)
  }
  await observeErrors()
  await logout()
  for (const response of held) sendJSON(response, null, 401, '请先登录')
  held.clear(); holdLoads = false
  await delay(1200)
  assert.deepEqual(await evaluate(page, 'window.fixtureErrors'), [], 'explicit logout displayed errors from old requests or a remounted admin page')
  assert.deepEqual(anonymousProtectedRequests, [], 'logout started new unauthenticated admin requests')
  for (const reader of readers) assert.equal(await evaluate(reader, 'document.querySelector(".quick-mail-secret-input").placeholder'), '请输入秘钥')

  // Genuine login errors remain visible; errors are not hidden by matching their text.
  await signIn('wrong-password')
  await waitFor(() => evaluate(page, `document.querySelector('[aria-live]')?.textContent.includes('账号或密码错误')`), 'visible invalid credentials')
  await signIn()
  await waitFor(() => evaluate(page, 'location.pathname === "/admin/dashboard"'), 'login again')
  for (const reader of readers) await waitFor(() => evaluate(reader, 'document.querySelector(".quick-mail-secret-input").placeholder === "已登录可留空"'), 'reader login sync')
  await delay(4000) // Let the deliberately produced login error toast expire.
  rejectAccountLoad = true
  await cdp.send('Page.bringToFront', {}, page)
  await cdp.send('Page.navigate', { url: base + '/admin/accounts' }, page)
  try {
    await waitFor(() => evaluate(page, `Array.from(document.querySelectorAll('[aria-live] .border-red-500')).some(e=>e.textContent.includes('当前操作失败，请重试'))`), 'visible active-session operation error')
  } catch (error) {
    t.diagnostic(JSON.stringify({ requests: requests.slice(-20), page: await evaluate(page, '({path:location.pathname,text:document.body.innerText,role:JSON.parse(JSON.parse(localStorage.getItem("auth_session_v1"))?.auth_user||"null")?.role})'), exceptions: cdp.exceptions }))
    throw error
  }
  rejectAccountLoad = false
  await cdp.send('Page.navigate', { url: base + '/admin/profile' }, page)
  await waitFor(() => evaluate(page, '!!document.getElementById("profile_username")'), 'profile')
  await observeErrors()
  await logout()
  await delay(1000)
  assert.deepEqual(await evaluate(page, 'window.fixtureErrors'), [], 'a repeated logout displayed authentication errors')
  assert.equal(logoutCount, 2)

  // A current session rejected by the server still invalidates and redirects.
  await signIn()
  await waitFor(() => evaluate(page, 'location.pathname === "/admin/dashboard"'), 'third login')
  activeToken = ''
  await evaluate(page, `fetch('/api/user/profile').catch(()=>null);true`)
  await waitFor(() => evaluate(page, 'location.pathname === "/login"'), 'actual session expiration redirect')
  assert.equal(await evaluate(page, 'JSON.parse(localStorage.getItem("auth_session_v1"))'), null)
  assert.deepEqual(cdp.exceptions, [], 'logout must not leave uncaught browser exceptions')
})
