export type BackupScheduleFrequency = 'daily' | 'interval_days' | 'weekly' | 'monthly'

export type SystemSettings = {
  site_name: string
  site_logo: string
  site_subtitle: string
  table_default_page_size: number
  table_page_size_options: number[]
  card_key_log_cleanup_days: string
  backup_schedule_enabled: boolean
  backup_schedule_frequency: BackupScheduleFrequency
  backup_schedule_time: string
  backup_schedule_interval_days: number
  backup_schedule_weekday: number
  backup_schedule_month_day: number
  backup_schedule_retain_count: number
  backup_webdav_enabled: boolean
  backup_webdav_url: string
  backup_webdav_username: string
  backup_webdav_password: string
  backup_webdav_remote_dir: string
}

export type UpdateCheckStatus = 'latest' | 'outdated' | 'error'

export type UpdateCheckResult = {
  current_version: string
  latest_version: string
  has_update: boolean
  status: UpdateCheckStatus
  source_url: string
  release_url: string
  message: string
  checked_at: string
}

export type DatabaseRestoreResult = {
  message: string
  restart_scheduled?: boolean
}

export type DatabaseBackupFile = {
  name: string
  size: number
  created_at: string
  modified_at: string
  directory: string
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

export type DatabaseBackupTaskPayload = Pick<
  SystemSettings,
  | 'backup_schedule_retain_count'
  | 'backup_webdav_enabled'
  | 'backup_webdav_url'
  | 'backup_webdav_username'
  | 'backup_webdav_password'
  | 'backup_webdav_remote_dir'
>

export type WebDAVTestResult = {
  message: string
}

async function readErrorMessage(response: Response) {
  const text = await response.text()
  if (!text) {
    return '\u8bf7\u6c42\u5931\u8d25'
  }
  try {
    const result = JSON.parse(text) as { msg?: string }
    return result.msg || '\u8bf7\u6c42\u5931\u8d25'
  } catch {
    return text
  }
}

async function requestBlob(url: string, options?: RequestInit): Promise<Blob> {
  const response = await fetch(url, options)
  if (!response.ok) {
    throw new Error(await readErrorMessage(response))
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
  const text = await response.text()
  let result: { code?: number; msg?: string; data?: T }

  try {
    result = text ? JSON.parse(text) : {}
  } catch {
    throw new Error(text || '\u63a5\u53e3\u8fd4\u56de\u683c\u5f0f\u9519\u8bef')
  }

  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '\u8bf7\u6c42\u5931\u8d25')
  }
  return result.data as T
}

export function getAdminSettings() {
  return request<SystemSettings>('/api/admin/settings')
}

export function updateAdminSettings(payload: Partial<SystemSettings>) {
  return request<SystemSettings>('/api/admin/settings', {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function checkAppUpdate(forceRefresh = false) {
  return request<UpdateCheckResult>(`/api/admin/update-check${forceRefresh ? '?refresh=1' : ''}`)
}

export function exportDatabaseBackup() {
  return requestBlob('/api/admin/database-backup/export')
}

export function listDatabaseBackupFiles() {
  return request<DatabaseBackupFile[]>('/api/admin/database-backup/files')
}

export function downloadDatabaseBackupFile(name: string) {
  return requestBlob(`/api/admin/database-backup/files/${encodeURIComponent(name)}`)
}

export function deleteDatabaseBackupFile(name: string) {
  return request<null>(`/api/admin/database-backup/files/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  })
}

export function createDatabaseBackupTask(payload: DatabaseBackupTaskPayload) {
  return request<BackgroundTask>('/api/admin/database-backup/manual-task', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function testDatabaseBackupWebDAV(payload: DatabaseBackupTaskPayload) {
  return request<WebDAVTestResult>('/api/admin/database-backup/webdav-test', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function restoreDatabaseBackup(file: File) {
  const formData = new FormData()
  formData.append('file', file)
  return request<DatabaseRestoreResult>('/api/admin/database-backup/restore', {
    method: 'POST',
    body: formData,
  })
}
