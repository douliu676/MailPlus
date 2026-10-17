import { clearAuthSession, getAuthToken, replaceAuthSession } from './session'

export type LoginRequest = {
  account: string
  password: string
}

export type LoginResponse = {
  access_token: string
  refresh_token: string
  expires_in: number
  token_type: string
  must_change_password: boolean
  user: {
    id: number
    username: string
    email: string
    avatar_url: string
    balance: number
    role: string
    status?: string
    created_at?: string
  }
}

export async function login(credentials: LoginRequest): Promise<LoginResponse> {
  const response = await fetch('/api/auth/login', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(credentials),
  })

  const result = await response.json()

  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '\u767b\u5f55\u5931\u8d25')
  }

  saveLoginResponse(result.data)

  return result.data
}

export function saveLoginResponse(data: LoginResponse) {
  replaceAuthSession({
    auth_token: data.access_token, refresh_token: data.refresh_token,
    auth_user: JSON.stringify(data.user), token_expires_at: String(Date.now() + data.expires_in * 1000),
    must_change_password: data.must_change_password ? 'true' : 'false',
  })
}

export function logout() {
  const token = getAuthToken()
  if (token) {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), 5000)
    void fetch('/api/auth/logout', {
      method: 'POST', headers: { Authorization: `Bearer ${token}` }, signal: controller.signal,
    }).catch(() => undefined).finally(() => clearTimeout(timer))
  }
  clearAuthSession(true, token)
}
