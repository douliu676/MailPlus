<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  Database,
  Download,
  Home,
  Save,
  Trash2,
  Upload,
  X,
} from 'lucide-vue-next'
import {
  createDatabaseBackupTask,
  deleteDatabaseBackupFile,
  downloadDatabaseBackupFile,
  exportDatabaseBackup,
  getAdminSettings,
  listDatabaseBackupFiles,
  restoreDatabaseBackup,
  testDatabaseBackupWebDAV,
  updateAdminSettings,
  type BackgroundTask,
  type DatabaseBackupFile,
  type DatabaseBackupTaskPayload,
  type BackupScheduleFrequency,
  type SystemSettings,
} from '../api/adminSettings'
import { clearAuthSession } from '../api/session'
import router from '../router'
import { useAppStore } from '../stores/app'
import { useTaskStore } from '../stores/tasks'

type SettingsTab = 'general' | 'backup'

const appStore = useAppStore()
const taskStore = useTaskStore()
const settingsPageCacheKey = 'system_settings_cache_v3'
const activeTab = ref<SettingsTab>('general')
const loading = ref(true)
const saving = ref(false)
const backingUp = ref(false)
const manualBackupStarting = ref(false)
const restoring = ref(false)
const testingWebDAV = ref(false)
const logoUploadError = ref('')
const restoreModalOpen = ref(false)
const restoreFile = ref<File | null>(null)
const restoreFileInput = ref<HTMLInputElement | null>(null)
const backupFilesModalOpen = ref(false)
const backupFilesLoading = ref(false)
const backupFileActionName = ref('')
const backupFiles = ref<DatabaseBackupFile[]>([])
const tablePageSizeOptionsInput = ref('10, 20, 50, 100')

const tabs = [
  { key: 'general' as const, label: '通用设置', icon: Home },
  { key: 'backup' as const, label: '数据备份', icon: Database },
]

const scheduleFrequencyOptions: Array<{ value: BackupScheduleFrequency; label: string }> = [
  { value: 'daily', label: '每天' },
  { value: 'interval_days', label: 'N天' },
  { value: 'weekly', label: '每周' },
  { value: 'monthly', label: '每月' },
]

const scheduleWeekdayOptions = [
  { value: 1, label: '周一' },
  { value: 2, label: '周二' },
  { value: 3, label: '周三' },
  { value: 4, label: '周四' },
  { value: 5, label: '周五' },
  { value: 6, label: '周六' },
  { value: 7, label: '周日' },
]

const form = reactive<SystemSettings>({
  site_name: '\u90ae\u7bb1\u7ba1\u7406\u7cfb\u7edf',
  site_logo: '',
  site_subtitle: '\u6279\u91cf\u8d26\u53f7\u4e0e\u4efb\u52a1\u7ba1\u7406\u5e73\u53f0',
  table_default_page_size: 20,
  table_page_size_options: [10, 20, 50, 100],
  card_key_log_cleanup_days: '',
  backup_schedule_enabled: false,
  backup_schedule_frequency: 'daily',
  backup_schedule_time: '03:00',
  backup_schedule_interval_days: 1,
  backup_schedule_weekday: 1,
  backup_schedule_month_day: 1,
  backup_schedule_retain_count: 3,
  backup_webdav_enabled: false,
  backup_webdav_url: '',
  backup_webdav_username: '',
  backup_webdav_password: '',
  backup_webdav_remote_dir: '/MailPlus',
})

function assignSettings(settings: SystemSettings) {
  Object.assign(form, settings)
  tablePageSizeOptionsInput.value = (form.table_page_size_options || []).join(', ')
}

function restoreSettingsCache() {
  try {
    const value = JSON.parse(localStorage.getItem(settingsPageCacheKey) || 'null')
    if (!value || typeof value !== 'object') return false
    assignSettings(value.settings || value)
    return true
  } catch {
    // Ignore stale cache.
  }
  return false
}

