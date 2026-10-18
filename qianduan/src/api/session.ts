export function getSessionItem(key: string) {
  return isAuthStorageKey(key) ? readAuthSession()?.[key] ?? null : localStorage.getItem(key)
}

export function setSessionItem(key: string, value: string) {
  if (isAuthStorageKey(key)) {
    setAuthSessionItem(key, value)
    return
  }
  localStorage.setItem(key, value)
  sessionStorage.removeItem(key)
}

const authStorageKeys = ['auth_token', 'refresh_token', 'auth_user', 'token_expires_at', 'must_change_password'] as const
type AuthStorageKey = typeof authStorageKeys[number]
type AuthSession = Partial<Record<AuthStorageKey, string>>
const authScopedStoragePrefixes = [
  'mail_receive_cache_', 'outlook_read_cache_', 'mail_management_cache_', 'outlook_management_cache_',
  'admin_users_management_cache_', 'dashboard_stats_cache_', 'system_settings_cache_',
  'proxy_system_cache_', 'card_key_management_cache_', 'card_key_logs_cache_',
  'card_key_email_picker_available_cache_', 'admin_task_center_tasks_',
]
export const authSessionStorageKey = 'auth_session_v1'
export const authSessionChangedEvent = 'auth-session-changed'
export const authSessionClearedEvent = 'auth-session-cleared'
export const authSessionClearedStorageKey = 'auth_session_cleared_at'

function isAuthStorageKey(key: string): key is AuthStorageKey {
  return (authStorageKeys as readonly string[]).includes(key)
}

function readAuthSession(): AuthSession | null {
  const stored = localStorage.getItem(authSessionStorageKey)
  if (stored !== null) {
    try {
      const value = JSON.parse(stored)
      if (!value || typeof value !== 'object' || Array.isArray(value)) return null
      const session: AuthSession = {}
      for (const key of authStorageKeys) {
        if (typeof value[key] === 'string') session[key] = value[key]
      }
      return session.auth_token ? session : null
    } catch {
      return null
    }
  }

  // Migrate one complete legacy session; never mix a tab's old metadata with a newer token.
  const legacy = localStorage.getItem('auth_token') ? localStorage
    : !localStorage.getItem(authSessionClearedStorageKey) && sessionStorage.getItem('auth_token') ? sessionStorage
      : null
  if (!legacy) return null
  const session: AuthSession = {}
  for (const key of authStorageKeys) session[key] = legacy.getItem(key) || ''
  localStorage.setItem(authSessionStorageKey, JSON.stringify(session))
  removeLegacyAuthSession()
  return session
}

function removeLegacyAuthSession() {
  for (const key of authStorageKeys) {
    sessionStorage.removeItem(key)
    localStorage.removeItem(key)
  }
}

function clearAuthScopedStorage() {
  for (const storage of [localStorage, sessionStorage]) {
    Object.keys(storage).forEach((key) => {
      if (authScopedStoragePrefixes.some((prefix) => key.startsWith(prefix))) storage.removeItem(key)
    })
  }
}

export function replaceAuthSession(session: Record<AuthStorageKey, string>) {
  clearAuthScopedStorage()
  // A single write publishes the token, expiry and user together to every open tab.
  localStorage.setItem(authSessionStorageKey, JSON.stringify(session))
  removeLegacyAuthSession()
  window.dispatchEvent(new Event(authSessionChangedEvent))
}

export function setAuthSessionItem(key: string, value: string) {
  if (!isAuthStorageKey(key)) return
  const session = readAuthSession()
  if (!session || session[key] === value) return
  localStorage.setItem(authSessionStorageKey, JSON.stringify({ ...session, [key]: value }))
  window.dispatchEvent(new Event(authSessionChangedEvent))
}

export function clearAuthSession(broadcast = true, expectedToken?: string) {
  const session = readAuthSession()
  if (expectedToken !== undefined && session?.auth_token !== expectedToken) return false

  // Keep a tombstone so an old tab's sessionStorage cannot resurrect a signed-out session.
  localStorage.setItem(authSessionStorageKey, 'null')
  removeLegacyAuthSession()
  clearAuthScopedStorage()
  if (!session?.auth_token) return false

  if (broadcast) {
    const value = String(Date.now())
    localStorage.setItem(authSessionClearedStorageKey, value)
    window.dispatchEvent(new CustomEvent(authSessionClearedEvent, { detail: value }))
  }
  return true
}

export function getAuthToken(): string {
  const session = readAuthSession()
  const token = session?.auth_token || ''
  if (!token) return ''
  const expiresAt = Number(session?.token_expires_at || 0)
  if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
    return clearAuthSession(true, token) ? '' : getAuthToken()
  }
  return token
}

