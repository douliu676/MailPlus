<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import {
  Check,
  ChevronDown,
  CircleDollarSign,
  Edit,
  Minus,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  X,
} from 'lucide-vue-next'
import PaginationBar from '../components/PaginationBar.vue'
import { useAppStore } from '../stores/app'
import {
  deleteUser,
  listUserBalanceRecords,
  listUsers,
  updateUser,
  updateUserBalance,
  updateUserStatus,
  type AdminUser,
  type BalanceRecord,
  type UserListResponse,
  type UserPayload,
} from '../api/adminUsers'
import { getAdminSettings } from '../api/adminSettings'
import { getSessionItem, setSessionItem } from '../api/session'

type SortKey = 'email' | 'id' | 'username' | 'role' | 'balance' | 'status' | 'created_at'
type SortOrder = 'asc' | 'desc'

const appStore = useAppStore()
const queryClient = useQueryClient()
const loading = ref(false)
const tableLoading = ref(true)
const users = ref<AdminUser[]>([])
const searchQuery = ref('')
const showUserModal = ref(false)
const showDeleteDialog = ref(false)
const showBalanceModal = ref(false)
const showBalanceRecordsModal = ref(false)
const editingUser = ref<AdminUser | null>(null)
const deletingUser = ref<AdminUser | null>(null)
const balanceUser = ref<AdminUser | null>(null)
const balanceRecordUser = ref<AdminUser | null>(null)
const balanceRecords = ref<BalanceRecord[]>([])
const balanceRecordFilter = ref<'all' | 'deposit' | 'deduct'>('all')
const activeMenuId = ref<number | null>(null)
const pageSizeOptions = ref<number[]>([20])
const tableSettingsLoaded = ref(false)
const pageSizeStorageKey = 'admin_users_page_size'
const usersManagementCacheKey = 'admin_users_management_cache_v1'
const usersCacheRestored = ref(false)
const usersTableWrapperRef = ref<HTMLElement | null>(null)

function readStoredUserID() {
  const raw = getSessionItem('auth_user')
  if (!raw) return 0

  try {
    const stored = JSON.parse(raw)
    const id = Number(stored.id)
    return Number.isFinite(id) ? id : 0
  } catch {
    return 0
  }
}

function syncStoredUser(nextUser: AdminUser) {
  const raw = getSessionItem('auth_user')
  let stored = {}

  if (raw) {
    try {
      stored = JSON.parse(raw)
    } catch {
      stored = {}
    }
  }

  setSessionItem('auth_user', JSON.stringify({ ...stored, ...nextUser }))
  window.dispatchEvent(new Event('storage'))
}

function readPersistedPageSize() {
  const value = Number(localStorage.getItem(pageSizeStorageKey))
  return Number.isFinite(value) && value > 0 ? value : 0
}

const pagination = reactive({
  page: 1,
  page_size: readPersistedPageSize(),
  total: 0,
  pages: 0,
})

const form = reactive<UserPayload>({
  username: '',
  email: '',
  password: '',
  balance: 0,
  role: 'user',
  enabled: true,
})

const balanceForm = reactive({
  type: 'deposit' as 'deposit' | 'deduct',
  amount: 0,
  remark: '',
})

const sortState = reactive<{ key: SortKey; order: SortOrder }>({
  key: 'created_at',
  order: 'desc',
})

const modalTitle = computed(() => '编辑用户')
const totalPages = computed(() => Math.max(pagination.pages, 1))
const normalizedPageSizeOptions = computed(() => {
  return Array.from(new Set(pageSizeOptions.value))
    .filter((value) => Number.isFinite(value) && value > 0)
    .sort((a, b) => a - b)
})

const userListQueryKey = computed(() => [
  'admin-users',
  pagination.page,
  pagination.page_size,
  searchQuery.value.trim(),
  sortState.key,
  sortState.order,
])

function applyUserListResponse(response: UserListResponse) {
  users.value = response.items
  pagination.total = response.total
  pagination.pages = response.pages
  pagination.page = response.page
  pagination.page_size = response.page_size
}

function cachedUserListResponse(): UserListResponse {
  return {
    items: users.value,
    total: pagination.total,
    page: pagination.page,
    page_size: pagination.page_size,
    pages: pagination.pages,
  }
}

function restoreUsersManagementCache() {
  try {
    const value = JSON.parse(localStorage.getItem(usersManagementCacheKey) || 'null')
    if (!value || typeof value !== 'object') return
    if (Array.isArray(value.users)) {
      users.value = value.users
      tableLoading.value = false
      usersCacheRestored.value = true
    }
    if (value.pagination && typeof value.pagination === 'object') {
      pagination.page = Number(value.pagination.page) || pagination.page
      pagination.page_size = Number(value.pagination.page_size) || pagination.page_size
      pagination.total = Number(value.pagination.total) || 0
      pagination.pages = Number(value.pagination.pages) || 0
    }
    if (value.query && typeof value.query === 'object') {
      searchQuery.value = String(value.query.search || '')
      if (['email', 'id', 'username', 'role', 'balance', 'status', 'created_at'].includes(value.query.sort_by)) {
        sortState.key = value.query.sort_by
      }
      sortState.order = value.query.sort_order === 'asc' ? 'asc' : 'desc'
    }
    if (Array.isArray(value.page_size_options) && value.page_size_options.length > 0) {
      pageSizeOptions.value = value.page_size_options
        .map((item: unknown) => Number(item))
        .filter((item: number) => Number.isFinite(item) && item > 0)
      tableSettingsLoaded.value = true
    }
    if (usersCacheRestored.value && pagination.page_size > 0) {
      queryClient.setQueryData<UserListResponse>(userListQueryKey.value, cachedUserListResponse())
    }
  } catch {
    // Ignore stale cache.
  }
}