function saveSettingsCache(settings: SystemSettings) {
  try {
    localStorage.setItem(
      settingsPageCacheKey,
      JSON.stringify({
        settings,
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; live settings remain available.
  }
}

function parseNumberList(value: string) {
  return Array.from(
    new Set(
      value
        .split(/[,，\s]+/)
        .map((item) => Number(item.trim()))
        .filter((item) => Number.isFinite(item) && item > 0)
    )
  )
}

function clampWholeNumber(value: unknown, min: number, max: number, fallback = min) {
  const numberValue = Math.floor(Number(value))
  if (!Number.isFinite(numberValue)) {
    return fallback
  }
  return Math.min(max, Math.max(min, numberValue))
}

function padTimePart(value: number) {
  return String(value).padStart(2, '0')
}

function scheduleTimeParts() {
  const [rawHour, rawMinute] = String(form.backup_schedule_time || '').split(':')
  return {
    hour: clampWholeNumber(rawHour, 0, 23, 1),
    minute: clampWholeNumber(rawMinute, 0, 59, 30),
  }
}

function updateScheduleTime(part: 'hour' | 'minute', value: unknown) {
  const current = scheduleTimeParts()
  const hour = part === 'hour' ? clampWholeNumber(value, 0, 23, current.hour) : current.hour
  const minute = part === 'minute' ? clampWholeNumber(value, 0, 59, current.minute) : current.minute
  form.backup_schedule_time = `${padTimePart(hour)}:${padTimePart(minute)}`
}

const backupScheduleHour = computed({
  get: () => scheduleTimeParts().hour,
  set: (value) => updateScheduleTime('hour', value),
})

const backupScheduleMinute = computed({
  get: () => scheduleTimeParts().minute,
  set: (value) => updateScheduleTime('minute', value),
})

const backupScheduleDescription = computed(() => {
  const { hour, minute } = scheduleTimeParts()
  const time = `${padTimePart(hour)}:${padTimePart(minute)}`

  if (form.backup_schedule_frequency === 'interval_days') {
    return `每隔 ${clampWholeNumber(form.backup_schedule_interval_days, 1, 365)} 天的 ${time} 执行一次`
  }
  if (form.backup_schedule_frequency === 'weekly') {
    const weekday = scheduleWeekdayOptions.find((item) => item.value === Number(form.backup_schedule_weekday))?.label || '周一'
    return `每周 ${weekday} 的 ${time} 执行一次`
  }
  if (form.backup_schedule_frequency === 'monthly') {
    return `每月 ${clampWholeNumber(form.backup_schedule_month_day, 1, 31)} 号 ${time} 执行一次`
  }
  return `每天的 ${time} 执行一次`
})

function handleLogoFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  logoUploadError.value = ''

  if (!file) {
    return
  }

  if (file.size > 300 * 1024) {
    logoUploadError.value = `图片大小超过 300KB 限制（${(file.size / 1024).toFixed(1)}KB）`
    input.value = ''
    return
  }

  if (!file.type.startsWith('image/')) {
    logoUploadError.value = '请选择图片文件'
    input.value = ''
    return
  }

  const reader = new FileReader()
  reader.onload = (event) => {
    form.site_logo = String(event.target?.result || '')
  }
  reader.onerror = () => {
    logoUploadError.value = '读取图片文件失败'
  }
  reader.readAsDataURL(file)
  input.value = ''
}

const hasSettingsCache = restoreSettingsCache()
if (hasSettingsCache) {
  loading.value = false
}

async function loadSettings(options: { showLoading?: boolean } = {}) {
  const showLoading = options.showLoading ?? !hasSettingsCache
  if (showLoading) {
    loading.value = true
  }
  try {
    const settings = await getAdminSettings()
    assignSettings(settings)
    saveSettingsCache(settings)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '\u8bfb\u53d6\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25')
  } finally {
    if (showLoading) {
      loading.value = false
    }
  }
}

async function saveSettings() {
  const pageSizes = parseNumberList(tablePageSizeOptionsInput.value)
  if (pageSizes.length === 0) {
    appStore.showError('\u8868\u683c\u5206\u9875\u9009\u9879\u81f3\u5c11\u9700\u8981\u4e00\u4e2a\u6570\u5b57')
    return
  }

  saving.value = true
  try {
    form.table_page_size_options = pageSizes
    if (!scheduleFrequencyOptions.some((item) => item.value === form.backup_schedule_frequency)) {
      form.backup_schedule_frequency = 'daily'
    }
    form.backup_schedule_interval_days = clampWholeNumber(form.backup_schedule_interval_days, 1, 365)
    form.backup_schedule_weekday = clampWholeNumber(form.backup_schedule_weekday, 1, 7)
    form.backup_schedule_month_day = clampWholeNumber(form.backup_schedule_month_day, 1, 31)
    updateScheduleTime('hour', backupScheduleHour.value)
    updateScheduleTime('minute', backupScheduleMinute.value)
    form.backup_schedule_retain_count = clampWholeNumber(form.backup_schedule_retain_count, 1, 365)
    const updated = await updateAdminSettings({ ...form })
    assignSettings(updated)
    saveSettingsCache(updated)
    await appStore.fetchPublicSettings(true)
    appStore.showSuccess('\u7cfb\u7edf\u8bbe\u7f6e\u5df2\u4fdd\u5b58')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '\u4fdd\u5b58\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25')
  } finally {
    saving.value = false
  }
}

function backupFilename() {
  const now = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  return `mailplus-db-backup-${stamp}.dump`
}

async function downloadDatabaseBackup() {
  backingUp.value = true
  try {
    const blob = await exportDatabaseBackup()
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = backupFilename()
    link.click()
    URL.revokeObjectURL(url)
    appStore.showSuccess('数据库备份已生成')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '数据库备份失败')
  } finally {
    backingUp.value = false
  }
}

