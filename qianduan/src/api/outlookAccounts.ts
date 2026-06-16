export type OutlookGroup = {
  id: number
  parent_id: number
  name: string
  system: boolean
  sort_order: number
  count: number
  created_at: string
}

export type OutlookAccount = {
  id: number
  group_id: number
  group_name: string
  email: string
  client_id: string
  refresh_token: string
  remark: string
  status: string
  status_reason: string
  last_token_refresh_at: string
  created_at: string
}

export type OutlookAccountListResponse = {
  items: OutlookAccount[]
  total: number
  page: number
  page_size: number
  pages: number
  normal: number
  error: number
}

export type OutlookAccountListParams = {
  group_id?: number
  search?: string
  page?: number
  page_size?: number
  sort_by?: 'group' | 'email' | 'client' | 'created_at' | 'status' | 'remark' | 'id'
  sort_order?: 'asc' | 'desc'
  exclude_card_key_bound?: boolean
}

export type AccountListFilter = {
  group_id?: number
  search?: string
}

export type BackgroundTask = {
  id: string
  type: string
  status: 'running' | 'success' | 'failed' | 'partial'
  total: number
  done: number
  success: number
  failed: number
  message: string
  file_name?: string
  download_url?: string
  created_at: string
  updated_at: string
}

export type SaveOutlookAccountPayload = {
  email: string
  password?: string
  client_id: string
  refresh_token?: string
  group_id: number
  remark?: string
  status?: string
}

export type OutlookMessage = {
  id: string
  folder: string
  subject: string
  from: string
  to: string
  cc: string
  time: string
  timestamp: number
  body_preview: string
  body: string
  html: string
  is_read: boolean
  has_attachments: boolean
}

