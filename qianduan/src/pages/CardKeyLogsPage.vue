<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RefreshCw, Search, Trash2, X } from 'lucide-vue-next'
import PaginationBar from '../components/PaginationBar.vue'
import { getAdminSettings, updateAdminSettings } from '../api/adminSettings'
import { clearCardKeyUseLogs, listCardKeyUseLogs, type CardKeyUseLog, type CardKeyUseLogListResponse } from '../api/cardKeyLogs'
import { useAppStore } from '../stores/app'

type CardKeyLogsCache = {
  logs?: CardKeyUseLog[]
  pagination?: {
    page?: number
    page_size?: number
    total?: number
    pages?: number
  }
  query?: {
    search?: string
  }
  page_size_options?: number[]
  cleanup_days?: string
  updated_at?: number
}

const appStore = useAppStore()
const fallbackTablePageSize = 20
const fallbackTablePageSizeOptions = [10, 20, 50, 100]
const pageSizeStorageKey = 'card_key_logs_page_size'
const cardKeyLogsCacheKey = 'card_key_logs_cache_v1'
const logs = ref<CardKeyUseLog[]>([])
const searchQuery = ref('')
const cleanupDaysInput = ref('')
const savedCleanupDaysInput = ref('')
const page = ref(1)
const pages = ref(0)
const pageSize = ref(readPersistedPageSize() || fallbackTablePageSize)
const pageSizeOptions = ref<number[]>(fallbackTablePageSizeOptions)
const total = ref(0)
const loading = ref(false)
const refreshing = ref(false)
const clearing = ref(false)
const cleanupSaving = ref(false)
const logsCacheRestored = ref(false)
let searchTimer: number | undefined
let cleanupSaveTimer: number | undefined
let requestID = 0
let mountedReady = false

function normalizePageSize(value: unknown, fallback = fallbackTablePageSize) {
  const size = Math.floor(Number(value))
  return Number.isFinite(size) && size > 0 ? size : fallback
}

function readPersistedPageSize() {
  const value = Number(localStorage.getItem(pageSizeStorageKey))
  return Number.isFinite(value) && value > 0 ? Math.floor(value) : 0
}

function normalizeCleanupDays(value: unknown) {
  const text = String(value ?? '').trim()
  if (!text) return ''
  const days = Math.floor(Number(text))
  return Number.isFinite(days) && days >= 0 ? String(days) : ''
}

function normalizePageSizeOptions(values: unknown, defaultSize: number, currentSize = defaultSize) {
  const list = Array.isArray(values) ? values : fallbackTablePageSizeOptions
  const result = list
    .map((value) => Number(value))
    .filter((value) => Number.isFinite(value) && value > 0)
  result.push(normalizePageSize(defaultSize), normalizePageSize(currentSize, defaultSize))
  return Array.from(new Set(result)).sort((a, b) => a - b)
}

async function loadSettings() {
  try {
    const settings = await getAdminSettings()
    const defaultSize = normalizePageSize(settings.table_default_page_size)
    const persistedSize = readPersistedPageSize()
    if (persistedSize) {
      pageSize.value = persistedSize
    } else if (!logsCacheRestored.value) {
      pageSize.value = defaultSize
    } else {
      pageSize.value = normalizePageSize(pageSize.value, defaultSize)
    }
    pageSizeOptions.value = normalizePageSizeOptions(settings.table_page_size_options, defaultSize, pageSize.value)
    cleanupDaysInput.value = normalizeCleanupDays(settings.card_key_log_cleanup_days)
    savedCleanupDaysInput.value = cleanupDaysInput.value
    saveCardKeyLogsCache()
  } catch {
    const persistedSize = readPersistedPageSize()
    pageSize.value = normalizePageSize(persistedSize || pageSize.value)
    pageSizeOptions.value = normalizePageSizeOptions(pageSizeOptions.value, fallbackTablePageSize, pageSize.value)
    if (!logsCacheRestored.value) {
      cleanupDaysInput.value = ''
      savedCleanupDaysInput.value = ''
    }
  }
}

function applyCardKeyUseLogListResponse(response: CardKeyUseLogListResponse) {
  logs.value = response.items || []
  total.value = Number(response.total) || 0
  pages.value = Number(response.pages) || 0
  page.value = Number(response.page) || page.value
  pageSize.value = normalizePageSize(response.page_size || pageSize.value)
  saveCardKeyLogsCache()
}