function databaseBackupTaskPayload(): DatabaseBackupTaskPayload {
  return {
    backup_schedule_retain_count: clampWholeNumber(form.backup_schedule_retain_count, 1, 365),
    backup_webdav_enabled: form.backup_webdav_enabled,
    backup_webdav_url: form.backup_webdav_url,
    backup_webdav_username: form.backup_webdav_username,
    backup_webdav_password: form.backup_webdav_password,
    backup_webdav_remote_dir: form.backup_webdav_remote_dir,
  }
}

function trackDatabaseBackupTask(task: BackgroundTask) {
  taskStore.trackTask(task, {
    title: '数据库备份',
    onSettled: async (latest) => {
      if (latest.status === 'success') {
        appStore.showSuccess(latest.message || '数据库备份已完成')
      } else if (latest.status === 'partial') {
        appStore.showWarning(latest.message || '本地备份成功，WebDAV 上传失败')
      } else {
        appStore.showError(latest.message || '数据库备份失败')
      }
      if (backupFilesModalOpen.value) {
        await loadBackupFiles()
      }
    },
  })
}

async function startManualDatabaseBackup() {
  manualBackupStarting.value = true
  try {
    const task = await createDatabaseBackupTask(databaseBackupTaskPayload())
    trackDatabaseBackupTask(task)
    appStore.showSuccess('手动备份任务已创建，可在任务中心查看')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '创建手动备份任务失败')
  } finally {
    manualBackupStarting.value = false
  }
}

function handleRestoreFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  restoreFile.value = input.files?.[0] || null
}

function openRestoreModal() {
  clearRestoreFile()
  restoreModalOpen.value = true
}

function closeRestoreModal() {
  if (restoring.value) return
  restoreModalOpen.value = false
  clearRestoreFile()
}

async function openBackupFilesModal() {
  backupFilesModalOpen.value = true
  await loadBackupFiles()
}

function closeBackupFilesModal() {
  if (backupFilesLoading.value) return
  backupFilesModalOpen.value = false
}

async function loadBackupFiles() {
  backupFilesLoading.value = true
  try {
    backupFiles.value = await listDatabaseBackupFiles()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '读取备份文件失败')
  } finally {
    backupFilesLoading.value = false
  }
}

async function downloadBackupFile(file: DatabaseBackupFile) {
  backupFileActionName.value = file.name
  try {
    const blob = await downloadDatabaseBackupFile(file.name)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = file.name
    link.click()
    URL.revokeObjectURL(url)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '下载备份文件失败')
  } finally {
    backupFileActionName.value = ''
  }
}

async function deleteBackupFile(file: DatabaseBackupFile) {
  backupFileActionName.value = file.name
  try {
    await deleteDatabaseBackupFile(file.name)
    appStore.showSuccess('备份文件已删除')
    await loadBackupFiles()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除备份文件失败')
  } finally {
    backupFileActionName.value = ''
  }
}

function chooseRestoreFile() {
  restoreFileInput.value?.click()
}

function clearRestoreFile() {
  restoreFile.value = null
  if (restoreFileInput.value) {
    restoreFileInput.value.value = ''
  }
}

function formatFileSize(size: number) {
  if (!Number.isFinite(size) || size <= 0) {
    return '0 B'
  }
  const units = ['B', 'KB', 'MB', 'GB']
  let value = size
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  return `${value >= 10 || unitIndex === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unitIndex]}`
}

function formatBackupFileTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return '-'
  }
  const pad = (numberValue: number) => String(numberValue).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

async function waitForBackendRestart(timeoutMs = 45000) {
  const startedAt = Date.now()
  const deadline = startedAt + timeoutMs
  const minimumReadyAt = startedAt + 5000
  let sawOffline = false

  await sleep(1500)

  while (Date.now() < deadline) {
    try {
      const response = await fetch('/api/health', { cache: 'no-store' })
      if (response.ok && (sawOffline || Date.now() >= minimumReadyAt)) {
        return true
      }
      if (!response.ok) {
        sawOffline = true
      }
    } catch {
      sawOffline = true
    }
    await sleep(1000)
  }

  return false
}

async function restoreDatabase() {
  if (!restoreFile.value) {
    appStore.showError('请先选择 .dump 备份文件')
    return
  }

  restoring.value = true
  try {
    const result = await restoreDatabaseBackup(restoreFile.value)
    restoreModalOpen.value = false
    clearRestoreFile()
    appStore.showSuccess(result.message || '数据库恢复完成，程序正在重启')
    if (result.restart_scheduled) {
      const restarted = await waitForBackendRestart()
      if (restarted) {
        clearAuthSession(false)
        appStore.showSuccess('程序已重启完成，请重新登录')
        await router.replace('/login')
      } else {
        appStore.showError('恢复已完成，但等待重启超时，请稍后刷新页面确认')
      }
    }
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '数据库恢复失败')
  } finally {
    restoring.value = false
  }
}

async function testBackupWebDAV() {
  testingWebDAV.value = true
  try {
    const result = await testDatabaseBackupWebDAV(databaseBackupTaskPayload())
    appStore.showSuccess(result.message || 'WebDAV 连接测试成功')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : 'WebDAV 连接测试失败')
  } finally {
    testingWebDAV.value = false
  }
}

