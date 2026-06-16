import { computed, ref } from 'vue'

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
  result_cleanup_after?: string
  created_at: string
  updated_at: string
}

export type TaskTrackOptions = {
  title?: string
  onSettled?: (task: BackgroundTask) => Promise<void> | void
}

type TrackedTask = BackgroundTask & {
  title: string
  tracked_at: number
}

const taskStorageKey = 'admin_task_center_tasks_v1'
const completedTaskRetentionMs = 24 * 60 * 60 * 1000
const tasks = ref<TrackedTask[]>(readCachedTasks())
const callbacks = new Map<string, TaskTrackOptions>()
const pollingTimers = new Map<string, number>()
const cleanupRefreshTimers = new Map<string, number>()
const cleanupRefreshDeadlines = new Map<string, number>()
const settledCallbacks = new Set<string>()

const orderedTasks = computed(() =>
  [...tasks.value].sort((a, b) => Number(b.status === 'running') - Number(a.status === 'running') || Date.parse(b.updated_at || '') - Date.parse(a.updated_at || '') || b.tracked_at - a.tracked_at)
)
const runningTasks = computed(() => tasks.value.filter((task) => task.status === 'running'))
const finishedTasks = computed(() => tasks.value.filter((task) => task.status !== 'running'))

function readCachedTasks() {
  if (typeof localStorage === 'undefined') return []
  try {
    const value = JSON.parse(localStorage.getItem(taskStorageKey) || '[]')
    if (!Array.isArray(value)) return []
    return value.filter((task) => task && typeof task.id === 'string').slice(0, 20) as TrackedTask[]
  } catch {
    return []
  }
}

function saveTasks() {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(taskStorageKey, JSON.stringify(tasks.value.slice(0, 20)))
  } catch {
    // Ignore storage quota errors; in-memory tasks still work.
  }
}

function defaultTaskTitle(task: Pick<BackgroundTask, 'type'>) {
  const titles: Record<string, string> = {
    mail_import: 'IMAP 导入',
    mail_export: 'IMAP 导出',
    mail_delete: 'IMAP 批量删除',
    mail_test: 'IMAP 批量测试',
    mail_batch_delete: 'IMAP 批量删除',
    mail_batch_test: 'IMAP 批量测试',
    outlook_import: '微软邮箱导入',
    outlook_export: '微软邮箱导出',
    outlook_delete: '微软邮箱批量删除',
    outlook_test: '微软邮箱批量测试',
    outlook_batch_delete: '微软邮箱批量删除',
    outlook_batch_test: '微软邮箱批量测试',
    outlook_batch_move: '微软邮箱分组调整',
    database_backup: '数据库备份',
  }
  return titles[task.type] || '后台任务'
}

function upsertTask(task: BackgroundTask, title?: string) {
  const tracked: TrackedTask = {
    ...task,
    title: title || tasks.value.find((item) => item.id === task.id)?.title || defaultTaskTitle(task),
    tracked_at: tasks.value.find((item) => item.id === task.id)?.tracked_at || Date.now(),
  }
  const index = tasks.value.findIndex((item) => item.id === task.id)
  if (index >= 0) {
    tasks.value.splice(index, 1, tracked)
  } else {
    tasks.value.unshift(tracked)
  }
  tasks.value = orderedTasks.value.slice(0, 20)
  saveTasks()
  return tracked
}

async function requestTask(id: string) {
  const response = await fetch(`/api/admin/tasks/${encodeURIComponent(id)}`)
  const result = await response.json().catch(() => ({ code: 500, msg: '任务状态获取失败' }))
  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '任务状态获取失败')
  }
  return result.data as BackgroundTask
}

async function requestTasks() {
  const response = await fetch('/api/admin/tasks?limit=20')
  const result = await response.json().catch(() => ({ code: 500, msg: '任务列表获取失败' }))
  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '任务列表获取失败')
  }
  return (result.data || []) as BackgroundTask[]
}

async function requestTaskBlob(id: string) {
  const response = await fetch(`/api/admin/tasks/${encodeURIComponent(id)}/download`)
  if (!response.ok) {
    const result = await response.json().catch(() => ({ msg: '任务结果下载失败' }))
    throw new Error(result.msg || '任务结果下载失败')
  }
  return response.blob()
}

