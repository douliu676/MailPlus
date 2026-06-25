export function getSessionItem(key: string) {
  migrateAuthSessionStorage()
  return localStorage.getItem(key)
}

export function setSessionItem(key: string, value: string) {
  localStorage.setItem(key, value)
  sessionStorage.removeItem(key)
}

const authStorageKeys = ['auth_token', 'refresh_token', 'auth_user', 'token_expires_at', 'must_change_password']
const authScopedStoragePrefixes = ['mail_receive_cache_v1:', 'outlook_read_cache_v1:']
export const authSessionClearedEvent = 'auth-session-cleared'
export const authSessionClearedStorageKey = 'auth_session_cleared_at'

function migrateAuthSessionStorage() {
  authStorageKeys.forEach((key) => {
    const legacyValue = sessionStorage.getItem(key)
    if (legacyValue && !localStorage.getItem(key)) {
      localStorage.setItem(key, legacyValue)
    }
    sessionStorage.removeItem(key)
  })
}

export function setAuthSessionItem(key: string, value: string) {
  localStorage.setItem(key, value)
  sessionStorage.removeItem(key)
}

export function clearAuthSession(broadcast = true) {
  authStorageKeys.forEach((key) => {
    sessionStorage.removeItem(key)
    localStorage.removeItem(key)
  })
  Object.keys(localStorage).forEach((key) => {
    if (authScopedStoragePrefixes.some((prefix) => key.startsWith(prefix))) {
      localStorage.removeItem(key)
    }
  })

  if (broadcast) {
    const value = String(Date.now())
    localStorage.setItem(authSessionClearedStorageKey, value)
    window.dispatchEvent(new CustomEvent(authSessionClearedEvent, { detail: value }))
  }
}

export function getAuthToken() {
  migrateAuthSessionStorage()
  const expiresAt = Number(localStorage.getItem('token_expires_at') || 0)
  if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
    clearAuthSession()
    return ''
  }
  return localStorage.getItem('auth_token') || ''
}

export function getAuthUserRole() {
  migrateAuthSessionStorage()
  try {
    const user = JSON.parse(localStorage.getItem('auth_user') || 'null') as { role?: unknown } | null
    return typeof user?.role === 'string' ? user.role : ''
  } catch {
    return ''
  }
}

export function installAuthFetchInterceptor() {
  const rawFetch = window.fetch.bind(window)

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const shouldAttachAuth = url.startsWith('/api/admin') || url.startsWith('/api/user')
    const token = shouldAttachAuth ? getAuthToken() : ''
    const headers = new Headers(init?.headers || (input instanceof Request ? input.headers : undefined))

    if (token && !headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${token}`)
    }

    const response = await rawFetch(input, {
      ...init,
      headers,
    })

    if (response.status === 401 && shouldAttachAuth) {
      clearAuthSession()
      if (window.location.pathname !== '/login') {
        window.location.replace('/login')
      }
    }

    return response
  }
}