onMounted(() => loadSettings({ showLoading: !hasSettingsCache }))
</script>

<template>
  <form class="mx-auto max-w-6xl space-y-6" novalidate @submit.prevent="saveSettings">
    <div class="settings-tabs-shell">
      <div class="settings-tabs-scroll">
        <div class="settings-tabs">
          <button
            v-for="tab in tabs"
            :key="tab.key"
            type="button"
            class="settings-tab"
            :class="{ 'settings-tab-active': activeTab === tab.key }"
            @click="activeTab = tab.key"
          >
            <component :is="tab.icon" class="settings-tab-icon" />
            <span class="settings-tab-label">{{ tab.label }}</span>
          </button>
        </div>
      </div>
    </div>

    <div v-if="loading" class="flex items-center justify-center py-12">
      <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
    </div>

    <template v-else>
      <div v-show="activeTab === 'general'" class="space-y-6">
        <div class="card">
          <div class="card-header">
            <h2 class="card-title">站点设置</h2>
            <p class="card-description">配置站点名称、Logo 和通用表格设置。</p>
          </div>
          <div class="space-y-6 p-6">
            <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
              <label class="block">
                <span class="input-label">站点名称</span>
                <input v-model="form.site_name" class="input" type="text" placeholder="Sub2API" />
                <span class="hint">显示在邮件和页面标题中</span>
              </label>
              <label class="block">
                <span class="input-label">站点副标题</span>
                <input v-model="form.site_subtitle" class="input" type="text" placeholder="订阅转 API 转换平台" />
                <span class="hint">显示在登录页面</span>
              </label>
            </div>

            <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
              <h3 class="text-sm font-medium text-gray-900 dark:text-white">通用表格设置</h3>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">设置后台与用户侧表格组件的默认分页行为</p>
              <div class="mt-5 grid grid-cols-1 gap-6 md:grid-cols-2">
                <label class="block">
                  <span class="input-label">默认每页条数</span>
                  <input v-model.number="form.table_default_page_size" class="input w-40" type="number" min="5" max="1000" />
                  <span class="hint">必须为 5-1000 之间的整数</span>
                </label>
                <label class="block">
                  <span class="input-label">可选每页条数列表</span>
                  <input v-model="tablePageSizeOptionsInput" class="input font-mono text-sm" type="text" placeholder="10, 20, 50, 100" />
                  <span class="hint">使用英文逗号分隔，取值范围 5-1000，保存时会自动去重并排序</span>
                </label>
              </div>
            </div>

            <div>
              <label class="input-label">站点Logo</label>
              <div class="flex items-start gap-4">
                <div class="flex-shrink-0">
                  <div
                    class="flex h-20 w-20 items-center justify-center overflow-hidden rounded-xl border-2 border-dashed border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-800"
                    :class="{ 'border-solid': !!form.site_logo }"
                  >
                    <img v-if="form.site_logo" :src="form.site_logo" alt="" class="h-full w-full object-contain" />
                    <svg
                      v-else
                      class="h-8 w-8 text-gray-400 dark:text-dark-500"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                    >
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        stroke-width="1.5"
                        d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
                      />
                    </svg>
                  </div>
                </div>

                <div class="flex-1 space-y-2">
                  <div class="flex items-center gap-2">
                    <label class="btn btn-secondary btn-sm cursor-pointer">
                      <input class="hidden" type="file" accept="image/*" @change="handleLogoFileChange" />
                      <Upload class="mr-1.5 h-4 w-4" />
                      上传图片
                    </label>
                    <button
                      v-if="form.site_logo"
                      class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
                      type="button"
                      @click="form.site_logo = ''; logoUploadError = ''"
                    >
                      <Trash2 class="mr-1.5 h-4 w-4" />
                      移除
                    </button>
                  </div>
                  <p class="text-xs text-gray-500 dark:text-gray-400">PNG、JPG 或 SVG 格式，最大 300KB。建议：80x80px 正方形图片。</p>
                  <p v-if="logoUploadError" class="text-xs text-red-500">{{ logoUploadError }}</p>
                </div>
              </div>
            </div>

          </div>
        </div>
      </div>

      <div v-show="activeTab === 'backup'" class="space-y-6">
        <div class="card">
          <div class="card-header">
            <h2 class="card-title">数据备份与恢复</h2>
            <p class="card-description">导出或恢复完整 PostgreSQL 数据库。</p>
          </div>
          <div class="space-y-5 p-6">
            <div class="backup-actions-row">
              <div class="backup-action-panel border border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-900/60">
                <div class="backup-action-content">
                  <h3 class="backup-action-title text-gray-900 dark:text-dark-100">完整备份</h3>
                  <p class="backup-action-description text-gray-500 dark:text-dark-300">生成 .dump 文件，包含当前数据库结构、数据和序列。</p>
                </div>
                <button class="btn btn-primary backup-action-button" type="button" :disabled="backingUp || restoring" @click="downloadDatabaseBackup">
                  <Download class="h-4 w-4" />
                  {{ backingUp ? '生成中' : '下载备份' }}
                </button>
              </div>

              <div class="backup-action-panel border border-red-200/80 bg-gray-50/80 dark:border-red-500/30 dark:bg-dark-900/60">
                <div class="backup-action-content">
                  <h3 class="backup-action-title text-red-600 dark:text-red-300">完整恢复</h3>
                  <p class="backup-action-description text-red-600 dark:text-red-300">清理当前数据库对象，再导入备份文件。恢复完成后建议重启后端。</p>
                </div>
                <button class="btn btn-secondary backup-action-button text-red-600 hover:text-red-700 dark:text-red-400" type="button" :disabled="backingUp || restoring" @click="openRestoreModal">
                  <Upload class="h-4 w-4" />
                  恢复备份
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="card">
          <div class="card-header">
            <h2 class="card-title">定时备份</h2>
            <p class="card-description">启用后按这里的时间自动生成本地数据库备份。</p>
          </div>
          <div class="space-y-6 p-6">
            <label class="backup-toggle-row">
              <input v-model="form.backup_schedule_enabled" class="backup-toggle-input" type="checkbox" />
              <span class="backup-toggle-track" aria-hidden="true">
                <span class="backup-toggle-thumb"></span>
              </span>
              <span class="min-w-0">
                <span class="backup-toggle-title">启用定时备份</span>
                <span class="backup-toggle-description">到达设定时间后自动备份到程序本地目录。</span>
              </span>
            </label>

            <div class="space-y-5">
              <div class="schedule-builder">
                <div class="schedule-controls">
                  <select v-model="form.backup_schedule_frequency" class="input schedule-frequency">
                    <option v-for="option in scheduleFrequencyOptions" :key="option.value" :value="option.value">
                      {{ option.label }}
                    </option>
                  </select>

                  <label v-if="form.backup_schedule_frequency === 'interval_days'" class="schedule-field">
                    <input v-model.number="form.backup_schedule_interval_days" class="input schedule-number" type="number" min="1" max="365" />
                    <span class="schedule-unit">天</span>
                  </label>

                  <select v-if="form.backup_schedule_frequency === 'weekly'" v-model.number="form.backup_schedule_weekday" class="input schedule-weekday">
                    <option v-for="option in scheduleWeekdayOptions" :key="option.value" :value="option.value">
                      {{ option.label }}
                    </option>
                  </select>

                  <label v-if="form.backup_schedule_frequency === 'monthly'" class="schedule-field">
                    <input v-model.number="form.backup_schedule_month_day" class="input schedule-number" type="number" min="1" max="31" />
                    <span class="schedule-unit">天</span>
                  </label>

                  <label class="schedule-field">
                    <input v-model.number="backupScheduleHour" class="input schedule-number" type="number" min="0" max="23" />
                    <span class="schedule-unit">小时</span>
                  </label>

                  <label class="schedule-field">
                    <input v-model.number="backupScheduleMinute" class="input schedule-number" type="number" min="0" max="59" />
                    <span class="schedule-unit">分钟</span>
                  </label>
                </div>

              </div>

              <p class="schedule-description">{{ backupScheduleDescription }}</p>

              <div class="backup-file-tools">
                <button class="btn btn-primary backup-file-button" type="button" :disabled="manualBackupStarting || backingUp || restoring" @click="startManualDatabaseBackup">
                  <Database class="h-4 w-4" />
                  {{ manualBackupStarting ? '创建中' : '手动备份' }}
                </button>
                <label class="block schedule-retain">
                  <span class="input-label">保留份数</span>
                  <input v-model.number="form.backup_schedule_retain_count" class="input" type="number" min="1" max="365" />
                  <span class="hint">自动清理时只保留最近这些文件</span>
                </label>
                <button class="btn btn-secondary backup-file-button" type="button" @click="openBackupFilesModal">
                  <Database class="h-4 w-4" />
                  查看备份
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="card">
          <div class="card-header">
            <h2 class="card-title">WebDAV 存储</h2>
            <p class="card-description">启用后，定时备份会在保存本地文件后额外上传到 WebDAV。</p>
          </div>
          <div class="space-y-6 p-6">
            <label class="backup-toggle-row">
              <input v-model="form.backup_webdav_enabled" class="backup-toggle-input" type="checkbox" />
              <span class="backup-toggle-track" aria-hidden="true">
                <span class="backup-toggle-thumb"></span>
              </span>
              <span class="min-w-0">
                <span class="backup-toggle-title">启用 WebDAV 上传</span>
                <span class="backup-toggle-description">本地备份成功后自动上传一份到远程目录。</span>
              </span>
            </label>

            <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
              <label class="block md:col-span-2">
                <span class="input-label">WebDAV 地址</span>
                <input v-model.trim="form.backup_webdav_url" class="input" type="url" placeholder="https://example.com/dav" />
                <span class="hint">填写 WebDAV 服务地址</span>
              </label>
              <label class="block">
                <span class="input-label">用户名</span>
                <input v-model.trim="form.backup_webdav_username" class="input" type="text" autocomplete="off" />
              </label>
              <label class="block">
                <span class="input-label">密码</span>
                <input v-model="form.backup_webdav_password" class="input" type="password" autocomplete="new-password" />
              </label>
              <label class="block md:col-span-2">
                <span class="input-label">远程目录</span>
                <input v-model.trim="form.backup_webdav_remote_dir" class="input" type="text" placeholder="/MailPlus" />
                <span class="hint">自动上传备份文件的目标目录</span>
              </label>
            </div>

            <div class="flex justify-start">
              <button class="btn btn-secondary" type="button" :disabled="testingWebDAV" @click="testBackupWebDAV">
                {{ testingWebDAV ? '测试中' : '测试连接' }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <div v-show="activeTab === 'general' || activeTab === 'backup'" class="flex justify-end">
        <button class="btn btn-primary" type="submit" :disabled="saving">
          <Save class="h-4 w-4" />
          {{ saving ? '保存中' : '保存设置' }}
        </button>
      </div>

      <Teleport to="body">
        <div v-if="restoreModalOpen" class="outlook-modal-mask center-mail-modal">
          <div class="outlook-import-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
            <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
              <h3 class="text-lg font-bold text-gray-900 dark:text-white">恢复备份</h3>
              <button class="modal-close-button" type="button" :disabled="restoring" @click="closeRestoreModal">
                <X class="h-5 w-5" />
              </button>
            </div>
            <div class="outlook-modal-scroll-body p-6">
              <p class="text-sm text-gray-600 dark:text-dark-300">上传完整数据库 .dump 备份文件，将当前数据库恢复到备份时的状态。</p>
              <div class="outlook-import-warning">恢复会清理当前数据库对象并导入备份内容；请确认已经下载当前数据备份，恢复完成后建议重启后端。</div>
              <label class="mt-5 block">
                <span class="input-label">数据文件</span>
                <div class="outlook-import-file-box">
                  <div class="min-w-0">
                    <div class="truncate text-sm font-bold text-gray-800 dark:text-dark-100">{{ restoreFile ? restoreFile.name : '请选择数据文件' }}</div>
                    <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ restoreFile ? formatFileSize(restoreFile.size) : 'DUMP (.dump)' }}</div>
                  </div>
                  <button class="outlook-import-file-button" type="button" :disabled="restoring" @click="chooseRestoreFile">选择文件</button>
                  <input ref="restoreFileInput" class="hidden" type="file" accept=".dump" :disabled="restoring" @change="handleRestoreFileChange" />
                </div>
              </label>
            </div>
            <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
              <button class="btn btn-secondary" type="button" :disabled="restoring" @click="closeRestoreModal">取消</button>
              <button class="btn btn-primary" type="button" :disabled="restoring || !restoreFile" @click="restoreDatabase">{{ restoring ? '恢复中...' : '开始恢复' }}</button>
            </div>
          </div>
        </div>
      </Teleport>

      <Teleport to="body">
        <div v-if="backupFilesModalOpen" class="outlook-modal-mask center-mail-modal">
          <div class="backup-files-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
            <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
              <h3 class="text-lg font-bold text-gray-900 dark:text-white">备份文件</h3>
              <button class="modal-close-button" type="button" :disabled="backupFilesLoading" @click="closeBackupFilesModal">
                <X class="h-5 w-5" />
              </button>
            </div>
            <div class="outlook-modal-scroll-body p-6">
              <div v-if="backupFilesLoading" class="backup-files-empty">正在读取备份文件...</div>
              <div v-else-if="backupFiles.length === 0" class="backup-files-empty">暂未找到本地备份文件</div>
              <div v-else class="backup-files-list">
                <div v-for="file in backupFiles" :key="`${file.directory}/${file.name}`" class="backup-file-item">
                  <div class="min-w-0">
                    <div class="truncate text-sm font-bold text-gray-800 dark:text-dark-100">{{ file.name }}</div>
                    <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ file.directory }}</div>
                  </div>
                  <div class="backup-file-meta">
                    <span>{{ formatFileSize(file.size) }}</span>
                    <span>{{ formatBackupFileTime(file.created_at || file.modified_at) }}</span>
                  </div>
                  <div class="backup-file-actions">
                    <button class="btn btn-secondary btn-xs" type="button" :disabled="!!backupFileActionName" @click="downloadBackupFile(file)">
                      <Download class="h-3.5 w-3.5" />
                      下载
                    </button>
                    <button class="btn btn-secondary btn-xs text-red-600 hover:text-red-700 dark:text-red-400" type="button" :disabled="!!backupFileActionName" @click="deleteBackupFile(file)">
                      <Trash2 class="h-3.5 w-3.5" />
                      删除
                    </button>
                  </div>
                </div>
              </div>
            </div>
            <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
              <button class="btn btn-secondary" type="button" :disabled="backupFilesLoading" @click="loadBackupFiles">刷新</button>
              <button class="btn btn-primary" type="button" @click="closeBackupFilesModal">关闭</button>
            </div>
          </div>
        </div>
      </Teleport>
    </template>
  </form>