function saveUsersManagementCache() {
  try {
    localStorage.setItem(
      usersManagementCacheKey,
      JSON.stringify({
        users: users.value,
        pagination: {
          page: pagination.page,
          page_size: pagination.page_size,
          total: pagination.total,
          pages: pagination.pages,
        },
        query: {
          search: searchQuery.value,
          sort_by: sortState.key,
          sort_order: sortState.order,
        },
        page_size_options: pageSizeOptions.value,
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

let userRequestID = 0
let usersAutoRefreshEnabled = false
let searchTimer: number | undefined
let userPageBeforeSearch = 1
let userScrollTopBeforeSearch = 0

function currentUsersTableScrollTop() {
  return usersTableWrapperRef.value?.scrollTop ?? 0
}

async function restoreUsersTableScroll(scrollTop: number) {
  await nextTick()
  const wrapper = usersTableWrapperRef.value
  if (!wrapper) return
  const maxScrollTop = Math.max(0, wrapper.scrollHeight - wrapper.clientHeight)
  wrapper.scrollTop = Math.min(scrollTop, maxScrollTop)
}

async function loadUsers(options: { showTableLoading?: boolean; restoreScrollTop?: number } = {}) {
  if (!pagination.page_size) return
  const requestID = ++userRequestID
  const showTableLoading = options.showTableLoading ?? (!usersCacheRestored.value && users.value.length === 0)
  loading.value = true
  const queryKey = userListQueryKey.value
  const params = {
    page: pagination.page,
    page_size: pagination.page_size,
    search: searchQuery.value.trim(),
    sort_by: sortState.key,
    sort_order: sortState.order,
  }
  const cached = queryClient.getQueryData<UserListResponse>(queryKey)
  if (cached) {
    applyUserListResponse(cached)
    tableLoading.value = false
    usersCacheRestored.value = true
    if (usersCacheRestored.value) {
      saveUsersManagementCache()
    }
  } else if (showTableLoading) {
    tableLoading.value = true
  }
  try {
    const response = await queryClient.fetchQuery({
      queryKey,
      queryFn: () => listUsers(params),
      staleTime: 0,
    })
    if (requestID !== userRequestID) return
    if (response.items.length === 0 && response.total > 0 && response.pages > 0 && pagination.page > response.pages) {
      pagination.page = response.pages
      loadUsers(options)
      return
    }
    applyUserListResponse(response)
    if (options.restoreScrollTop !== undefined) {
      await restoreUsersTableScroll(options.restoreScrollTop)
    }
    usersCacheRestored.value = true
    if (usersCacheRestored.value) {
      saveUsersManagementCache()
    }
  } catch (error) {
    if (requestID === userRequestID) {
      appStore.showError(error instanceof Error ? error.message : '获取用户列表失败')
    }
  } finally {
    if (requestID === userRequestID) {
      loading.value = false
      tableLoading.value = false
    }
  }
}

async function refreshUserListAfterMutation() {
  await queryClient.invalidateQueries({ queryKey: ['admin-users'] })
  await loadUsers()
}

async function loadTableSettings() {
  try {
    const settings = await getAdminSettings()
    const defaultPageSize = Number(settings.table_default_page_size || 20)
    const persistedPageSize = readPersistedPageSize()
    const options = Array.isArray(settings.table_page_size_options) ? settings.table_page_size_options : []
    pageSizeOptions.value = options
      .map((value) => Number(value))
      .filter((value) => Number.isFinite(value) && value > 0)

    if (persistedPageSize > 0 && pageSizeOptions.value.includes(persistedPageSize)) {
      pagination.page_size = persistedPageSize
    } else {
      pagination.page_size = Number.isFinite(defaultPageSize) && defaultPageSize > 0 ? defaultPageSize : 20
    }
    saveUsersManagementCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取表格分页设置失败')
  }
}

watch(searchQuery, (value, oldValue) => {
  if (!usersAutoRefreshEnabled) return
  const nextSearch = value.trim()
  const previousSearch = oldValue.trim()
  if (nextSearch && !previousSearch) {
    userPageBeforeSearch = pagination.page
    userScrollTopBeforeSearch = currentUsersTableScrollTop()
  }
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    if (!nextSearch && previousSearch) {
      pagination.page = userPageBeforeSearch
      void loadUsers({ restoreScrollTop: userScrollTopBeforeSearch })
      return
    }
    pagination.page = 1
    void loadUsers()
  }, 300)
})

function setSort(key: SortKey) {
  if (sortState.key === key) {
    sortState.order = sortState.order === 'asc' ? 'desc' : 'asc'
  } else {
    sortState.key = key
    sortState.order = 'asc'
  }
  pagination.page = 1
  loadUsers()
}

function generatePassword() {
  const uppercase = 'ABCDEFGHJKLMNPQRSTUVWXYZ'
  const lowercase = 'abcdefghjkmnpqrstuvwxyz'
  const numbers = '123456789'
  const groups = [uppercase, lowercase, numbers]
  const chars = groups.join('')
  const password = [
    randomChar(uppercase),
    randomChar(lowercase),
    randomChar(numbers),
  ]
  const values = new Uint32Array(12)
  window.crypto.getRandomValues(values)
  for (let index = password.length; index < 12; index += 1) {
    password.push(chars[values[index] % chars.length])
  }
  form.password = shufflePassword(password).join('')
}

function randomChar(chars: string) {
  const value = new Uint32Array(1)
  window.crypto.getRandomValues(value)
  return chars[value[0] % chars.length]
}

function shufflePassword(chars: string[]) {
  const values = new Uint32Array(chars.length)
  window.crypto.getRandomValues(values)
  for (let index = chars.length - 1; index > 0; index -= 1) {
    const swapIndex = values[index] % (index + 1)
    ;[chars[index], chars[swapIndex]] = [chars[swapIndex], chars[index]]
  }
  return chars
}

function openEditModal(user: AdminUser) {
  editingUser.value = user
  form.username = user.username
  form.email = user.email
  form.password = ''
  form.balance = user.balance
  form.role = user.role
  form.enabled = user.status === 'active'
  activeMenuId.value = null
  showUserModal.value = true
}

async function saveUser() {
  if (!editingUser.value) return
  try {
    const updated = await updateUser(editingUser.value.id, form)
    if (updated.id === readStoredUserID()) {
      syncStoredUser(updated)
    }
    appStore.showSuccess('用户已更新')
    showUserModal.value = false
    await refreshUserListAfterMutation()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存用户失败')
  }
}

function openBalanceModal(user: AdminUser) {
  balanceUser.value = user
  balanceForm.type = 'deposit'
  balanceForm.amount = 0
  balanceForm.remark = ''
  activeMenuId.value = null
  showBalanceModal.value = true
}

function openRefundModal(user: AdminUser) {
  balanceUser.value = user
  balanceForm.type = 'deduct'
  balanceForm.amount = 0
  balanceForm.remark = ''
  activeMenuId.value = null
  showBalanceModal.value = true
}

async function openBalanceRecordsModal(user: AdminUser) {
  balanceRecordUser.value = user
  balanceRecordFilter.value = 'all'
  activeMenuId.value = null
  showBalanceRecordsModal.value = true
  await loadBalanceRecords()
}

async function saveBalance() {
  if (!balanceUser.value) return
  const userID = balanceUser.value.id
  try {
    const updated = await updateUserBalance(userID, balanceForm)
    if (updated.id === readStoredUserID()) {
      syncStoredUser(updated)
    }
    appStore.showSuccess('余额已更新')
    showBalanceModal.value = false
    await queryClient.invalidateQueries({ queryKey: ['admin-user-balance-records', userID] })
    await refreshUserListAfterMutation()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '余额更新失败')
  }
}

async function loadBalanceRecords() {
  if (!balanceRecordUser.value) return
  try {
    const userID = balanceRecordUser.value.id
    const filter = balanceRecordFilter.value
    const response = await queryClient.fetchQuery({
      queryKey: ['admin-user-balance-records', userID, filter],
      queryFn: () => listUserBalanceRecords(userID, filter),
      staleTime: 0,
    })
    balanceRecordUser.value = response.user
    balanceRecords.value = response.records
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取充值记录失败')
  }
}

function fillRefundAll() {
  balanceForm.amount = Number((balanceUser.value?.balance || 0).toFixed(2))
}

async function toggleStatus(user: AdminUser) {
  const nextStatus = user.status === 'active' ? 'disabled' : 'active'
  try {
    await updateUserStatus(user.id, nextStatus)
    appStore.showSuccess(nextStatus === 'active' ? '用户已启用' : '用户已禁用')
    activeMenuId.value = null
    await refreshUserListAfterMutation()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '更新用户状态失败')
  }
}

function openDeleteDialog(user: AdminUser) {
  deletingUser.value = user
  activeMenuId.value = null
  showDeleteDialog.value = true
}

async function confirmDelete() {
  if (!deletingUser.value) return
  try {
    await deleteUser(deletingUser.value.id)
    appStore.showSuccess('用户已删除')
    showDeleteDialog.value = false
    deletingUser.value = null
    await refreshUserListAfterMutation()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除用户失败')
  }
}

function changePage(page: number) {
  const valid = Math.max(1, Math.min(page, totalPages.value))
  if (valid === pagination.page) return
  pagination.page = valid
  loadUsers()
}

function handlePageSizeChange() {
  pagination.page = 1
  loadUsers()
}

function selectPageSize(size: number) {
  pagination.page_size = size
  localStorage.setItem(pageSizeStorageKey, String(size))
  if (usersCacheRestored.value) {
    saveUsersManagementCache()
  }
  handlePageSizeChange()
}

function formatDateTime(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatMoney(value: number) {
  return `$${Number(value || 0).toFixed(2)}`
}

function showTodo(message: string) {
  activeMenuId.value = null
  appStore.showInfo(message)
}

function toggleActionMenu(user: AdminUser) {
  activeMenuId.value = activeMenuId.value === user.id ? null : user.id
}

function closeMenus(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (!target.closest('[data-user-action-menu]')) {
    activeMenuId.value = null
  }
}

restoreUsersManagementCache()

onMounted(async () => {
  await loadTableSettings()
  if (!pagination.page_size) {
    pagination.page_size = 20
  }
  tableSettingsLoaded.value = true
  if (usersCacheRestored.value) {
    saveUsersManagementCache()
  }
  usersAutoRefreshEnabled = true
  await loadUsers()
  document.addEventListener('click', closeMenus)
})

onBeforeUnmount(() => {
  window.clearTimeout(searchTimer)
  document.removeEventListener('click', closeMenus)
})
</script>

<template>
  <div class="user-page-layout min-h-[calc(100vh-8rem)] gap-3">
    <section class="user-table-card overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/50">
      <div class="user-table-toolbar flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div class="search-clear-field relative max-w-full" style="width: min(350px, 100%); flex: 0 0 min(350px, 100%);">
          <Search class="absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-gray-400" />
          <input
            v-model="searchQuery"
            class="input search-clear-input h-9 pl-10 text-sm"
            type="text"
            placeholder="邮箱/用户名"
          />
          <button v-if="searchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="searchQuery = ''">
            <X class="h-3.5 w-3.5" />
          </button>
        </div>

        <div class="flex flex-wrap items-center justify-end gap-2">
          <button class="btn btn-secondary h-9 px-3" type="button" :disabled="loading" title="刷新" @click="loadUsers()">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </button>

        </div>
      </div>

      <div ref="usersTableWrapperRef" class="table-wrapper users-table-wrapper">
        <table class="w-full min-w-max divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="table-header bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th
                v-for="column in [
                  ['email', '邮箱'],
                  ['id', 'ID'],
                  ['username', '用户名'],
                  ['role', '角色'],
                  ['balance', '余额'],
                  ['status', '状态'],
                  ['created_at', '创建时间'],
                ]"
                :key="column[0]"
                class="sticky-header-cell px-5 py-4 font-medium"
              >
                <button class="inline-flex items-center gap-1.5 transition-colors hover:text-gray-900 dark:hover:text-white" type="button" @click="setSort(column[0] as SortKey)">
                  {{ column[1] }}
                  <ChevronDown
                    class="h-3.5 w-3.5 transition-transform"
                    :class="{
                      'rotate-180 text-gray-700 dark:text-gray-200': sortState.key === column[0] && sortState.order === 'asc',
                      'text-gray-400 dark:text-dark-400': sortState.key !== column[0] || sortState.order === 'desc',
                    }"
                  />
                </button>
              </th>
              <th class="sticky-header-cell sticky-col sticky-col-right px-5 py-4 text-center font-medium">操作</th>
            </tr>
          </thead>
          <tbody class="table-body divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-if="tableLoading">
              <td colspan="8" class="px-6 py-12 text-center text-gray-500 dark:text-dark-400">加载中...</td>
            </tr>
            <tr v-else-if="users.length === 0">
              <td colspan="8" class="px-6 py-12 text-center text-gray-500 dark:text-dark-400">暂无用户</td>
            </tr>
            <tr v-for="item in users" v-else :key="item.id" class="hover:bg-gray-50 dark:hover:bg-dark-800">
              <td class="sticky-col sticky-col-right bg-white px-5 py-5 dark:bg-dark-900">
                <div class="flex items-center gap-3">
                  <div class="flex h-8 w-8 items-center justify-center overflow-hidden rounded-full bg-primary-100 dark:bg-primary-900/30">
                    <img v-if="item.avatar_url" :src="item.avatar_url" :alt="item.username" class="h-full w-full object-cover" />
                    <span v-else class="text-sm font-medium text-primary-700 dark:text-primary-300">{{ (item.email || item.username).charAt(0).toUpperCase() }}</span>
                  </div>
                  <span class="font-medium text-gray-900 dark:text-white">{{ item.email || '-' }}</span>
                </div>
              </td>
              <td class="px-5 py-5 text-gray-700 dark:text-gray-300">{{ item.id }}</td>
              <td class="px-5 py-5 text-gray-700 dark:text-gray-300">{{ item.username || '-' }}</td>
              <td class="px-5 py-5">
                <span class="badge" :class="item.role === 'admin' ? 'badge-purple' : 'badge-gray'">
                  {{ item.role === 'admin' ? '管理员' : '普通用户' }}
                </span>
              </td>
              <td class="px-5 py-5">
                <button class="font-medium text-gray-900 underline decoration-dashed decoration-gray-300 underline-offset-4 transition-colors hover:text-primary-600 dark:text-white dark:decoration-dark-500 dark:hover:text-primary-400" type="button" @click="openBalanceRecordsModal(item)">
                  {{ formatMoney(item.balance) }}
                </button>
                <button class="ml-2 rounded px-2 py-0.5 text-xs font-medium text-emerald-600 transition-colors hover:bg-emerald-50 dark:text-emerald-400 dark:hover:bg-emerald-900/20" type="button" @click="openBalanceModal(item)">
                  充值
                </button>
              </td>
              <td class="px-5 py-5">
                <div class="flex items-center gap-1.5">
                  <span class="inline-block h-2 w-2 rounded-full" :class="item.status === 'active' ? 'bg-green-500' : 'bg-red-500'"></span>
                  <span class="text-sm text-gray-700 dark:text-gray-300">{{ item.status === 'active' ? '启用' : '禁用' }}</span>
                </div>
              </td>
              <td class="px-5 py-5 text-gray-500 dark:text-dark-400">{{ formatDateTime(item.created_at) }}</td>
              <td class="px-5 py-5">
                <div class="flex items-center justify-center gap-2">
                  <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400" type="button" @click="openEditModal(item)">
                    <Edit class="h-4 w-4" />
                    <span class="text-xs">编辑</span>
                  </button>
                  <div class="relative" data-user-action-menu>
                    <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white" type="button" @click.stop="toggleActionMenu(item)">
                      <MoreHorizontal class="h-4 w-4" />
                      <span class="text-xs">更多</span>
                    </button>
                    <div v-if="activeMenuId === item.id" class="absolute right-0 top-full z-40 mt-1 w-48 overflow-hidden rounded-lg border border-gray-200 bg-white py-1 text-left shadow-lg dark:border-dark-600 dark:bg-dark-800">
                      <button class="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700" type="button" @click="openBalanceModal(item)">
                        <Plus class="h-4 w-4 text-emerald-500" />
                        <span>充值</span>
                      </button>
                      <button class="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700" type="button" @click="openRefundModal(item)">
                        <Minus class="h-4 w-4 text-amber-500" />
                        <span>退款</span>
                      </button>
                      <button class="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700" type="button" @click="openBalanceRecordsModal(item)">
                        <CircleDollarSign class="h-4 w-4 text-gray-400 dark:text-gray-300" />
                        <span>充值记录</span>
                      </button>
                      <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
                      <button v-if="item.role !== 'admin'" class="dropdown-item w-full" type="button" @click="toggleStatus(item)">
                        {{ item.status === 'active' ? '禁用用户' : '启用用户' }}
                      </button>
                      <button v-if="item.role !== 'admin'" class="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-red-600 hover:bg-red-50 dark:text-red-300 dark:hover:bg-red-950/30" type="button" @click="openDeleteDialog(item)">
                        <Trash2 class="h-4 w-4" />
                        <span>删除</span>
                      </button>
                    </div>
                  </div>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="tableSettingsLoaded" class="user-pagination-footer flex items-center justify-between border-t border-gray-200 bg-gray-50 px-5 py-3 dark:border-dark-700 dark:bg-dark-800">
        <PaginationBar
          :page="pagination.page"
          :pages="totalPages"
          :page-size="pagination.page_size"
          :page-size-options="normalizedPageSizeOptions"
          :total="pagination.total"
          @page-change="changePage"
          @page-size-change="selectPageSize"
        />
      </div>
    </section>

    <Teleport to="body">
      <div v-if="showUserModal" class="user-modal-mask fixed inset-0 z-[60] flex items-center justify-center overflow-hidden p-4">
        <div class="user-form-modal flex w-full max-w-lg flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 border-b border-gray-200 px-6 py-5 dark:border-dark-700">
            <h3 class="text-lg font-bold">{{ modalTitle }}</h3>
          </div>
          <div class="user-form-modal-body space-y-4 px-6 py-5">
            <label class="block">
              <span class="input-label">邮箱</span>
              <input v-model="form.email" class="input" type="email" />
            </label>
            <label class="block">
              <span class="input-label">用户名</span>
              <input v-model="form.username" class="input" type="text" />
            </label>
            <label class="block">
              <span class="input-label">密码</span>
              <div class="flex gap-2">
                <input v-model="form.password" class="input" type="text" placeholder="留空则不修改" />
                <button class="btn btn-secondary shrink-0 px-3" type="button" title="随机生成密码" @click="generatePassword">
                  <RefreshCw class="h-5 w-5" />
                </button>
              </div>
            </label>
            <label class="block">
              <span class="input-label">余额</span>
              <input v-model.number="form.balance" class="input" type="number" min="0" step="0.01" />
            </label>
          </div>
          <div class="shrink-0 flex justify-end gap-2 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="showUserModal = false">取消</button>
            <button class="btn btn-primary" type="button" @click="saveUser">
              <Check class="h-5 w-5" />
              保存
            </button>
          </div>
        </div>
      </div>

      <div v-if="showBalanceModal" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/45 p-4 backdrop-blur-sm">
        <div class="balance-modal w-full max-w-md overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-600 dark:bg-dark-800">
          <div class="flex items-center justify-between border-b border-gray-200 px-6 py-5 dark:border-dark-600">
            <h3 class="text-xl font-bold">{{ balanceForm.type === 'deposit' ? '充值' : '退款' }}</h3>
            <button class="modal-close-button" type="button" @click="showBalanceModal = false">
              <X class="h-5 w-5" />
            </button>
          </div>

          <div class="space-y-5 px-6 py-5">
            <div class="flex items-center gap-4 rounded-xl bg-gray-100 px-4 py-4 dark:bg-dark-700">
              <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-emerald-100 text-lg font-semibold text-emerald-700 dark:bg-emerald-200 dark:text-emerald-800">
                {{ (balanceUser?.email || balanceUser?.username || 'U').charAt(0).toUpperCase() }}
              </div>
              <div class="min-w-0">
                <div class="truncate text-base text-gray-900 dark:text-white">{{ balanceUser?.email || '-' }}</div>
                <div class="text-sm text-gray-500 dark:text-dark-400">当前余额: {{ formatMoney(balanceUser?.balance || 0) }}</div>
              </div>
            </div>

            <label class="block">
              <span class="input-label">{{ balanceForm.type === 'deposit' ? '充值金额' : '退款金额' }}</span>
              <div class="flex gap-2">
                <div class="relative flex-1">
                  <span class="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-gray-400">$</span>
                  <input v-model.number="balanceForm.amount" class="input pl-10" type="number" min="0" step="0.01" />
                </div>
                <button v-if="balanceForm.type === 'deduct'" class="btn btn-secondary px-4" type="button" @click="fillRefundAll">全部</button>
              </div>
            </label>

            <label class="block">
              <span class="input-label">备注</span>
              <textarea v-model="balanceForm.remark" class="input min-h-24 resize-y py-3"></textarea>
            </label>
          </div>

          <div class="flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-600">
            <button class="btn btn-secondary" type="button" @click="showBalanceModal = false">取消</button>
            <button class="btn" :class="balanceForm.type === 'deposit' ? 'btn-primary' : 'bg-red-600 text-white hover:bg-red-500'" type="button" @click="saveBalance">确认</button>
          </div>
        </div>
      </div>

      <div v-if="showBalanceRecordsModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4 backdrop-blur-sm">
        <div class="balance-record-modal w-full max-w-3xl overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-600 dark:bg-dark-800">
          <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-600">
            <h3 class="text-lg font-bold">用户充值和并发变动记录</h3>
            <button class="modal-close-button" type="button" @click="showBalanceRecordsModal = false">
              <X class="h-5 w-5" />
            </button>
          </div>

          <div class="space-y-4 px-5 py-4">
            <div class="rounded-xl bg-gray-100 px-4 py-4 dark:bg-dark-700">
              <div class="flex items-center justify-between gap-4">
                <div class="flex min-w-0 items-center gap-4">
                  <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-emerald-100 text-base font-semibold text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300">
                    {{ (balanceRecordUser?.email || balanceRecordUser?.username || 'U').charAt(0).toUpperCase() }}
                  </div>
                  <div class="min-w-0">
                    <div class="flex min-w-0 items-center gap-3">
                      <div class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ balanceRecordUser?.email || '-' }}</div>
                      <span class="rounded bg-emerald-500/15 px-2 py-0.5 text-xs font-semibold text-emerald-500">{{ balanceRecordUser?.username || '-' }}</span>
                    </div>
                    <div class="text-xs text-gray-500 dark:text-dark-400">创建时间: {{ formatDateTime(balanceRecordUser?.created_at) }}</div>
                  </div>
                </div>
                <div class="text-right">
                  <div class="text-xs text-gray-500 dark:text-dark-400">当前余额</div>
                  <div class="text-xl font-bold text-gray-900 dark:text-white">{{ formatMoney(balanceRecordUser?.balance || 0) }}</div>
                </div>
              </div>
              <div class="mt-3 border-t border-gray-200 pt-3 text-right text-xs dark:border-dark-600">
                <span class="text-gray-500 dark:text-dark-400">总充值: </span>
                <span class="font-semibold text-emerald-500">{{ formatMoney(balanceRecords.filter((item) => item.type === 'deposit').reduce((sum, item) => sum + item.amount, 0)) }}</span>
              </div>
            </div>

            <div class="flex flex-wrap items-center gap-3">
              <select v-model="balanceRecordFilter" class="input h-10 w-48" @change="loadBalanceRecords">
                <option value="all">全部类型</option>
                <option value="deposit">充值</option>
                <option value="deduct">退款</option>
              </select>
              <button class="btn btn-secondary h-10" type="button" @click="balanceRecordUser && openBalanceModal(balanceRecordUser)">
                <Plus class="h-4 w-4 text-emerald-500" />
                充值
              </button>
              <button class="btn btn-secondary h-10" type="button" @click="balanceRecordUser && openRefundModal(balanceRecordUser)">
                <Minus class="h-4 w-4 text-amber-500" />
                退款
              </button>
            </div>

            <div v-if="balanceRecords.length === 0" class="py-12 text-center text-sm text-gray-500 dark:text-dark-400">暂无变动记录</div>
            <div v-else class="space-y-3">
              <div v-for="record in balanceRecords" :key="record.id" class="flex items-center justify-between rounded-xl border border-gray-200 bg-white px-4 py-3 dark:border-dark-600 dark:bg-dark-800/60">
                <div class="flex min-w-0 items-center gap-4">
                  <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-emerald-500/10 text-emerald-500">
                    <CircleDollarSign class="h-4 w-4" />
                  </div>
                  <div class="min-w-0">
                    <div class="text-sm font-semibold text-gray-900 dark:text-white">
                      {{ record.type === 'deposit' ? '余额充值（管理员）' : '余额退款（管理员）' }}
                    </div>
                    <div class="text-xs text-gray-500 dark:text-dark-400">{{ formatDateTime(record.created_at) }}</div>
                  </div>
                </div>
                <div class="text-right">
                  <div class="text-base font-bold" :class="record.type === 'deposit' ? 'text-emerald-500' : 'text-amber-500'">
                    {{ record.type === 'deposit' ? '+' : '-' }}{{ formatMoney(record.amount) }}
                  </div>
                  <div class="text-xs text-gray-500 dark:text-dark-400">{{ record.remark || '管理员调整' }}</div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <div v-if="showDeleteDialog" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div class="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-dark-700 dark:bg-dark-900">
        <h3 class="text-lg font-bold">删除用户</h3>
        <p class="mt-3 text-sm text-gray-500 dark:text-dark-400">确定要删除用户 {{ deletingUser?.username }} 吗？此操作无法撤销。</p>
        <div class="mt-6 flex justify-end gap-2">
          <button class="btn btn-secondary" type="button" @click="showDeleteDialog = false">取消</button>
          <button class="btn bg-red-600 text-white hover:bg-red-500" type="button" @click="confirmDelete">删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.user-page-layout {
  display: flex;
  align-items: stretch;
  min-width: 0;
  width: 100%;
}