async function requestDeleteTask(id: string) {
  const response = await fetch(`/api/admin/tasks/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
  const result = await response.json().catch(() => ({ code: 500, msg: '任务清理失败' }))
  if (response.status === 404) {
    return
  }
  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '任务清理失败')
  }
}

function progressPercent(task: Pick<BackgroundTask, 'total' | 'done' | 'status'>) {
  if (task.status !== 'running' && task.total > 0) return 100
  if (!task.total) return task.status === 'running' ? 8 : 100
  return Math.max(0, Math.min(100, Math.round((task.done / task.total) * 100)))
}

function progressText(task: Pick<BackgroundTask, 'total' | 'done' | 'status'>) {
  if (!task.total) return task.status === 'running' ? '准备中' : '完成'
  return `${Math.min(task.done, task.total)} / ${task.total}`
}

function statusLabel(status: BackgroundTask['status']) {
  if (status === 'running') return '进行中'
  if (status === 'success') return '成功'
  if (status === 'partial') return '部分完成'
  return '失败'
}

function trackTask(task: BackgroundTask, options: TaskTrackOptions = {}) {
  callbacks.set(task.id, options)
  const tracked = upsertTask(task, options.title)
  if (task.status === 'running') {
    startPolling(task.id)
  } else {
    scheduleTaskExpiry(tracked)
    void runSettledCallback(tracked)
  }
}

async function refreshTasks() {
  const latestTasks = await requestTasks()
  for (const task of latestTasks) {
    const tracked = upsertTask(task)
    if (tracked.status === 'running') {
      startPolling(tracked.id)
    } else {
      scheduleTaskExpiry(tracked)
    }
  }
}

function startPolling(id: string) {
  if (pollingTimers.has(id)) return

  const poll = async () => {
    try {
      const latest = await requestTask(id)
      const tracked = upsertTask(latest)
      if (tracked.status === 'running') {
        pollingTimers.set(id, window.setTimeout(poll, 1000))
        return
      }
      pollingTimers.delete(id)
      scheduleTaskExpiry(tracked)
      await runSettledCallback(tracked)
    } catch {
      pollingTimers.set(id, window.setTimeout(poll, 2500))
    }
  }

  pollingTimers.set(id, window.setTimeout(poll, 600))
}

async function runSettledCallback(task: BackgroundTask) {
  if (settledCallbacks.has(task.id)) return
  settledCallbacks.add(task.id)
  await callbacks.get(task.id)?.onSettled?.(task)
}

async function downloadTask(task: BackgroundTask) {
  const blob = await requestTaskBlob(task.id)
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = task.file_name || `${task.type || 'task'}-${task.id.slice(0, 8)}.zip`
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
  const latest = await requestTask(task.id).catch(() => null)
  if (latest) {
    const tracked = upsertTask(latest)
    scheduleTaskExpiry(tracked)
  }
}

function taskExpiryDeadline(task: BackgroundTask | TrackedTask) {
  if (task.status === 'running') return null
  const resultCleanupAt = Date.parse(task.result_cleanup_after || '')
  if (Number.isFinite(resultCleanupAt)) return resultCleanupAt
  const updatedAt = Date.parse(task.updated_at || task.created_at || '')
  if (Number.isFinite(updatedAt)) return updatedAt + completedTaskRetentionMs
  return ('tracked_at' in task ? task.tracked_at : Date.now()) + completedTaskRetentionMs
}

function clearPollingTimer(id: string) {
  const timer = pollingTimers.get(id)
  if (timer) {
    window.clearTimeout(timer)
    pollingTimers.delete(id)
  }
}

function clearCleanupTimer(id: string) {
  const cleanupTimer = cleanupRefreshTimers.get(id)
  if (cleanupTimer) {
    window.clearTimeout(cleanupTimer)
    cleanupRefreshTimers.delete(id)
  }
  cleanupRefreshDeadlines.delete(id)
}

function forgetTask(id: string) {
  clearPollingTimer(id)
  clearCleanupTimer(id)
  callbacks.delete(id)
  settledCallbacks.delete(id)
  tasks.value = tasks.value.filter((task) => task.id !== id)
  saveTasks()
}

function scheduleTaskExpiry(task: BackgroundTask | TrackedTask) {
  const deadline = taskExpiryDeadline(task)
  if (!deadline || !Number.isFinite(deadline)) return
  const existingDeadline = cleanupRefreshDeadlines.get(task.id)
  if (existingDeadline && Math.abs(existingDeadline - deadline) < 1000) return
  clearCleanupTimer(task.id)
  const delay = deadline - Date.now() + 1500
  cleanupRefreshDeadlines.set(task.id, deadline)
  cleanupRefreshTimers.set(
    task.id,
    window.setTimeout(async () => {
      cleanupRefreshTimers.delete(task.id)
      cleanupRefreshDeadlines.delete(task.id)
      await requestDeleteTask(task.id).catch(() => undefined)
      forgetTask(task.id)
    }, Math.max(0, delay))
  )
}

async function removeTask(id: string) {
  const task = tasks.value.find((item) => item.id === id)
  forgetTask(id)
  if (task?.status !== 'running') {
    await requestDeleteTask(id)
  }
}

async function clearFinishedTasks() {
  const finished = [...finishedTasks.value]
  for (const task of finished) {
    clearPollingTimer(task.id)
    clearCleanupTimer(task.id)
    callbacks.delete(task.id)
    settledCallbacks.delete(task.id)
  }
  tasks.value = tasks.value.filter((task) => task.status === 'running')
  saveTasks()
  const results = await Promise.allSettled(finished.map((task) => requestDeleteTask(task.id)))
  const failed = results.find((result) => result.status === 'rejected')
  if (failed?.status === 'rejected') {
    throw failed.reason instanceof Error ? failed.reason : new Error('任务清理失败')
  }
}

function resumeRunningTasks() {
  for (const task of tasks.value) {
    if (task.status === 'running') {
      startPolling(task.id)
    } else {
      scheduleTaskExpiry(task)
    }
  }
}

resumeRunningTasks()

export function useTaskStore() {
  return {
    tasks,
    orderedTasks,
    runningTasks,
    finishedTasks,
    trackTask,
    progressPercent,
    progressText,
    statusLabel,
    downloadTask,
    removeTask,
    clearFinishedTasks,
    refreshTasks,
  }
}
