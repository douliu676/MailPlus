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

test('backend login survives open IMAP/Outlook pages and a delayed previous-session 401', {
  timeout: 90_000,
  skip: !browserExecutable && 'Set MAILPLUS_BROWSER_EXECUTABLE to an Edge or Chromium executable',
}, async (t) => {
  const temporaryRoot = resolve(frontendRoot, 'tmp')
  await mkdir(temporaryRoot, { recursive: true })
  const testRoot = await mkdtemp(join(temporaryRoot, 'auth-browser-'))
  const webRoot = process.env.MAILPLUS_AUTH_WEB_ROOT ? resolve(process.env.MAILPLUS_AUTH_WEB_ROOT) : join(testRoot, 'web')
  let browserProcess, debuggerClient, heldResponse, server
  t.after(async () => {
    heldResponse?.destroy()
    if (debuggerClient) {
      await debuggerClient.send('Browser.close').catch(() => {})
      debuggerClient.close()
    }
    if (browserProcess && browserProcess.exitCode === null) browserProcess.kill()
    if (server) {
      server.closeAllConnections()
      await new Promise((done) => server.close(done))
    }
    // Only remove the unique fixture directory created by this test, never a browser/user profile.
    const actualRoot = await realpath(testRoot)
    if (dirname(actualRoot) === await realpath(temporaryRoot) && basename(actualRoot).startsWith('auth-browser-')) {
      await rm(actualRoot, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 })
    }
  })

  if (!process.env.MAILPLUS_AUTH_WEB_ROOT) {
    await build({ root: frontendRoot, logLevel: 'warn', build: { outDir: webRoot, emptyOutDir: false } })
  }
  let loginCount = 0
  let activeToken = ''
  const user = { id: 1, username: 'fixture-admin', email: 'fixture@example.invalid', role: 'admin', balance: 0, avatar_url: '', status: 'active' }
  const sendJSON = (response, data, status = 200, headers = {}) => {
    response.writeHead(status, { 'Content-Type': 'application/json', ...headers })
    response.end(JSON.stringify({ code: status === 200 ? 0 : status, data, msg: status === 200 ? 'ok' : 'fixture session expired' }))
  }
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, 'http://127.0.0.1').pathname
      if (pathname === '/api/auth/login') {
        request.resume()
        activeToken = 'fixture-login-' + (++loginCount)
        return sendJSON(response, { access_token: activeToken, refresh_token: 'fixture-refresh', expires_in: 3600, must_change_password: false, user })
      }
      if (pathname === '/api/settings/public') return sendJSON(response, { site_name: 'MailPlus fixture' })
      if (pathname === '/api/user/fixture-delayed') {
        heldResponse = response
        return
      }
      if (pathname.startsWith('/api/')) {
        if (!activeToken || request.headers.authorization !== 'Bearer ' + activeToken) {
          return sendJSON(response, null, 401, { 'X-MailPlus-Auth-Error': 'session' })
        }
        if (pathname === '/api/user/profile') return sendJSON(response, user)
        return sendJSON(response, { items: [], total: 0, page: 1, page_size: 20, stats: { total: 0, normal: 0, error: 0 }, status: 'latest' })
      }
      let filename = resolve(webRoot, '.' + decodeURIComponent(pathname))
      if (!filename.startsWith(webRoot + sep)) filename = join(webRoot, 'index.html')
      if (!extname(filename)) filename = join(webRoot, 'index.html')
      const content = await readFile(filename)
      const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml' }
      response.writeHead(200, { 'Content-Type': types[extname(filename)] || 'application/octet-stream' })
      response.end(content)
    } catch {
      response.writeHead(404)
      response.end()
    }
  })
  await new Promise((yes, no) => { server.once('error', no); server.listen(0, '127.0.0.1', yes) })
  const baseURL = 'http://127.0.0.1:' + server.address().port
  const profile = join(testRoot, 'profile')
  let browserStartupError
  browserProcess = spawn(browserExecutable, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-networking', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0',
    '--user-data-dir=' + profile, 'about:blank',
  ], { windowsHide: true, stdio: 'ignore' })
  browserProcess.on('error', (error) => { browserStartupError = error })
  const portFile = join(profile, 'DevToolsActivePort')
  await waitFor(() => {
    if (browserStartupError) throw browserStartupError
    return existsSync(portFile)
  }, 'isolated browser startup')
  const port = (await readFile(portFile, 'utf8')).split('\n')[0].trim()
  const info = await (await fetch('http://127.0.0.1:' + port + '/json/version')).json()
  debuggerClient = await connectDebugger(info.webSocketDebuggerUrl)
  const { evaluate } = debuggerClient
  const readers = []
  for (const mode of ['imap', 'outlook']) {
    const reader = await debuggerClient.page(baseURL + '/' + mode + '/mail/')
    await waitFor(() => evaluate(reader, '!!document.querySelector(".quick-mail-fetch-button")'), mode + ' reader mount')
    readers.push(reader)
  }
  async function signIn() {
    const page = await debuggerClient.page(baseURL + '/login')
    await waitFor(() => evaluate(page, '!!document.querySelector("#account")'), 'login form')
    await evaluate(page, '(() => { for (const [id, value] of [["account", "fixture-admin"], ["password", "fixture-password"]]) { const input = document.getElementById(id); input.value = value; input.dispatchEvent(new Event("input", { bubbles: true })); } document.querySelector("form").requestSubmit(); })()')
    return page
  }
  async function assertStillSignedIn(admin) {
    await delay(700)
    assert.equal(await evaluate(admin, 'location.pathname'), '/admin/dashboard', 'backend login was lost while the mail readers remained open')
    for (const page of [admin, ...readers]) {
      assert.equal(await evaluate(page, 'fetch("/api/user/profile").then(response => response.status)'), 200, 'an open page lost the current session')
    }
    assert.deepEqual(await Promise.all(readers.map((page) => evaluate(page, 'location.pathname'))), ['/imap/mail/', '/outlook/mail/'])
  }
  const firstAdmin = await signIn()
  await waitFor(() => loginCount === 1, 'first login response')
  await assertStillSignedIn(firstAdmin)

  await evaluate(readers[0], 'window.fixtureDelayedStatus = "pending"; fetch("/api/user/fixture-delayed").then(response => { window.fixtureDelayedStatus = response.status }).catch(error => { window.fixtureDelayedStatus = error.name }); true')
  await waitFor(() => !!heldResponse, 'old in-flight request')
  activeToken = ''
  const secondAdmin = await signIn()
  await waitFor(() => loginCount === 2, 'login after session expiry')
  sendJSON(heldResponse, null, 401, { 'X-MailPlus-Auth-Error': 'session' })
  heldResponse = undefined
  await waitFor(() => evaluate(readers[0], 'window.fixtureDelayedStatus === 401 || window.fixtureDelayedStatus === "AbortError"'), 'old request cancelled or rejected')
  await assertStillSignedIn(secondAdmin)
  assert.deepEqual(debuggerClient.exceptions, [], 'browser must not report uncaught application exceptions')
})
