export type QuickMailMessage = {
  id: string
  source: 'imap' | 'outlook'
  uid?: number
  folder: 'inbox' | 'trash'
  mailbox?: string
  subject: string
  from: string
  to: string
  cc?: string
  time: string
  timestamp: number
  body: string
  html: string
  body_preview?: string
  is_read?: boolean
  has_attachments?: boolean
}

export type QuickMailReceiveResult = {
  inbox: QuickMailMessage[]
  trash: QuickMailMessage[]
}

export type QuickMailKeyStatus = {
  configured: boolean
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options?.headers || {}),
    },
  })
  const result = await response.json().catch(() => ({ code: 500, msg: '请求失败' }))
  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '请求失败')
  }
  return result.data as T
}

export function getQuickMailKeyStatus() {
  return request<QuickMailKeyStatus>('/api/user/quick-mail-key')
}

export function updateQuickMailKey(key: string) {
  return request<QuickMailKeyStatus>('/api/user/quick-mail-key', {
    method: 'PUT',
    body: JSON.stringify({ key }),
  })
}

export function receiveQuickMail(mode: 'imap' | 'outlook', payload: { email: string; limit: number; admin_key?: string }) {
  return request<QuickMailReceiveResult>(`/api/admin/quick-mail/${mode}/receive`, {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}