.user-table-card {
  display: flex;
  min-width: 0;
  max-width: 100%;
  flex: 1 1 0;
  flex-direction: column;
  min-height: calc(100vh - 8rem);
}

.user-table-toolbar {
  padding: 0.75rem 1rem;
}

.users-table-wrapper {
  flex: 1;
  height: 100%;
  min-height: 0;
}

.user-pagination-footer {
  flex: 0 0 auto;
}

.user-form-modal {
  max-height: calc(100vh - 2rem);
}

.user-modal-mask {
  background: rgb(0 0 0 / 0.45);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
}

.modal-close-button {
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

.modal-close-button:hover {
  background: rgb(226 232 240 / 0.9);
  color: rgb(71 85 105);
}

.dark .modal-close-button {
  background: transparent;
  color: rgb(203 213 225);
}

.dark .modal-close-button:hover {
  background: rgb(51 65 85 / 0.9);
  color: white;
}

.user-form-modal-body {
  min-height: 0;
  overflow-y: auto;
}

.user-form-modal-body::-webkit-scrollbar {
  width: 0.55rem;
}

.user-form-modal-body::-webkit-scrollbar-track {
  border-radius: 999px;
  background: rgb(226 232 240 / 0.8);
}

.user-form-modal-body::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.85);
}

.dark .user-form-modal-body::-webkit-scrollbar-track {
  background: rgb(15 23 42 / 0.75);
}

