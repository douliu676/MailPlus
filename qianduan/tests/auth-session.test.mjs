import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { createContext, runInContext } from 'node:vm'
import ts from 'typescript'

const apiDirectory = new URL('../src/api/', import.meta.url)
const compiled = new Map()
const jsonResponse = (data, status = 200, headers = {}) => new Response(JSON.stringify(data), { status, headers })
const unauthorized = (scope = 'session') => jsonResponse(
  { code: 401, msg: 'fixture authorization failed' }, 401, { 'X-MailPlus-Auth-Error': scope },
)

function storageView(values, changed = () => {}) {
  const methods = {}
  Object.defineProperties(methods, {
    getItem: { value: (key) => values.get(String(key)) ?? null },
    setItem: { value: (key, value) => {
      key = String(key)
      value = String(value)
      const oldValue = values.get(key) ?? null
      values.set(key, value)
      if (value !== oldValue) changed(key, oldValue, value)
    } },
    removeItem: { value: (key) => {
      key = String(key)
      const oldValue = values.get(key)
      values.delete(key)
      if (oldValue !== undefined) changed(key, oldValue, null)
    } },
    clear: { value: () => { values.clear(); changed(null, null, null) } },
    key: { value: (index) => [...values.keys()][index] ?? null },
    length: { get: () => values.size },
  })
  return new Proxy(methods, {
    ownKeys: () => [...Reflect.ownKeys(methods), ...values.keys()],
    getOwnPropertyDescriptor: (target, key) => Reflect.getOwnPropertyDescriptor(target, key)
      || (values.has(key) ? { enumerable: true, configurable: true, value: values.get(key) } : undefined),
  })
}