</template>

<style scoped>
.backup-actions-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1rem;
}

.backup-action-panel {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  min-width: 0;
  border-radius: 0.875rem;
  padding: 1rem;
}

.backup-action-content {
  min-width: 0;
}

.backup-action-title {
  font-size: 0.875rem;
  font-weight: 700;
}

.backup-action-description {
  margin-top: 0.25rem;
  font-size: 0.75rem;
  line-height: 1.45;
}

.backup-action-button {
  flex-shrink: 0;
  white-space: nowrap;
}

.backup-toggle-row {
  display: flex;
  align-items: center;
  gap: 0.85rem;
  cursor: pointer;
}

.backup-toggle-input {
  position: absolute;
  height: 1px;
  width: 1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
}

.backup-toggle-track {
  position: relative;
  display: inline-flex;
  height: 1.55rem;
  width: 2.8rem;
  flex-shrink: 0;
  align-items: center;
  border-radius: 999px;
  border: 1px solid rgb(203 213 225);
  background: rgb(241 245 249);
  transition: background-color 0.15s ease, border-color 0.15s ease;
}

.backup-toggle-thumb {
  position: absolute;
  left: 0.18rem;
  height: 1.1rem;
  width: 1.1rem;
  border-radius: 999px;
  background: white;
  box-shadow: 0 1px 3px rgb(15 23 42 / 0.2);
  transition: transform 0.15s ease;
}

