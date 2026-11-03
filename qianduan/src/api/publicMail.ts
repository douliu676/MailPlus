export type PublicMailInfo = {
  key: string
  status: string
  bound_email: string
  has_bound_email: boolean
  usage_limit: number
  used_count: number
  remaining: number
  mail_days: number
  mail_keyword: string
  cooldown_seconds: number
  reserved_count?: number
  busy?: boolean
  operation?: 'receive' | 'send' | ''
}

export type PublicMailMessage = {
  id: string
  source: string
  folder: string
  subject: string
  from: string
  to: string
  time: string
  timestamp: number
  body_preview: string
  body: string
  html: string
}

export type PublicMailMessagesResponse = {
  email: string
  messages: PublicMailMessage[]
  message_item: PublicMailMessage | null
  charged: boolean
  repeated: boolean
  used_count: number
  remaining: number
  wait_seconds: number
  cooldown_seconds: number
  message: string
  reserved_count?: number
}

export type PublicMailSendPayload = {
  request_id: string
  email?: string
  nickname: string
  recipient: string
  subject: string
  body: string
}

export type PublicMailSendResponse = {
  operation_id: string
  request_id: string
  email: string
  recipient: string
  subject: string
  status: 'running' | 'success' | 'failed' | 'unknown'
  charged: boolean
  replayed: boolean
  used_count: number
  reserved_count: number
  remaining: number
  busy: boolean
  operation: 'receive' | 'send' | ''
  message: string
  error: string
}

export class PublicMailApiError extends Error {
  status: number
  code: number
  data: Record<string, unknown>

  constructor(message: string, status: number, code: number, data: Record<string, unknown> = {}) {
    super(message)
    this.name = 'PublicMailApiError'
    this.status = status
    this.code = code
    this.data = data
  }
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 120_000)
  try {
    const response = await fetch(url, {
      ...options,
      signal: controller.signal,
      headers: {
        'Content-Type': 'application/json',
        ...(options?.headers || {}),
      },
    })
    const result = await response.json().catch(() => ({ code: 500, msg: '请求失败', data: {} }))
    if (!response.ok || result.code !== 0) {
      throw new PublicMailApiError(result.msg || '请求失败', response.status, result.code || response.status, result.data || {})
    }
    return result.data as T
  } finally {
    clearTimeout(timer)
  }
}

function publicMailKeySegment(cardKey: string) {
  const value = cardKey.trim()
  const normalized = value.startsWith('keys=') ? value : `keys=${value}`
  return encodeURIComponent(normalized)
}

export function getPublicMailInfo(cardKey: string) {
  return request<PublicMailInfo>(`/api/public/mail/${publicMailKeySegment(cardKey)}`)
}

export function getPublicMailMessages(cardKey: string, email?: string) {
  return request<PublicMailMessagesResponse>(`/api/public/mail/${publicMailKeySegment(cardKey)}/messages`, {
    method: 'POST',
    body: JSON.stringify({ email: email || '' }),
  })
}

export function sendPublicMailMessage(cardKey: string, payload: PublicMailSendPayload) {
  // Never retry a POST automatically. A caller may query this request_id, or
  // explicitly resubmit the same identifier without sending/charging twice.
  return request<PublicMailSendResponse>(`/api/public/mail/${publicMailKeySegment(cardKey)}/send`, {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function newPublicMailRequestID() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
}

export function getPublicMailSendStatus(cardKey: string, requestID: string) {
  return request<PublicMailSendResponse>(`/api/public/mail/${publicMailKeySegment(cardKey)}/send/${encodeURIComponent(requestID)}`)
}

export async function getPublicMailPlain(cardKey: string, email?: string) {
  const query = new URLSearchParams()
  if (email) query.set('email', email)
  const suffix = query.toString() ? `?${query.toString()}` : ''
  const response = await fetch(`/api/public/mail/${publicMailKeySegment(cardKey)}/all${suffix}`, {
    headers: {
      Accept: 'text/plain',
    },
  })
  const text = await response.text()
  if (!response.ok) {
    throw new PublicMailApiError(text || '获取邮件失败', response.status, response.status)
  }
  return text
}
