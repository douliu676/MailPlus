export type MailGroup = {
  id: number
  parent_id: number
  name: string
  system: boolean
  sort_order: number
  count: number
  created_at: string
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

export function listMailGroups(params: { exclude_card_key_bound?: boolean } = {}) {
  const query = new URLSearchParams()
  if (params.exclude_card_key_bound) query.set('exclude_card_key_bound', '1')
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<MailGroup[]>(`/api/admin/mail-groups${suffix}`)
}

export function createMailGroup(payload: { name: string; parent_id?: number; sort_order?: number }) {
  return request<MailGroup>('/api/admin/mail-groups', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateMailGroup(id: number, payload: { name: string; sort_order?: number }) {
  return request<MailGroup>(`/api/admin/mail-groups/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteMailGroup(id: number) {
  return request<void>(`/api/admin/mail-groups/${id}`, {
    method: 'DELETE',
  })
}