.backup-toggle-input:checked + .backup-toggle-track {
  border-color: rgb(20 184 166 / 0.7);
  background: rgb(20 184 166);
}

.backup-toggle-input:checked + .backup-toggle-track .backup-toggle-thumb {
  transform: translateX(1.25rem);
}

.backup-toggle-title {
  display: block;
  font-size: 0.875rem;
  font-weight: 700;
  color: rgb(17 24 39);
}

.backup-toggle-description {
  display: block;
  margin-top: 0.15rem;
  font-size: 0.75rem;
  line-height: 1.45;
  color: rgb(100 116 139);
}

.dark .backup-toggle-track {
  border-color: rgb(71 85 105);
  background: rgb(15 23 42 / 0.55);
}

.dark .backup-toggle-input:checked + .backup-toggle-track {
  border-color: rgb(45 212 191 / 0.65);
  background: rgb(15 118 110);
}

.dark .backup-toggle-title {
  color: rgb(241 245 249);
}

.dark .backup-toggle-description {
  color: rgb(148 163 184);
}

.schedule-builder {
  display: block;
}

.schedule-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.65rem;
}

.schedule-frequency {
  width: 5.5rem;
}

.schedule-weekday {
  width: 6.5rem;
}

.schedule-field {
  display: inline-flex;
  width: 8rem;
  min-width: 0;
  align-items: stretch;
}