function browserFixture() {
  const localValues = new Map()
  const tabs = []
  const queuedEvents = []
  let loginToken = 'fixture-new-session'

  function newTab(pathname = '/login') {
    const window = new EventTarget()
    const redirects = []
    window.location = {
      origin: 'http://mailplus.example.invalid',
      href: 'http://mailplus.example.invalid' + pathname,
      pathname,
      replace: (next) => { redirects.push(next); window.location.pathname = next },
    }
    window.localStorage = storageView(localValues, (key, oldValue, newValue) => {
      for (const other of tabs) {
        if (other.window === window) continue
        const event = new Event('storage')
        Object.assign(event, { key, oldValue, newValue, storageArea: other.window.localStorage })
        queuedEvents.push(() => other.window.dispatchEvent(event))
      }
    })
    const sessionValues = new Map()
    window.sessionStorage = storageView(sessionValues)
    const tab = { window, redirects, sessionValues, clearedEvents: 0 }
    tab.respond = async () => jsonResponse({ code: 0, data: {} })
    window.fetch = async (input, init) => {
      const url = typeof input === 'string' ? input : input.url || input.toString()
      if (url === '/api/auth/login') {
        return jsonResponse({ code: 0, data: {
          access_token: loginToken, refresh_token: 'fixture-refresh', expires_in: 3600,
          token_type: 'Bearer', must_change_password: false,
          user: { id: 1, username: 'fixture-admin', role: 'admin', email: 'fixture@example.invalid' },
        } })
      }
      return tab.respond(input, init)
    }
    const context = createContext({
      window, localStorage: window.localStorage, sessionStorage: window.sessionStorage,
      Event, EventTarget, CustomEvent, Headers, Request, Response, URL, DOMException,
      setTimeout, clearTimeout, console, fetch: (...args) => window.fetch(...args),
    })
    const modules = new Map()
    function load(name) {
      name = name.replace(/^\.\//, '').replace(/\.ts$/, '')
      if (modules.has(name)) return modules.get(name)
      const filename = fileURLToPath(new URL(name + '.ts', apiDirectory))
      if (!compiled.has(filename)) {
        compiled.set(filename, ts.transpileModule(readFileSync(filename, 'utf8'), {
          compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
          fileName: filename,
        }).outputText)
      }
      const module = { exports: {} }
      modules.set(name, module.exports)
      runInContext('(function(exports, require, module) {\n' + compiled.get(filename) + '\n})', context, { filename })(module.exports, load, module)
      return module.exports
    }
    tab.session = load('session')
    tab.auth = load('auth')
    window.addEventListener(tab.session.authSessionClearedEvent, () => { tab.clearedEvents++ })
    tabs.push(tab)
    return tab
  }

  return {
    newTab, localValues,
    seedLegacySession(token = 'fixture-old-session', expiresAt = Date.now() + 3_600_000) {
      localValues.set('auth_token', token)
      localValues.set('token_expires_at', String(expiresAt))
      localValues.set('auth_user', JSON.stringify({ id: 1, role: 'admin' }))
    },
    async login(tab, token = 'fixture-new-session') {
      loginToken = token
      return tab.auth.login({ account: 'fixture-admin', password: 'fixture-password' })
    },
    deliverStorageEvents() {
      let deliveries = 0
      while (queuedEvents.length) {
        assert.ok(++deliveries < 1000, 'storage notifications must not trigger a logout loop')
        queuedEvents.shift()()
      }
    },
  }
}

function deferred() {
  let resolve
  const promise = new Promise((yes) => { resolve = yes })
  return { promise, resolve }
}

for (const mode of ['imap', 'outlook']) {
  test('logging in with an open ' + mode + ' reader does not broadcast a logout', async () => {
    const browser = browserFixture()
    const reader = browser.newTab('/' + mode + '/mail/')
    const admin = browser.newTab('/login')
    // A reader already open during an upgrade may still react to legacy logout notifications.
    reader.window.addEventListener('storage', (event) => {
      if (event.key === reader.session.authSessionClearedStorageKey) reader.session.clearAuthSession(false)
    })
    await browser.login(admin)
    browser.deliverStorageEvents()
    assert.equal(admin.session.getAuthToken(), 'fixture-new-session')
    assert.equal(reader.session.getAuthToken(), 'fixture-new-session')
    assert.equal(admin.clearedEvents, 0)
  })
}

test('reading anonymous login state does not send logout notifications', () => {
  const browser = browserFixture()
  const tab = browser.newTab('/imap/mail/')
  assert.equal(tab.session.getAuthToken(), '')
  assert.equal(tab.session.getAuthToken(), '')
  assert.equal(tab.clearedEvents, 0)
  assert.equal(browser.localValues.has(tab.session.authSessionClearedStorageKey), false)
})

test('a delayed 401 from the previous login cannot clear the new login', async () => {
  const browser = browserFixture()
  browser.seedLegacySession()
  const tab = browser.newTab('/admin/accounts')
  const oldResponse = deferred()
  tab.respond = (_input, init) => {
    assert.equal(new Headers(init.headers).get('Authorization'), 'Bearer fixture-old-session')
    return oldResponse.promise
  }
  tab.session.installAuthFetchInterceptor()
  const pending = tab.window.fetch('/api/user/profile')
  await browser.login(tab)
  oldResponse.resolve(unauthorized())
  assert.equal((await pending).status, 401)
  assert.equal(tab.session.getAuthToken(), 'fixture-new-session')
  assert.deepEqual(tab.redirects, [])
})

test('a current session rejected by a protected endpoint still logs out once', async () => {
  const browser = browserFixture()
  browser.seedLegacySession()
  const tab = browser.newTab('/admin/accounts')
  tab.respond = async () => unauthorized()
  tab.session.installAuthFetchInterceptor()
  await Promise.all([tab.window.fetch('/api/user/profile'), tab.window.fetch('/api/admin/mail-accounts')])
  assert.equal(tab.session.getAuthToken(), '')
  assert.deepEqual(tab.redirects, ['/login'])
  assert.equal(tab.clearedEvents, 1)
})

for (const mode of ['imap', 'outlook']) {
  test(mode + ' access-key rejection does not revoke the backend login', async () => {
    const browser = browserFixture()
    browser.seedLegacySession()
    const tab = browser.newTab('/' + mode + '/mail/')
    tab.respond = async () => unauthorized('quick-mail-key')
    tab.session.installAuthFetchInterceptor()
    await tab.window.fetch('/api/admin/quick-mail/' + mode + '/receive', { method: 'POST' })
    assert.equal(tab.session.getAuthToken(), 'fixture-old-session')
    assert.deepEqual(tab.redirects, [])
  })
}

test('an anonymous request returning after login cannot revoke that login', async () => {
  const browser = browserFixture()
  const tab = browser.newTab('/imap/mail/')
  const oldResponse = deferred()
  tab.respond = () => oldResponse.promise
  tab.session.installAuthFetchInterceptor()
  const pending = tab.window.fetch('/api/admin/quick-mail/imap/detail', { method: 'POST' })
  await browser.login(tab)
  oldResponse.resolve(unauthorized('quick-mail-key'))
  await pending
  assert.equal(tab.session.getAuthToken(), 'fixture-new-session')
  assert.deepEqual(tab.redirects, [])
})

test('an explicit outdated Authorization header is not mistaken for the current session', async () => {
  const browser = browserFixture()
  browser.seedLegacySession('fixture-current-session')
  const tab = browser.newTab('/admin/accounts')
  tab.respond = async () => unauthorized()
  tab.session.installAuthFetchInterceptor()
  await tab.window.fetch('/api/user/profile', { headers: { Authorization: 'Bearer fixture-previous-session' } })
  assert.equal(tab.session.getAuthToken(), 'fixture-current-session')
  assert.deepEqual(tab.redirects, [])
})

test('expiration cleanup is idempotent after an actual session expires', () => {
  const browser = browserFixture()
  browser.seedLegacySession('fixture-expired-session', Date.now() - 1000)
  const tab = browser.newTab('/imap/mail/')
  assert.equal(tab.session.getAuthToken(), '')
  assert.equal(tab.session.getAuthToken(), '')
  assert.equal(tab.clearedEvents, 1)
})

test('same-origin Request objects get auth while foreign-origin requests do not', async () => {
  const browser = browserFixture()
  browser.seedLegacySession()
  const tab = browser.newTab('/admin/accounts')
  const authorization = []
  tab.respond = async (_input, init) => {
    authorization.push(new Headers(init.headers).get('Authorization'))
    return jsonResponse({ code: 0, data: {} })
  }
  tab.session.installAuthFetchInterceptor()
  await tab.window.fetch(new Request('http://mailplus.example.invalid/api/user/profile'))
  await tab.window.fetch('https://external.example.invalid/api/user/profile')
  assert.deepEqual(authorization, ['Bearer fixture-old-session', null])
})

test('queued logout notifications reconcile with the latest login in both readers', async () => {
  const browser = browserFixture()
  browser.seedLegacySession()
  const admin = browser.newTab('/admin/accounts')
  const readers = ['imap', 'outlook'].map((mode) => browser.newTab('/' + mode + '/mail/'))
  const observed = readers.map(() => [])
  readers.forEach((reader, index) => reader.session.watchAuthSession(() => {
    observed[index].push(reader.session.getAuthToken())
  }))
  browser.deliverStorageEvents()
  admin.session.clearAuthSession()
  await browser.login(admin)
  // Deliver the old logout and new login together, as when a background tab resumes.
  browser.deliverStorageEvents()
  assert.equal(admin.session.getAuthToken(), 'fixture-new-session')
  for (const changes of observed) assert.deepEqual(changes, ['fixture-new-session'])
})

test('explicit logout still notifies both readers exactly once', () => {
  const browser = browserFixture()
  browser.seedLegacySession()
  const admin = browser.newTab('/admin/accounts')
  const readers = ['imap', 'outlook'].map((mode) => browser.newTab('/' + mode + '/mail/'))
  const observed = readers.map(() => [])
  const unsubscribe = readers.map((reader, index) => reader.session.watchAuthSession(() => {
    observed[index].push(reader.session.getAuthToken())
  }))
  browser.deliverStorageEvents()
  admin.session.clearAuthSession()
  browser.deliverStorageEvents()
  admin.session.clearAuthSession()
  browser.deliverStorageEvents()
  for (const changes of observed) assert.deepEqual(changes, [''])
  unsubscribe.forEach((stop) => stop())
})

test('legacy tab data cannot replace or resurrect a newer shared login', () => {
  const browser = browserFixture()
  browser.seedLegacySession('fixture-shared-session')
  const admin = browser.newTab('/admin/accounts')
  const oldTab = browser.newTab('/imap/mail/')
  oldTab.sessionValues.set('auth_token', 'fixture-stale-tab-session')
  oldTab.sessionValues.set('token_expires_at', String(Date.now() + 3_600_000))
  oldTab.sessionValues.set('auth_user', JSON.stringify({ id: 2, role: 'user' }))
  assert.equal(admin.session.getAuthToken(), 'fixture-shared-session')
  assert.equal(oldTab.session.getAuthToken(), 'fixture-shared-session')
  assert.equal(oldTab.session.getAuthUserRole(), 'admin')
  admin.session.clearAuthSession()
  assert.equal(oldTab.session.getAuthToken(), '')
  oldTab.session.setAuthSessionItem('auth_user', JSON.stringify({ id: 2, role: 'user' }))
  assert.equal(oldTab.session.getAuthToken(), '')
  assert.equal(oldTab.session.getSessionItem('auth_user'), null)
})

test('quick-mail 401 responses from older backends do not log out the administrator', async () => {
  const browser = browserFixture()
  browser.seedLegacySession()
  const tab = browser.newTab('/outlook/mail/')
  tab.respond = async () => jsonResponse({ code: 401, msg: 'fixture key rejected' }, 401)
  tab.session.installAuthFetchInterceptor()
  await tab.window.fetch('/api/admin/quick-mail/outlook/detail', { method: 'POST' })
  assert.equal(tab.session.getAuthToken(), 'fixture-old-session')
  assert.deepEqual(tab.redirects, [])
})