function restoreCardKeyLogsCache() {
  try {
    const value = JSON.parse(localStorage.getItem(cardKeyLogsCacheKey) || 'null') as CardKeyLogsCache | null
    if (!value || typeof value !== 'object') return

    let restored = false
    if (Array.isArray(value.logs)) {
      logs.value = value.logs
      restored = true
    }
    if (value.pagination && typeof value.pagination === 'object') {
      page.value = Number(value.pagination.page) || page.value
      total.value = Number(value.pagination.total) || 0
      pages.value = Number(value.pagination.pages) || 0
      pageSize.value = normalizePageSize(value.pagination.page_size || pageSize.value)
      restored = true
    }
    if (value.query && typeof value.query === 'object') {
      searchQuery.value = String(value.query.search || '')
      restored = true
    }
    if (Array.isArray(value.page_size_options) && value.page_size_options.length > 0) {
      pageSizeOptions.value = normalizePageSizeOptions(value.page_size_options, pageSize.value, pageSize.value)
      restored = true
    }
    if (Object.prototype.hasOwnProperty.call(value, 'cleanup_days')) {
      cleanupDaysInput.value = normalizeCleanupDays(value.cleanup_days)
      savedCleanupDaysInput.value = cleanupDaysInput.value
      restored = true
    }
    logsCacheRestored.value = restored
    if (restored) saveCardKeyLogsCache()
  } catch {
    // Ignore stale cache.
  }
}

