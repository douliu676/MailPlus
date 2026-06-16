export type MailServer = {
  id: number
  name: string
  imap_host: string
  smtp_host: string
  created_at: string
}

export type SaveMailServerPayload = {
  name: string
  imap_host: string
  smtp_host: string
}

export type MailAccount = {
  id: number
  group_id: number
  group_name: string
  email: string
  server_id: number
  server_name: string
  imap_host: string
  smtp_host: string
  imap_protocol: string
  imap_port: number
  imap_ssl: boolean
  smtp_protocol: string
  smtp_port: number
  smtp_ssl: boolean
  remark: string
  status: string
  status_reason: string
  created_at: string
}

export type MailAccountListResponse = {
  items: MailAccount[]
  total: number
  page: number
  page_size: number
  pages: number
  normal: number
  error: number
}

export type MailAccountListParams = {
  group_id?: number
  search?: string
  page?: number
  page_size?: number
  sort_by?: 'group' | 'email' | 'server' | 'created_at' | 'status' | 'remark' | 'id'
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

export type SaveMailAccountPayload = {
  email: string
  password: string
  group_id: number
  server_id: number
  imap_host: string
  smtp_host: string
  imap_protocol: string
  imap_port: number
  imap_ssl: boolean
  smtp_protocol: string
  smtp_port: number
  smtp_ssl: boolean
  remark?: string
}

export type BatchMailAccountPayload = Omit<SaveMailAccountPayload, 'email' | 'password' | 'remark'> & {
  content: string
}

export type ReceivedMailMessage = {
  uid: number
  folder: 'inbox' | 'trash'
  mailbox: string
  subject: string
  from: string
  to: string
  time: string
  timestamp: number
}

export type ReceiveMailMessagesResult = {
  inbox: ReceivedMailMessage[]
  trash: ReceivedMailMessage[]
}

export type ReceivedMailDetail = ReceivedMailMessage & {
  body: string
  html: string
}

export type SendMailMessagePayload = {
  nickname?: string
  recipient: string
  subject?: string
  body?: string
}

async function requestBlob(url: string, options?: RequestInit): Promise<Blob> {
  const response = await fetch(url, options)
  if (!response.ok) {
    const result = await response.json().catch(() => ({ msg: '请求失败' }))
    throw new Error(result.msg || '请求失败')
  }
  return response.blob()
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

export function listMailServers() {
  return request<MailServer[]>('/api/admin/mail-servers')
}

export function createMailServer(payload: SaveMailServerPayload) {
  return request<MailServer>('/api/admin/mail-servers', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateMailServer(id: number, payload: SaveMailServerPayload) {
  return request<MailServer>(`/api/admin/mail-servers/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteMailServer(id: number) {
  return request<void>(`/api/admin/mail-servers/${id}`, {
    method: 'DELETE',
  })
}

export function listMailAccounts(params: MailAccountListParams = {}) {
  const query = new URLSearchParams()
  if (params.group_id) query.set('group_id', String(params.group_id))
  if (params.search) query.set('search', params.search)
  if (params.page) query.set('page', String(params.page))
  if (params.page_size) query.set('page_size', String(params.page_size))
  if (params.sort_by) query.set('sort_by', params.sort_by)
  if (params.sort_order) query.set('sort_order', params.sort_order)
  if (params.exclude_card_key_bound) query.set('exclude_card_key_bound', '1')
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<MailAccountListResponse>(`/api/admin/mail-accounts${suffix}`)
}

export function createMailAccount(payload: SaveMailAccountPayload) {
  return request<MailAccount>('/api/admin/mail-accounts', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function batchCreateMailAccounts(payload: BatchMailAccountPayload) {
  return request<MailAccount[]>('/api/admin/mail-accounts/batch', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateMailAccount(id: number, payload: SaveMailAccountPayload) {
  return request<MailAccount>(`/api/admin/mail-accounts/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteMailAccount(id: number) {
  return request<void>(`/api/admin/mail-accounts/${id}`, {
    method: 'DELETE',
  })
}

export function batchMailAction(payload: { action: 'delete' | 'test'; ids?: number[]; filter?: AccountListFilter; test_type?: 'all' | 'receive' | 'send' }) {
  return request<BackgroundTask>('/api/admin/mail-accounts/batch-action', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function getBackgroundTask(id: string) {
  return request<BackgroundTask>(`/api/admin/tasks/${encodeURIComponent(id)}`)
}

export function testMailAccount(id: number, type: 'all' | 'receive' | 'send') {
  return request<{ message: string }>(`/api/admin/mail-accounts/${id}/test`, {
    method: 'POST',
    body: JSON.stringify({ type }),
  })
}

export function receiveMailMessages(id: number, limit: number) {
  return request<ReceiveMailMessagesResult>(`/api/admin/mail-accounts/${id}/receive`, {
    method: 'POST',
    body: JSON.stringify({ limit }),
  })
}

export function receiveMailDetail(id: number, message: Pick<ReceivedMailMessage, 'uid' | 'mailbox' | 'folder'>) {
  return request<ReceivedMailDetail>(`/api/admin/mail-accounts/${id}/receive/detail`, {
    method: 'POST',
    body: JSON.stringify({ uid: message.uid, mailbox: message.mailbox, folder: message.folder }),
  })
}

export function sendMailAccountMessage(id: number, payload: SendMailMessagePayload) {
  return request<{ message: string }>(`/api/admin/mail-accounts/${id}/send`, {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function exportMailDataZip(ids: number[] = [], password: string, filter?: AccountListFilter) {
  return requestBlob('/api/admin/mail-data/export', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ ids, password, filter }),
  })
}

export function createMailDataExportTask(ids: number[] = [], password: string, filter?: AccountListFilter) {
  return request<BackgroundTask>('/api/admin/mail-data/export-task', {
    method: 'POST',
    body: JSON.stringify({ ids, password, filter }),
  })
}

export function downloadBackgroundTaskResult(id: string) {
  return requestBlob(`/api/admin/tasks/${encodeURIComponent(id)}/download`)
}

export function importMailDataZip(file: File, password: string) {
  const formData = new FormData()
  formData.append('file', file)
  formData.append('password', password)
  return request<{ groups: number; accounts: number }>('/api/admin/mail-data/import', {
    method: 'POST',
    body: formData,
  })
}

export function createMailDataImportTask(file: File, password: string) {
  const formData = new FormData()
  formData.append('file', file)
  formData.append('password', password)
  return request<BackgroundTask>('/api/admin/mail-data/import-task', {
    method: 'POST',
    body: formData,
  })
}
