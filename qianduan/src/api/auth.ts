import { clearAuthSession, setAuthSessionItem } from './session'

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

  clearAuthSession()
  setAuthSessionItem('auth_token', result.data.access_token)
  setAuthSessionItem('refresh_token', result.data.refresh_token)
  setAuthSessionItem('auth_user', JSON.stringify(result.data.user))
  setAuthSessionItem('token_expires_at', String(Date.now() + result.data.expires_in * 1000))
  setAuthSessionItem('must_change_password', result.data.must_change_password ? 'true' : 'false')

  return result.data
}