export function getAuthUserRole() {
  try {
    const user = JSON.parse(getSessionItem('auth_user') || 'null') as { role?: unknown } | null
    return typeof user?.role === 'string' ? user.role : ''
  } catch {
    return ''
  }
}

export function createAuthCacheGuard() {
  const token = getAuthToken()
  return () => Boolean(token && getAuthToken() === token && getAuthUserRole() === 'admin')
}

export function watchAuthSession(listener: () => void) {
  const identity = () => JSON.stringify([getAuthToken(), getAuthUserRole()])
  let previous = identity()
  const reconcile = () => {
    const current = identity()
    if (current === previous) return
    previous = current
    listener()
  }
  const onStorage = (event: StorageEvent) => {
    if (event.key == null || event.key === authSessionStorageKey || event.key === authSessionClearedStorageKey || isAuthStorageKey(event.key)) {
      // Storage events may arrive after another login. Read the latest state without replaying a logout.
      reconcile()
    }
  }
  window.addEventListener('storage', onStorage)
  window.addEventListener(authSessionChangedEvent, reconcile)
  window.addEventListener(authSessionClearedEvent, reconcile)
  return () => {
    window.removeEventListener('storage', onStorage)
    window.removeEventListener(authSessionChangedEvent, reconcile)
    window.removeEventListener(authSessionClearedEvent, reconcile)
  }
}

let authFetchInstalled = false

export function installAuthFetchInterceptor() {
  if (authFetchInstalled) return
  authFetchInstalled = true
  // Remove settings cached by older builds, which included the WebDAV password.
  Object.keys(localStorage).filter((key) => key.startsWith('system_settings_cache_')).forEach((key) => localStorage.removeItem(key))
  if (!getAuthToken()) clearAuthScopedStorage()
  const rawFetch = window.fetch.bind(window)
  let sessionToken = getAuthToken()
  let sessionRequests = new AbortController()
  const syncRequestSession = () => {
    const current = getAuthToken()
    if (current === sessionToken) return
    sessionToken = current
    sessionRequests.abort()
    sessionRequests = new AbortController()
  }
  watchAuthSession(syncRequestSession)

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const requestURL = new URL(url, window.location.href)
    const shouldAttachAuth = requestURL.origin === window.location.origin && /^\/api\/(?:admin|user)(?:\/|$)/.test(requestURL.pathname)
    const optionalQuickMailAuth = /^\/api\/admin\/quick-mail\/(?:imap|outlook)\/(?:receive|detail)\/?$/.test(requestURL.pathname)
    const publicOAuthCallback = requestURL.pathname === '/api/admin/outlook-oauth/callback'
    syncRequestSession()
    const token = shouldAttachAuth ? getAuthToken() : ''
    const headers = new Headers(init?.headers || (input instanceof Request ? input.headers : undefined))

    if (token && !headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${token}`)
    }
    const requestToken = /^Bearer\s+(.+)$/i.exec(headers.get('Authorization') || '')?.[1].trim() || ''
    if (shouldAttachAuth && !optionalQuickMailAuth && !publicOAuthCallback && !requestToken) {
      throw new DOMException('请求已取消', 'AbortError')
    }
    const ownedBySession = Boolean(shouldAttachAuth && token && requestToken === token)
    const callerSignal = init?.signal ?? (input instanceof Request ? input.signal : undefined)
    const signal = ownedBySession
      ? AbortSignal.any([sessionRequests.signal, ...(callerSignal ? [callerSignal] : [])])
      : callerSignal

    const response = await rawFetch(input, {
      ...init,
      headers,
      ...(signal ? { signal } : {}),
    })
    if (ownedBySession && readAuthSession()?.auth_token !== requestToken) {
      throw new DOMException('登录状态已变更', 'AbortError')
    }

    const authError = response.headers.get('X-MailPlus-Auth-Error')
    const sessionRejected = authError === 'session' || (!authError && !optionalQuickMailAuth)
    if (response.status === 401 && shouldAttachAuth && requestToken && sessionRejected && clearAuthSession(true, requestToken)) {
      if (window.location.pathname !== '/login') {
        window.location.replace('/login')
      }
    }

    // A successful response from a previous session must not repopulate caches
    // after logout. Check both headers and completion of delayed response bodies.
    if (response.ok && shouldAttachAuth && requestToken) {
      const assertCurrentSession = () => {
        if (readAuthSession()?.auth_token !== requestToken) throw new DOMException('登录状态已变更', 'AbortError')
      }
      assertCurrentSession()
      for (const method of ['json', 'text', 'blob', 'arrayBuffer', 'formData'] as const) {
        const readBody = response[method].bind(response)
        Object.defineProperty(response, method, { value: async () => {
          const value = await readBody()
          assertCurrentSession()
          return value
        } })
      }
    }

    return response
  }
}
