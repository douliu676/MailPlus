export type QuickMailMessage = {
  id: string
  source: 'imap' | 'outlook'
  uid?: number
  uid_validity?: number
  message_id?: string
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
  body_loaded?: boolean
}

export type QuickMailReceiveResult = {
  inbox: QuickMailMessage[]
  trash: QuickMailMessage[]
  warnings?: string[]
}

export type QuickMailMode = 'imap' | 'outlook'
export type QuickMailIdentity = { email: string; admin_key?: string }
export type QuickMailReceivePayload = QuickMailIdentity & { limit: number }

export type QuickMailKeyStatus = {
  configured: boolean
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const controller = new AbortController()
  const parent = options?.signal
  const abort = () => controller.abort()
  let timedOut = false
  const timer = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, 120_000)
  if (parent?.aborted) abort()
  else parent?.addEventListener('abort', abort, { once: true })
  try {
    if (controller.signal.aborted) throw new DOMException('请求已取消', 'AbortError')
    const response = await fetch(url, {
      ...options,
      signal: controller.signal,
      headers: {
        'Content-Type': 'application/json',
        ...(options?.headers || {}),
      },
    })
    const result = await response.json().catch(() => ({ code: response.status || 500, msg: '请求失败' }))
    if (!response.ok || result.code !== 0) {
      if (response.status === 504 || response.status === 408 || /backend request timeout/i.test(result.msg || '')) {
        throw new Error('读取邮件超时，请稍后重试')
      }
      throw new Error(result.msg || '请求失败')
    }
    return result.data as T
  } catch (error) {
    if (parent?.aborted) throw new DOMException('请求已取消', 'AbortError')
    if (timedOut) throw new Error('读取邮件超时，请稍后重试')
    throw error
  } finally {
    clearTimeout(timer)
    parent?.removeEventListener('abort', abort)
  }
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

export function receiveQuickMail(mode: QuickMailMode, payload: QuickMailReceivePayload, signal?: AbortSignal) {
  return request<QuickMailReceiveResult>(`/api/admin/quick-mail/${mode}/receive`, {
    method: 'POST',
    body: JSON.stringify({ ...payload, include_body: false }),
    signal,
  })
}

export function receiveQuickMailDetail(mode: QuickMailMode, identity: QuickMailIdentity, message: QuickMailMessage, signal?: AbortSignal) {
  return request<QuickMailMessage>(`/api/admin/quick-mail/${mode}/detail`, {
    method: 'POST',
    body: JSON.stringify({
      ...identity,
      uid: message.uid,
      uid_validity: message.uid_validity,
      mailbox: message.mailbox,
      folder: message.folder,
      message_id: message.message_id,
    }),
    signal,
  })
}
