export function getSessionItem(key: string) {
  return sessionStorage.getItem(key)
}

export function setSessionItem(key: string, value: string) {
  sessionStorage.setItem(key, value)
}

const authStorageKeys = ['auth_token', 'refresh_token', 'auth_user', 'token_expires_at']
export const authSessionClearedEvent = 'auth-session-cleared'
export const authSessionClearedStorageKey = 'auth_session_cleared_at'

export function setAuthSessionItem(key: string, value: string) {
  sessionStorage.setItem(key, value)
  localStorage.setItem(key, value)
}

export function clearAuthSession(broadcast = true) {
  authStorageKeys.forEach((key) => {
    sessionStorage.removeItem(key)
    localStorage.removeItem(key)
  })

  if (broadcast) {
    const value = String(Date.now())
    localStorage.setItem(authSessionClearedStorageKey, value)
    window.dispatchEvent(new CustomEvent(authSessionClearedEvent, { detail: value }))
  }
}

export function getAuthToken() {
  const expiresAt = Number(sessionStorage.getItem('token_expires_at') || localStorage.getItem('token_expires_at') || 0)
  if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
    clearAuthSession()
    return ''
  }
  const token = sessionStorage.getItem('auth_token') || localStorage.getItem('auth_token') || ''
  const refreshToken = sessionStorage.getItem('refresh_token') || localStorage.getItem('refresh_token') || ''
  const user = sessionStorage.getItem('auth_user') || localStorage.getItem('auth_user') || ''

  if (token) {
    setAuthSessionItem('auth_token', token)
    if (refreshToken) setAuthSessionItem('refresh_token', refreshToken)
    if (user) setAuthSessionItem('auth_user', user)
    setAuthSessionItem('token_expires_at', String(expiresAt))
  }

  return token
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