.dark .user-form-modal-body::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.95);
}

.page-size-trigger {
  display: inline-flex;
  width: 5rem;
  height: 2.25rem;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  border: 1px solid rgb(203 213 225);
  border-radius: 0.75rem;
  background: white;
  padding: 0 0.75rem;
  font-size: 0.875rem;
  color: rgb(51 65 85);
  outline: none;
  transition: border-color 0.2s ease, background-color 0.2s ease, color 0.2s ease, box-shadow 0.2s ease;
}

.page-size-trigger:hover,
.page-size-trigger:focus-visible {
  border-color: rgb(20 184 166);
  box-shadow: 0 0 0 1px rgb(20 184 166 / 0.35);
}

html.dark .page-size-trigger {
  border-color: rgb(20 184 166 / 0.75);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.page-jump-form {
  margin-left: 0.5rem;
  display: inline-flex;
  height: 2.25rem;
  align-items: stretch;
}

.page-jump-input {
  width: 4.25rem;
  border: 1px solid rgb(203 213 225);
  border-right: 0;
  border-radius: 0.5rem 0 0 0.5rem;
  background: rgb(255 255 255);
  padding: 0 0.5rem;
  text-align: center;
  font-size: 0.8125rem;
  color: rgb(51 65 85);
  outline: none;
}

.page-jump-input:focus {
  border-color: rgb(20 184 166);
  box-shadow: 0 0 0 1px rgb(20 184 166 / 0.55);
}

.page-jump-input::placeholder {
  color: rgb(148 163 184);
}

.page-jump-input::-webkit-outer-spin-button,
.page-jump-input::-webkit-inner-spin-button {
  margin: 0;
  appearance: none;
}

.page-jump-input[type='number'] {
  appearance: textfield;
}

.page-jump-button {
  display: inline-flex;
  min-width: 2.25rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(203 213 225);
  border-radius: 0 0.5rem 0.5rem 0;
  background: rgb(248 250 252);
  color: rgb(71 85 105);
  font-size: 0.9rem;
  font-weight: 700;
  transition: border-color 0.15s ease, background-color 0.15s ease, color 0.15s ease;
}

.page-jump-button:hover {
  border-color: rgb(20 184 166);
  background: rgb(240 253 250);
  color: rgb(13 148 136);
}

html.dark .page-jump-input {
  border-color: rgb(55 65 81);
  background: rgb(15 23 42 / 0.85);
  color: rgb(226 232 240);
}

html.dark .page-jump-input::placeholder {
  color: rgb(148 163 184 / 0.62);
}

html.dark .page-jump-button {
  border-color: rgb(55 65 81);
  background: rgb(30 41 59);
  color: rgb(148 163 184);
}

html.dark .page-jump-button:hover {
  border-color: rgb(20 184 166);
  background: rgb(30 41 59);
  color: rgb(45 212 191);
}

.page-size-menu {
  position: absolute;
  right: 0;
  bottom: calc(100% + 0.5rem);
  z-index: 60;
  width: 5rem;
  overflow: hidden;
  border: 1px solid rgb(226 232 240);
  border-radius: 0.75rem;
  background: white;
  box-shadow: 0 18px 42px rgb(15 23 42 / 0.18);
}

.page-size-option {
  display: flex;
  width: 100%;
  height: 2.5rem;
  align-items: center;
  justify-content: space-between;
  padding: 0 0.9rem;
  font-size: 0.875rem;
  color: rgb(51 65 85);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.page-size-option:hover {
  background: rgb(241 245 249);
}

.page-size-option-active {
  background: rgb(240 253 250);
  color: rgb(13 148 136);
}

html.dark .page-size-menu {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59);
  box-shadow: 0 18px 42px rgb(0 0 0 / 0.35);
}

html.dark .page-size-option {
  color: rgb(226 232 240);
}

html.dark .page-size-option:hover {
  background: rgb(51 65 85);
}

html.dark .page-size-option-active {
  background: rgb(51 65 85 / 0.9);
  color: rgb(45 212 191);
}

.table-wrapper {
  --select-col-width: 52px;
  position: relative;
  height: 100%;
  overflow-x: auto;
  overflow-y: auto;
  isolation: isolate;
}

.table-wrapper .table-header {
  position: sticky;
  top: 0;
  z-index: 200;
  background-color: rgb(249 250 251);
}

.dark .table-wrapper .table-header {
  background-color: rgb(31 41 55);
}

.table-body {
  position: relative;
  z-index: 0;
}

.sticky-header-cell {
  position: sticky;
  top: 0;
  z-index: 210;
  background-color: rgb(249 250 251);
}

.dark .sticky-header-cell {
  background-color: rgb(31 41 55);
}

.sticky-col {
  position: sticky;
  z-index: 20;
}

.sticky-col-right {
  right: 0;
}

.sticky-header-cell.sticky-col {
  z-index: 220;
}

tbody .sticky-col {
  background-color: white;
}

.dark tbody .sticky-col {
  background-color: rgb(17 24 39);
}

tbody tr:hover .sticky-col {
  background-color: rgb(249 250 251);
}

.dark tbody tr:hover .sticky-col {
  background-color: rgb(31 41 55);
}

.sticky-col-right::before {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  width: 10px;
  pointer-events: none;
  content: '';
  transform: translateX(-100%);
  background: linear-gradient(to left, rgb(0 0 0 / 0.08), transparent);
}

.dark .sticky-col-right::before {
  background: linear-gradient(to left, rgb(0 0 0 / 0.2), transparent);
}

.table-wrapper {
  scrollbar-width: auto !important;
}

.table-wrapper::-webkit-scrollbar {
  display: block !important;
  width: 12px !important;
  height: 12px !important;
  background-color: transparent !important;
}

.table-wrapper::-webkit-scrollbar-track {
  margin: 0 4px !important;
  background-color: rgb(0 0 0 / 0.03) !important;
  border-radius: 6px !important;
}

.dark .table-wrapper::-webkit-scrollbar-track {
  background-color: rgb(255 255 255 / 0.05) !important;
}

.table-wrapper::-webkit-scrollbar-thumb {
  background-color: rgb(107 114 128 / 0.75) !important;
  border: 2px solid transparent !important;
  border-radius: 6px !important;
  background-clip: padding-box !important;
}

.table-wrapper::-webkit-scrollbar-thumb:hover {
  background-color: rgb(75 85 99 / 0.9) !important;
}

.dark .table-wrapper::-webkit-scrollbar-thumb {
  background-color: rgb(156 163 175 / 0.75) !important;
}

.dark .table-wrapper::-webkit-scrollbar-thumb:hover {
  background-color: rgb(209 213 219 / 0.9) !important;
}

@media (max-width: 767px) {
  .user-page-layout {
    gap: 0.75rem;
  }

  .user-table-card {
    min-height: calc(100vh - 18rem);
    border-radius: 0.875rem;
  }

  .user-table-toolbar {
    align-items: stretch;
    padding: 0.75rem;
  }

  .user-table-toolbar > div {
    width: 100% !important;
    flex-basis: 100% !important;
  }

  .user-pagination-footer {
    flex-wrap: wrap;
    gap: 0.75rem;
    padding: 0.75rem;
  }

  .table-wrapper table {
    min-width: 820px;
  }

  .user-form-modal,
  .balance-modal,
  .balance-record-modal {
    max-width: calc(100vw - 1.5rem) !important;
    max-height: calc(100vh - 1.5rem);
  }

  .user-form-modal-body,
  .balance-record-modal > div:last-child {
    overflow-y: auto;
  }

  .page-size-menu {
    right: auto;
    left: 0;
  }
}

@media (min-width: 1600px) {
  .user-table-card {
    min-height: min(calc(100vh - 8rem), 920px);
  }
}

@media (max-width: 640px) {
  .user-table-card {
    min-height: 0;
  }

  .users-table-wrapper {
    min-height: 18rem;
  }

  .user-pagination-footer {
    align-items: stretch;
    flex-direction: column;
  }

  .user-form-modal,
  .balance-modal,
  .balance-record-modal {
    width: calc(100vw - 1rem);
    max-width: calc(100vw - 1rem) !important;
    max-height: calc(100svh - 1rem);
    border-radius: 0.875rem;
  }

  .balance-record-modal > div:nth-child(2) {
    max-height: calc(100svh - 5.5rem);
    overflow-y: auto;
  }

  .balance-record-modal > div:nth-child(2) > div:first-child > .flex {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (max-width: 420px) {
  .user-table-toolbar .btn,
  .user-pagination-footer .btn {
    width: 100%;
  }

  .user-form-modal .border-t,
  .balance-modal .border-t {
    align-items: stretch;
    flex-direction: column-reverse;
  }

  .balance-record-modal select,
  .balance-record-modal .btn {
    width: 100%;
  }
}
</style>

<style>
html.dark .page-size-menu {
  width: 5rem !important;
  border-color: rgb(51 65 85) !important;
  background: rgb(30 41 59) !important;
  box-shadow: 0 18px 42px rgb(0 0 0 / 0.35) !important;
}

html.dark .page-size-option {
  color: rgb(226 232 240) !important;
  background: transparent !important;
}

html.dark .page-size-option:hover {
  background: rgb(51 65 85) !important;
}

html.dark .page-size-option-active {
  color: rgb(45 212 191) !important;
  background: rgb(51 65 85 / 0.9) !important;
}
</style>