.schedule-retain {
  width: 12rem;
  min-width: 10rem;
}

.schedule-number {
  min-width: 0;
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}

.schedule-unit {
  display: inline-flex;
  min-width: 3.35rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(203 213 225);
  border-left: 0;
  border-radius: 0 0.5rem 0.5rem 0;
  background: rgb(248 250 252);
  padding: 0 0.7rem;
  font-size: 0.875rem;
  color: rgb(100 116 139);
  white-space: nowrap;
}

.schedule-description {
  font-size: 0.8125rem;
  line-height: 1.45;
  color: rgb(100 116 139);
}

.backup-file-tools {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 1rem;
  border-top: 1px solid rgb(226 232 240);
  padding-top: 1rem;
}

.backup-file-button {
  min-width: 7.5rem;
  margin-bottom: 1.35rem;
  white-space: nowrap;
}

.backup-files-modal {
  width: min(42rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
  display: flex;
  flex-direction: column;
}

.backup-files-empty {
  display: flex;
  min-height: 9rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.75rem;
  border: 1px dashed rgb(148 163 184 / 0.55);
  background: rgb(248 250 252);
  font-size: 0.875rem;
  color: rgb(100 116 139);
}

.backup-files-list {
  display: grid;
  gap: 0.75rem;
}

.backup-file-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: 1rem;
  align-items: center;
  border-radius: 0.75rem;
  border: 1px solid rgb(226 232 240);
  background: rgb(248 250 252);
  padding: 0.85rem 1rem;
}

.backup-file-meta {
  display: grid;
  gap: 0.25rem;
  justify-items: end;
  font-size: 0.75rem;
  color: rgb(100 116 139);
  white-space: nowrap;
}

.backup-file-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  white-space: nowrap;
}

.dark .backup-file-tools {
  border-top-color: rgb(51 65 85);
}

.dark .backup-files-empty,
.dark .backup-file-item {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42 / 0.45);
}