function saveCardKeyLogsCache() {
  try {
    localStorage.setItem(pageSizeStorageKey, String(pageSize.value))
    localStorage.setItem(
      cardKeyLogsCacheKey,
      JSON.stringify({
        logs: logs.value,
        pagination: {
          page: page.value,
          page_size: pageSize.value,
          total: total.value,
          pages: pages.value,
        },
        query: {
          search: searchQuery.value,
        },
        page_size_options: pageSizeOptions.value,
        cleanup_days: savedCleanupDaysInput.value || cleanupDaysInput.value,
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

async function loadLogs() {
  const id = ++requestID
  loading.value = true
  try {
    const response = await listCardKeyUseLogs({
      search: searchQuery.value.trim(),
      page: page.value,
      page_size: pageSize.value,
    })
    if (id !== requestID) return
    if (response.items.length === 0 && response.total > 0 && response.pages > 0 && page.value > response.pages) {
      page.value = response.pages
      return
    }
    applyCardKeyUseLogListResponse(response)
  } catch (error) {
    if (id === requestID) {
      appStore.showError(error instanceof Error ? error.message : '读取卡密日志失败')
    }
  } finally {
    if (id === requestID) loading.value = false
  }
}

async function refreshLogs() {
  if (refreshing.value || loading.value) return
  refreshing.value = true
  try {
    await loadLogs()
  } finally {
    refreshing.value = false
  }
}

function queueSearch() {
  if (!mountedReady) return
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    if (page.value !== 1) {
      page.value = 1
    } else {
      void loadLogs()
    }
  }, 300)
}

function queueCleanupSave() {
  if (!mountedReady) return
  window.clearTimeout(cleanupSaveTimer)
  cleanupSaveTimer = window.setTimeout(() => {
    void saveCleanupDays()
  }, 450)
}

async function saveCleanupDays() {
  const normalized = normalizeCleanupDays(cleanupDaysInput.value)
  if (cleanupDaysInput.value !== normalized) cleanupDaysInput.value = normalized
  if (normalized === savedCleanupDaysInput.value) return

  cleanupSaving.value = true
  try {
    await updateAdminSettings({ card_key_log_cleanup_days: normalized })
    savedCleanupDaysInput.value = normalized
    saveCardKeyLogsCache()
    appStore.showSuccess('定期清理已保存')
    if (normalized) {
      page.value = 1
      await loadLogs()
    }
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存定期清理失败')
  } finally {
    cleanupSaving.value = false
  }
}

async function handleClearLogs() {
  const confirmed = await appStore.showConfirm({
    title: '清空日志',
    message: '确定要清空全部卡密使用日志吗？',
    confirmText: '清空',
    cancelText: '取消',
    tone: 'danger',
  })
  if (!confirmed) return

  clearing.value = true
  try {
    const result = await clearCardKeyUseLogs()
    appStore.showSuccess(`已清空 ${result.count || 0} 条日志`)
    page.value = 1
    logs.value = []
    total.value = 0
    pages.value = 0
    saveCardKeyLogsCache()
    await loadLogs()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '清空卡密日志失败')
  } finally {
    clearing.value = false
  }
}

function handlePageSizeChange(size: number) {
  pageSize.value = normalizePageSize(size)
  page.value = 1
  saveCardKeyLogsCache()
}

watch(searchQuery, queueSearch)
watch(cleanupDaysInput, queueCleanupSave)
watch([page, pageSize], () => {
  if (!mountedReady) return
  saveCardKeyLogsCache()
  void loadLogs()
})

onMounted(async () => {
  restoreCardKeyLogsCache()
  await loadSettings()
  mountedReady = true
  await loadLogs()
})

onBeforeUnmount(() => {
  window.clearTimeout(searchTimer)
  window.clearTimeout(cleanupSaveTimer)
})
</script>

<template>
  <section class="card-key-use-logs-page w-full min-w-0">
    <div class="card-key-use-logs-card flex min-h-[calc(100vh-8rem)] w-full min-w-0 flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900/90 dark:shadow-black/20">
      <div class="card-key-use-logs-toolbar border-b border-gray-200 bg-gray-50/80 px-5 py-4 dark:border-dark-700 dark:bg-dark-800/70">
        <div class="card-key-log-search-actions">
          <div class="card-key-log-search-field search-clear-field">
            <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            <input
              v-model="searchQuery"
              class="input search-clear-input h-9 pl-10 text-sm"
              type="search"
              placeholder="搜索卡密或邮箱"
            />
            <button v-if="searchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="searchQuery = ''">
              <X class="h-3.5 w-3.5" />
            </button>
          </div>

          <button class="card-key-log-refresh-button" type="button" title="刷新" :disabled="refreshing || loading" @click="refreshLogs">
            <RefreshCw class="h-4 w-4" :class="{ 'card-key-log-refresh-icon-spinning': refreshing || loading }" />
            刷新
          </button>
        </div>

        <div class="card-key-logs-actions">
          <label class="card-key-cleanup-field">
            <span class="card-key-cleanup-label text-slate-800 dark:!text-slate-200">定期清理：</span>
            <input
              v-model.trim="cleanupDaysInput"
              class="input card-key-cleanup-input"
              type="number"
              min="0"
              step="1"
              placeholder="清理天数"
              :aria-busy="cleanupSaving"
              aria-label="定期清理天数"
            />
          </label>

          <button class="card-key-use-logs-button card-key-use-logs-button-danger" type="button" :disabled="clearing" @click="handleClearLogs">
            <Trash2 class="h-4 w-4" />
            <span>清空日志</span>
          </button>
        </div>
      </div>

      <div class="card-key-use-logs-table-shell relative flex-1 overflow-x-auto overflow-y-auto bg-white dark:bg-dark-900">
        <table class="card-key-use-logs-table">
          <thead>
            <tr>
              <th class="card-key-use-logs-head-cell card-key-use-logs-col-key">卡密</th>
              <th class="card-key-use-logs-head-cell card-key-use-logs-col-email">绑定邮箱</th>
              <th class="card-key-use-logs-head-cell card-key-use-logs-col-subject">邮件标题</th>
              <th class="card-key-use-logs-head-cell card-key-use-logs-col-ip">使用IP</th>
              <th class="card-key-use-logs-head-cell card-key-use-logs-col-time">使用时间</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in logs" :key="item.id" class="hover:bg-primary-50/70 dark:hover:bg-primary-900/10">
              <td class="font-mono text-sm">{{ item.card_key }}</td>
              <td>{{ item.bound_email || '-' }}</td>
              <td>{{ item.mail_subject || '-' }}</td>
              <td>{{ item.user_ip || '-' }}</td>
              <td>{{ item.used_at || '-' }}</td>
            </tr>
          </tbody>
        </table>

        <div v-if="logs.length === 0" class="card-key-use-logs-empty p-8 text-center text-sm font-semibold text-gray-500 dark:text-dark-400">
          {{ loading ? '加载中...' : '暂无卡密使用日志' }}
        </div>
      </div>

      <div class="border-t border-gray-200 bg-gray-50/80 px-4 py-3 dark:border-dark-700 dark:bg-dark-800/70">
        <PaginationBar
          :page="page"
          :pages="pages"
          :page-size="pageSize"
          :page-size-options="pageSizeOptions"
          :total="total"
          :disabled="loading"
          @page-change="page = $event"
          @page-size-change="handlePageSizeChange"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.card-key-use-logs-page {
  display: flex;
  height: calc(100vh - 8rem);
  max-height: calc(100vh - 8rem);
  min-height: 0;
  overflow: hidden;
}

.card-key-use-logs-card {
  height: 100%;
  max-height: 100%;
  min-height: 0;
}

.card-key-use-logs-toolbar {
  display: grid;
  grid-template-columns: minmax(16rem, 22rem) minmax(0, 1fr);
  gap: 0.75rem;
  align-items: center;
}

.card-key-logs-actions {
  display: inline-flex;
  align-items: center;
  justify-self: end;
  gap: 0.75rem;
}

.card-key-log-search-actions {
  display: inline-flex;
  width: min(28rem, 100%);
  max-width: 100%;
  align-items: center;
  gap: 0.6rem;
}

.card-key-log-search-field {
  position: relative;
  min-width: 0;
  flex: 1 1 auto;
  width: auto;
  max-width: 100%;
}

.card-key-log-refresh-button {
  display: inline-flex;
  height: 2.25rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  border: 1px solid rgb(148 163 184 / 0.45);
  border-radius: 0.65rem;
  background: rgb(248 250 252);
  padding: 0 0.85rem;
  color: rgb(51 65 85);
  font-size: 0.8125rem;
  font-weight: 600;
  transition:
    transform 0.15s ease,
    background-color 0.15s ease,
    color 0.15s ease;
}

.card-key-log-refresh-button:hover {
  transform: translateY(-1px);
}

.card-key-log-refresh-button:disabled {
  cursor: wait;
  opacity: 0.72;
}

.card-key-log-refresh-button:disabled:hover {
  transform: none;
}

.card-key-log-refresh-icon-spinning {
  animation: card-key-log-refresh-spin 0.8s linear infinite;
}

@keyframes card-key-log-refresh-spin {
  to {
    transform: rotate(360deg);
  }
}

.dark .card-key-log-refresh-button {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.card-key-use-logs-button {
  display: inline-flex;
  height: 2.5rem;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
  border: 0;
  border-radius: 0.55rem;
  padding: 0 0.95rem;
  color: white;
  font-size: 0.875rem;
  font-weight: 800;
  line-height: 1;
  white-space: nowrap;
  transition:
    transform 160ms ease,
    box-shadow 160ms ease,
    opacity 160ms ease;
}

.card-key-use-logs-button:disabled {
  cursor: not-allowed;
  opacity: 0.72;
}

.card-key-use-logs-button-danger {
  background: linear-gradient(135deg, rgb(239 68 68), rgb(220 38 38));
  box-shadow: 0 10px 18px rgb(239 68 68 / 0.16);
}

.card-key-cleanup-field {
  display: inline-flex;
  height: 2.5rem;
  min-width: 0;
  align-items: center;
  justify-content: flex-end;
  gap: 0.55rem;
  white-space: nowrap;
}

.card-key-cleanup-label {
  color: rgb(30 41 59);
  font-size: 0.95rem;
  font-weight: 800;
  line-height: 1;
}

.card-key-cleanup-input {
  height: 2.5rem;
  width: 7.35rem;
  min-width: 7.35rem;
  padding: 0 0.85rem;
  font-size: 0.875rem;
  font-weight: 600;
}

.card-key-use-logs-table-shell {
  --card-key-use-logs-divider: rgb(148 163 184 / 0.08);
  min-width: 0;
  width: 100%;
  max-width: 100%;
  min-height: 0;
  scrollbar-color: transparent transparent;
  scrollbar-width: thin;
}

.dark .card-key-use-logs-table-shell {
  --card-key-use-logs-divider: rgb(148 163 184 / 0.12);
}

.card-key-use-logs-table-shell::-webkit-scrollbar {
  width: 0.5rem;
  height: 0.5rem;
}

.card-key-use-logs-table-shell::-webkit-scrollbar-track {
  background: transparent;
}

.card-key-use-logs-table-shell::-webkit-scrollbar-thumb {
  border-radius: 9999px;
  background: transparent;
  transition: background-color 0.2s ease;
}

.card-key-use-logs-table-shell:hover {
  scrollbar-color: rgb(209 213 219 / 0.5) transparent;
}

.card-key-use-logs-table-shell:hover::-webkit-scrollbar-thumb {
  background: rgb(209 213 219 / 0.5);
}

.card-key-use-logs-table-shell:hover::-webkit-scrollbar-thumb:hover {
  background: rgb(156 163 175);
}

.dark .card-key-use-logs-table-shell:hover {
  scrollbar-color: rgb(71 85 105 / 0.5) transparent;
}

.dark .card-key-use-logs-table-shell:hover::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.5);
}

.dark .card-key-use-logs-table-shell:hover::-webkit-scrollbar-thumb:hover {
  background: rgb(100 116 139);
}

.card-key-use-logs-table {
  width: max(100%, 86rem);
  min-width: 86rem;
  table-layout: fixed;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 0.8125rem;
}

.card-key-use-logs-table th,
.card-key-use-logs-table td {
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.65rem 0.8rem !important;
  text-align: left;
  vertical-align: middle;
}

.card-key-use-logs-table th {
  position: sticky;
  top: 0;
  z-index: 5;
  background: rgb(248 250 252);
  color: rgb(100 116 139);
  font-weight: 800;
  letter-spacing: 0;
  padding: 0.78rem 0.85rem !important;
  text-align: center;
  white-space: nowrap;
}

.card-key-use-logs-head-cell {
  border-right: 1px solid var(--card-key-use-logs-divider);
  text-align: center !important;
}

.card-key-use-logs-head-cell:last-child {
  border-right: 0;
}

.card-key-use-logs-table td {
  border-bottom-color: rgb(226 232 240 / 0.88);
  color: rgb(30 41 59);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .card-key-use-logs-table td {
  border-bottom-color: rgb(51 65 85);
  color: rgb(203 213 225);
}

.dark .card-key-use-logs-table th {
  border-bottom-color: rgb(51 65 85);
  background: rgb(30 41 59);
  color: rgb(203 213 225);
}

.card-key-use-logs-col-key { width: 9rem; }
.card-key-use-logs-col-email { width: 16rem; }
.card-key-use-logs-col-subject { width: 41rem; }
.card-key-use-logs-col-ip { width: 9rem; }
.card-key-use-logs-col-time { width: 11rem; }

.card-key-use-logs-empty {
  pointer-events: none;
}

@media (max-width: 1024px) {
  .card-key-use-logs-toolbar {
    grid-template-columns: minmax(16rem, 22rem) 1fr;
  }

  .card-key-logs-actions {
    justify-self: end;
  }
}

@media (max-width: 900px) {
  .card-key-use-logs-page {
    height: calc(100vh - 7.75rem);
    max-height: calc(100vh - 7.75rem);
  }
}

@media (max-width: 720px) {
  .card-key-use-logs-toolbar {
    grid-template-columns: 1fr;
    padding-right: 0.85rem;
    padding-left: 0.85rem;
  }

  .card-key-log-search-actions {
    width: 100%;
  }

  .card-key-logs-actions {
    width: 100%;
    justify-content: flex-end;
  }
}

@media (max-width: 640px) {
  .card-key-use-logs-page {
    height: auto;
    max-height: none;
    overflow: visible;
  }

  .card-key-use-logs-card {
    min-height: 0;
    height: auto;
    max-height: none;
    border-radius: 0.875rem;
  }

  .card-key-use-logs-table-shell {
    min-height: 18rem;
    max-height: 60vh;
  }

  .card-key-logs-actions,
  .card-key-log-search-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .card-key-log-refresh-button,
  .card-key-use-logs-button,
  .card-key-cleanup-field,
  .card-key-cleanup-input {
    width: 100%;
  }

  .card-key-cleanup-field {
    justify-content: stretch;
  }
}
</style>
