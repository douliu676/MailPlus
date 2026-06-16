export type CardKeyStatus = 'unused' | 'used' | 'disabled'

export type CardKeyGroup = {
  id: number
  name: string
  sort_order: number
  count: number
  created_at: string
}

export type SaveCardKeyGroupPayload = {
  name: string
  sort_order?: number
}

export type CardKey = {
  id: number
  group_id: number
  group_name: string
  key: string
  amount: number
  status: CardKeyStatus
  used_by: string
  used_at: string
  usage_limit: number
  used_count: number
  mail_days: number
  mail_days_blank: boolean
  mail_keyword: string
  bound_email: string
  remark: string
  created_at: string
  updated_at: string
}

export type CardKeyListParams = {
  group_id?: number
  search?: string
  status?: CardKeyStatus | ''
  page?: number
  page_size?: number
  sort_by?: 'id' | 'group' | 'key' | 'amount' | 'usage_limit' | 'status' | 'used_by' | 'used_at' | 'bound_email' | 'mail_filter' | 'created_at' | 'remark'
  sort_order?: 'asc' | 'desc'
}

export type CardKeyListResponse = {
  items: CardKey[]
  total: number
  page: number
  page_size: number
  pages: number
  unused: number
  used: number
  disabled: number
}

export type SaveCardKeyPayload = {
  group_id: number
  key: string
  amount?: number
  status: CardKeyStatus
  used_by?: string
  usage_limit: number
  mail_days: number
  mail_days_blank?: boolean
  mail_keyword?: string
  bound_email?: string
  remark?: string
}

export type BatchCardKeyPayload = {
  group_id: number
  count: number
  amount?: number
  status?: CardKeyStatus
  usage_limit: number
  mail_days: number
  mail_days_blank?: boolean
  mail_keyword?: string
  bound_email?: string
  remark?: string
}

export type CardKeyBatchActionPayload = {
  action: 'delete' | 'unbind_email'
  ids?: number[]
}

export type CardKeyBatchActionResult = {
  count: number
  skipped?: number
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

export function listCardKeyGroups() {
  return request<CardKeyGroup[]>('/api/admin/card-key-groups')
}

export function createCardKeyGroup(payload: SaveCardKeyGroupPayload) {
  return request<CardKeyGroup>('/api/admin/card-key-groups', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateCardKeyGroup(id: number, payload: SaveCardKeyGroupPayload) {
  return request<CardKeyGroup>(`/api/admin/card-key-groups/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteCardKeyGroup(id: number) {
  return request<void>(`/api/admin/card-key-groups/${id}`, {
    method: 'DELETE',
  })
}

export function listCardKeys(params: CardKeyListParams = {}) {
  const query = new URLSearchParams()
  if (params.group_id) query.set('group_id', String(params.group_id))
  if (params.search) query.set('search', params.search)
  if (params.status) query.set('status', params.status)
  if (params.page) query.set('page', String(params.page))
  if (params.page_size) query.set('page_size', String(params.page_size))
  if (params.sort_by) query.set('sort_by', params.sort_by)
  if (params.sort_order) query.set('sort_order', params.sort_order)
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<CardKeyListResponse>(`/api/admin/card-keys${suffix}`)
}

export function createCardKey(payload: SaveCardKeyPayload) {
  return request<CardKey>('/api/admin/card-keys', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function batchCreateCardKeys(payload: BatchCardKeyPayload) {
  return request<CardKey[]>('/api/admin/card-keys/batch', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateCardKey(id: number, payload: SaveCardKeyPayload) {
  return request<CardKey>(`/api/admin/card-keys/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteCardKey(id: number) {
  return request<void>(`/api/admin/card-keys/${id}`, {
    method: 'DELETE',
  })
}

export function batchCardKeyAction(payload: CardKeyBatchActionPayload) {
  return request<CardKeyBatchActionResult>('/api/admin/card-keys/batch-action', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}
