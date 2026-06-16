export type CardKeyUseLog = {
  id: number
  card_key: string
  bound_email: string
  mail_subject: string
  user_ip: string
  used_at: string
}

export type CardKeyUseLogListParams = {
  search?: string
  page?: number
  page_size?: number
}

export type CardKeyUseLogListResponse = {
  items: CardKeyUseLog[]
  total: number
  page: number
  page_size: number
  pages: number
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

export function listCardKeyUseLogs(params: CardKeyUseLogListParams = {}) {
  const query = new URLSearchParams()
  if (params.search) query.set('search', params.search)
  if (params.page) query.set('page', String(params.page))
  if (params.page_size) query.set('page_size', String(params.page_size))
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<CardKeyUseLogListResponse>(`/api/admin/card-key-logs${suffix}`)
}

export function clearCardKeyUseLogs() {
  return request<{ count: number }>('/api/admin/card-key-logs', {
    method: 'DELETE',
  })
}