export type OutlookMessageList = {
  items: OutlookMessage[]
  total: number
  error?: string
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const isFormData = options?.body instanceof FormData
  const response = await fetch(url, {
    ...options,
    headers: isFormData
      ? options?.headers
      : {
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

async function requestBlob(url: string, options?: RequestInit): Promise<Blob> {
  const response = await fetch(url, options)
  if (!response.ok) {
    const result = await response.json().catch(() => ({ msg: '请求失败' }))
    throw new Error(result.msg || '请求失败')
  }
  return response.blob()
}

export function listOutlookGroups(params: { exclude_card_key_bound?: boolean } = {}) {
  const query = new URLSearchParams()
  if (params.exclude_card_key_bound) query.set('exclude_card_key_bound', '1')
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<OutlookGroup[]>(`/api/admin/outlook-groups${suffix}`)
}

export function createOutlookGroup(payload: { name: string; parent_id?: number; sort_order?: number }) {
  return request<OutlookGroup>('/api/admin/outlook-groups', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateOutlookGroup(id: number, payload: { name: string; sort_order?: number }) {
  return request<OutlookGroup>(`/api/admin/outlook-groups/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteOutlookGroup(id: number) {
  return request<void>(`/api/admin/outlook-groups/${id}`, {
    method: 'DELETE',
  })
}

export function listOutlookAccounts(params: OutlookAccountListParams = {}) {
  const query = new URLSearchParams()
  if (params.group_id) query.set('group_id', String(params.group_id))
  if (params.search) query.set('search', params.search)
  if (params.page) query.set('page', String(params.page))
  if (params.page_size) query.set('page_size', String(params.page_size))
  if (params.sort_by) query.set('sort_by', params.sort_by)
  if (params.sort_order) query.set('sort_order', params.sort_order)
  if (params.exclude_card_key_bound) query.set('exclude_card_key_bound', '1')
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<OutlookAccountListResponse>(`/api/admin/outlook-accounts${suffix}`)
}

export function createOutlookAccount(payload: SaveOutlookAccountPayload) {
  return request<OutlookAccount>('/api/admin/outlook-accounts', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function batchCreateOutlookAccounts(payload: { content: string; group_id: number }) {
  return request<OutlookAccount[]>('/api/admin/outlook-accounts/batch', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateOutlookAccount(id: number, payload: SaveOutlookAccountPayload) {
  return request<OutlookAccount>(`/api/admin/outlook-accounts/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteOutlookAccount(id: number) {
  return request<void>(`/api/admin/outlook-accounts/${id}`, {
    method: 'DELETE',
  })
}

export function batchOutlookAction(payload: { action: 'delete' | 'move' | 'test'; ids?: number[]; filter?: AccountListFilter; group_id?: number }) {
  return request<void | BackgroundTask>('/api/admin/outlook-accounts/batch-action', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function exportOutlookDataZip(ids: number[] = [], password: string, filter?: AccountListFilter) {
  return requestBlob('/api/admin/outlook-data/export', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ ids, password, filter }),
  })
}

export function createOutlookDataExportTask(ids: number[] = [], password: string, filter?: AccountListFilter) {
  return request<BackgroundTask>('/api/admin/outlook-data/export-task', {
    method: 'POST',
    body: JSON.stringify({ ids, password, filter }),
  })
}

export function downloadBackgroundTaskResult(id: string) {
  return requestBlob(`/api/admin/tasks/${encodeURIComponent(id)}/download`)
}

export function getBackgroundTask(id: string) {
  return request<BackgroundTask>(`/api/admin/tasks/${encodeURIComponent(id)}`)
}

export function importOutlookDataZip(file: File, password: string) {
  const formData = new FormData()
  formData.append('file', file)
  formData.append('password', password)
  return request<{ groups: number; accounts: number }>('/api/admin/outlook-data/import', {
    method: 'POST',
    body: formData,
  })
}

export function createOutlookDataImportTask(file: File, password: string) {
  const formData = new FormData()
  formData.append('file', file)
  formData.append('password', password)
  return request<BackgroundTask>('/api/admin/outlook-data/import-task', {
    method: 'POST',
    body: formData,
  })
}

export function testOutlookAccount(id: number) {
  return request<{ message: string }>(`/api/admin/outlook-accounts/${id}/test`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
}

export function getOutlookAuthorizeURL(params: { client_id?: string; login_hint?: string } = {}) {
  const query = new URLSearchParams()
  if (params.client_id) query.set('client_id', params.client_id)
  if (params.login_hint) query.set('login_hint', params.login_hint)
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<{ url: string; client_id: string; redirect_uri: string; state?: string }>(`/api/admin/outlook-oauth/authorize${suffix}`)
}

export function getOutlookOAuthResult(state: string) {
  return request<{ status: 'pending' | 'success' | 'not_found'; client_id?: string; refresh_token?: string }>(`/api/admin/outlook-oauth/result?state=${encodeURIComponent(state)}`)
}

export function exchangeOutlookCode(payload: { code: string; client_id?: string; redirect_uri?: string }) {
  return request<{ client_id: string; refresh_token: string }>('/api/admin/outlook-oauth/exchange', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function listOutlookMessages(id: number, params: { folder: string; top: number; skip: number; keyword?: string }) {
  const query = new URLSearchParams()
  query.set('folder', params.folder)
  query.set('top', String(params.top))
  query.set('skip', String(params.skip))
  if (params.keyword) query.set('keyword', params.keyword)
  return request<OutlookMessageList>(`/api/admin/outlook-accounts/${id}/messages?${query.toString()}`)
}

export function getOutlookMessageDetails(id: number, ids: string[]) {
  return request<OutlookMessage[]>(`/api/admin/outlook-accounts/${id}/messages/batch-detail`, {
    method: 'POST',
    body: JSON.stringify({ ids }),
  })
}

export function getOutlookMessageDetail(id: number, messageID: string) {
  return request<OutlookMessage>(`/api/admin/outlook-accounts/${id}/messages/${encodeURIComponent(messageID)}`)
}