.dark .backup-files-empty,
.dark .backup-file-meta {
  color: rgb(148 163 184);
}

.dark .schedule-unit {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42 / 0.6);
  color: rgb(148 163 184);
}

.dark .schedule-description {
  color: rgb(148 163 184);
}

@media (max-width: 640px) {
  .schedule-builder {
    align-items: stretch;
    gap: 0.65rem;
  }

  .schedule-controls {
    align-items: stretch;
  }

  .schedule-frequency,
  .schedule-weekday,
  .schedule-field,
  .schedule-retain {
    width: 100%;
  }

  .backup-file-button {
    width: 100%;
    margin-bottom: 0;
  }

  .backup-file-item {
    grid-template-columns: 1fr;
  }

  .backup-file-meta {
    justify-items: start;
  }

  .backup-file-actions {
    justify-content: flex-start;
  }

}

.outlook-modal-mask {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
  background: rgb(0 0 0 / 0.45);
  backdrop-filter: blur(4px);
}

:global(.center-mail-modal) {
  overflow: hidden;
}

:global(.outlook-import-modal) {
  width: min(31rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
  display: flex;
  flex-direction: column;
}

:global(.scrollable-mail-modal) {
  display: flex;
  flex-direction: column;
}

.outlook-modal-scroll-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}

.outlook-modal-scroll-body::-webkit-scrollbar {
  width: 0.55rem;
}

.outlook-modal-scroll-body::-webkit-scrollbar-track {
  border-radius: 999px;
  background: rgb(226 232 240 / 0.8);
}

.outlook-modal-scroll-body::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.85);
}

.dark .outlook-modal-scroll-body::-webkit-scrollbar-track {
  background: rgb(15 23 42 / 0.75);
}

.dark .outlook-modal-scroll-body::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.95);
}

:global(.modal-close-button) {
  display: inline-flex;
  height: 2.25rem;
  width: 2.25rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.75rem;
  background: transparent;
  color: rgb(148 163 184);
  transition: background-color 0.15s ease, color 0.15s ease, transform 0.15s ease;
}

:global(.modal-close-button:hover) {
  background: rgb(226 232 240 / 0.9);
  color: rgb(71 85 105);
}

:global(.modal-close-button:disabled) {
  cursor: not-allowed;
  opacity: 0.55;
}

.outlook-import-warning {
  margin-top: 1rem;
  border-radius: 0.75rem;
  border: 1px solid rgb(245 158 11 / 0.35);
  background: rgb(245 158 11 / 0.08);
  padding: 0.85rem 1rem;
  font-size: 0.8125rem;
  line-height: 1.6;
  color: rgb(146 64 14);
}

.outlook-import-file-box {
  margin-top: 0.5rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border-radius: 0.75rem;
  border: 1px dashed rgb(148 163 184 / 0.65);
  background: rgb(248 250 252);
  padding: 0.9rem 1rem;
}

.outlook-import-file-button {
  flex-shrink: 0;
  border-radius: 0.65rem;
  border: 1px solid rgb(20 184 166 / 0.35);
  background: rgb(240 253 250);
  padding: 0.45rem 0.8rem;
  font-size: 0.8125rem;
  font-weight: 700;
  color: rgb(15 118 110);
}

.outlook-import-file-button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

:global(.dark .modal-close-button) {
  background: transparent;
  color: rgb(203 213 225);
}

:global(.dark .modal-close-button:hover) {
  background: rgb(51 65 85 / 0.9);
  color: white;
}

.dark .outlook-import-warning {
  border-color: rgb(245 158 11 / 0.28);
  background: rgb(245 158 11 / 0.1);
  color: rgb(252 211 77);
}

.dark .outlook-import-file-box {
  border-color: rgb(71 85 105);
  background: rgb(15 23 42 / 0.35);
}

.dark .outlook-import-file-button {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: rgb(94 234 212);
}

@media (max-width: 640px) {
  form {
    max-width: 100%;
  }

  .card {
    border-radius: 0.875rem;
  }

  .card.p-6,
  .card > .space-y-6 {
    padding: 1rem;
  }

  .card-header {
    padding: 1rem;
  }

  .card .flex.items-start.gap-4,
  .card .flex.items-center.gap-2,
  .card .flex.justify-end {
    align-items: stretch;
    flex-wrap: wrap;
  }

  .card .btn,
  form > .flex.justify-end .btn {
    width: 100%;
  }

  .card table {
    min-width: 680px;
  }

  .backup-actions-row {
    grid-template-columns: 1fr;
  }

  .backup-action-panel {
    align-items: stretch;
    flex-direction: column;
  }

  .backup-action-button {
    width: 100%;
  }

  .outlook-modal-mask {
    padding: 0.75rem;
  }

  :global(.outlook-import-modal) {
    width: min(31rem, calc(100vw - 1.5rem));
  }

  .outlook-import-file-box {
    align-items: stretch;
    flex-direction: column;
  }

  .outlook-import-file-button {
    flex: 1 1 auto;
  }
}
</style>
