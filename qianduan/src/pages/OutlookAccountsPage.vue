<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { Check, ChevronDown, ChevronRight, CircleHelp, Copy, Download, ExternalLink, Folder, FolderPlus, Inbox, KeyRound, MoreHorizontal, Pencil, Play, Plus, RefreshCw, Search, StickyNote, Trash2, Upload, X } from 'lucide-vue-next'
import PaginationBar from '../components/PaginationBar.vue'
import SafeMailFrame from '../components/SafeMailFrame.vue'
import { useAppStore } from '../stores/app'
import { useTaskStore } from '../stores/tasks'
import { getAdminSettings } from '../api/adminSettings'
import { batchCreateOutlookAccounts, batchOutlookAction, createOutlookAccount, createOutlookDataExportTask, createOutlookDataImportTask, createOutlookGroup, deleteOutlookAccount, deleteOutlookGroup, exchangeOutlookCode, getOutlookAuthorizeURL, getOutlookMessageDetail, getOutlookOAuthResult, listOutlookAccounts, listOutlookGroups, listOutlookMessages, testOutlookAccount, updateOutlookAccount, updateOutlookGroup, type AccountListFilter, type BackgroundTask, type OutlookAccount, type OutlookAccountListResponse, type OutlookGroup, type OutlookMessage } from '../api/outlookAccounts'
import { copyToClipboard } from '../utils/clipboard'
import { mailContactDetail, mailContactEmails } from '../utils/mailContacts'
import { normalizeOutlookAccountPageCache, outlookAccountPageCacheKey, outlookManagementCacheKey, rememberOutlookAccountPage, type OutlookAccountPageCacheEntry } from '../utils/outlookManagementCache'
import { authSessionClearedEvent } from '../api/session'

const appStore = useAppStore()
const taskStore = useTaskStore()
const queryClient = useQueryClient()
const searchQuery = ref('')
const activeGroupID = ref(0)
const defaultOutlookClientID = '9e5f94bc-e8a4-4e73-b8be-63364c29d753'
const defaultOutlookOAuthClientID = 'e69b7798-fa18-4d45-9dc2-a4a40580588d'
const defaultOutlookManualRedirectURI = 'https://localhost'
const pageSizeStorageKey = 'outlook_accounts_page_size'
const fallbackTablePageSize = 20
const fallbackTablePageSizeOptions = [10, 20, 50, 100]
const groupExpandedStorageKey = 'outlook_group_expanded_ids'
const outlookSortStorageKey = 'outlook_accounts_sort'
function readPersistedPageSize() {
  const value = Number(localStorage.getItem(pageSizeStorageKey))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function normalizePageSizeOptions(values: unknown, defaultPageSize: number) {
  const options = Array.isArray(values) ? values : fallbackTablePageSizeOptions
  const result = options.map((value) => Number(value)).filter((value) => Number.isFinite(value) && value > 0)
  if (Number.isFinite(defaultPageSize) && defaultPageSize > 0) {
    result.push(defaultPageSize)
  }
  return Array.from(new Set(result)).sort((a, b) => a - b)
}

type PaginationItem = { key: string; type: 'page'; page: number } | { key: string; type: 'ellipsis' }

function buildPaginationItems(currentPage: number, totalPages: number): PaginationItem[] {
  const pages = Math.max(1, Math.floor(Number(totalPages) || 1))
  const current = Math.max(1, Math.min(Math.floor(Number(currentPage) || 1), pages))
  const items: PaginationItem[] = []
  const addPage = (page: number) => items.push({ key: `page-${page}`, type: 'page', page })
  const addEllipsis = (key: string) => items.push({ key, type: 'ellipsis' })

  if (pages <= 7) {
    for (let page = 1; page <= pages; page += 1) addPage(page)
    return items
  }

  if (current <= 4) {
    for (let page = 1; page <= 4; page += 1) addPage(page)
    addEllipsis('ellipsis-end')
    addPage(pages)
    return items
  }

  if (current >= pages - 3) {
    addPage(1)
    addEllipsis('ellipsis-start')
    for (let page = pages - 3; page <= pages; page += 1) addPage(page)
    return items
  }

  addPage(1)
  addEllipsis('ellipsis-start')
  for (let page = current - 1; page <= current + 1; page += 1) addPage(page)
  addEllipsis('ellipsis-end')
  addPage(pages)
  return items
}

function readPersistedExpandedGroupIDs() {
  try {
    const value = JSON.parse(localStorage.getItem(groupExpandedStorageKey) || '[]')
    return Array.isArray(value) ? value.map((item) => Number(item)).filter((item) => Number.isFinite(item)) : []
  } catch {
    return []
  }
}

const groups = ref<OutlookGroup[]>([
  { id: 1, parent_id: 0, name: '全部微软邮箱', system: true, sort_order: 0, count: 0, created_at: '' },
  { id: 2, parent_id: 0, name: '默认分组', system: true, sort_order: 0, count: 0, created_at: '' },
])
const accounts = ref<OutlookAccount[]>([])
const accountPageCache = ref<Record<string, OutlookAccountPageCacheEntry>>({})
const selectedIDs = ref<number[]>([])
const expandedGroupIDs = ref<number[]>(readPersistedExpandedGroupIDs())
const groupNameScrollX = ref(0)
const groupNameScrollMax = ref(0)
const pageSize = ref(readPersistedPageSize() || fallbackTablePageSize)
const accountPage = ref(1)
const accountTotal = ref(0)
const accountPages = ref(0)
const accountPageJump = ref('')
const outlookVirtualScrollTop = ref(0)
const outlookVirtualViewportHeight = ref(520)
const outlookVirtualRowHeight = 74
const outlookVirtualOverscan = 6
const pageSizeDropdownOpen = ref(false)
const pageSizeOptions = ref<number[]>([fallbackTablePageSize])
const loading = ref(false)
let accountAutoRefreshEnabled = false
let accountRequestID = 0
let accountSearchTimer: number | undefined
const groupMenuOpen = ref(false)
const groupMenuX = ref(0)
const groupMenuY = ref(0)
const contextGroup = ref<OutlookGroup | null>(null)
const showGroupModal = ref(false)
const groupModalMode = ref<'create' | 'createChild' | 'edit'>('create')
const groupName = ref('')
const groupSortOrder = ref(1)
const groupSaving = ref(false)
const accountModalOpen = ref(false)
const batchModalOpen = ref(false)
const importModalOpen = ref(false)
const exportModalOpen = ref(false)
const moreActionsOpen = ref(false)
const importFileName = ref('')
const importFile = ref<File | null>(null)
const importPassword = ref('')
const exportPassword = ref('')
const outlookTableAreaRef = ref<HTMLElement | null>(null)
const outlookGroupListRef = ref<HTMLElement | null>(null)
const exportingOutlookData = ref(false)
const importFileInputRef = ref<HTMLInputElement | null>(null)
const importingOutlookData = ref(false)
const readModalOpen = ref(false)
const readTarget = ref<OutlookAccount | null>(null)
type OutlookReadFolder = 'inbox' | 'junkemail'
const readFolder = ref<OutlookReadFolder>('inbox')
const readSearchQuery = ref('')
const readLoading = ref(false)
const readDetailLoading = ref(false)
const readLimit = ref(5)
const readPageSize = ref(fallbackTablePageSize)
const readPage = ref(1)
const readPageJump = ref('')
const readPageSizeDropdownOpen = ref(false)
const readPageSizeOptions = ref<number[]>([fallbackTablePageSize])
const readMessages = reactive<{ inbox: OutlookMessage[]; junkemail: OutlookMessage[] }>({
  inbox: [],
  junkemail: [],
})
const readCache = reactive<Record<number, { inbox: OutlookMessage[]; junkemail: OutlookMessage[] }>>({})
const readDetailCache = reactive<Record<number, Record<string, OutlookMessage>>>({})
const readDetailPending: Record<number, Record<string, Promise<OutlookMessage | null>>> = {}
const readCacheStoragePrefix = 'outlook_read_cache_v1:'
const selectedMessage = ref<OutlookMessage | null>(null)
const editingID = ref<number | null>(null)
const activeAccountMenuID = ref<number | null>(null)
const accountMenuX = ref(0)
const accountMenuY = ref(0)
const remarkModalOpen = ref(false)
const remarkSaving = ref(false)
const remarkTarget = ref<OutlookAccount | null>(null)
const remarkText = ref('')
const authBusy = ref(false)
const manualAuthURL = ref('')
let lastGroupClickAt = 0
let lastGroupClickID = 0
let refreshTokenExchangeTimer: number | undefined
let currentExchangeCode = ''
let oauthResultPollTimer: number | undefined
let oauthResultFilled = false
let readDetailRequestID = 0
let readWarmupRunID = 0
const exchangedOutlookCodes = new Set<string>()

const accountForm = reactive({
  email: '',
  password: '',
  client_id: '',
  refresh_token: '',
  group_id: 2,
  remark: '',
})

const batchForm = reactive({
  content: '',
  group_id: 2,
})

function groupSortOrderValue(group: OutlookGroup) {
  const value = Number(group.sort_order)
  return Number.isFinite(value) && value > 0 ? value : group.id
}
function sameParentCustomGroups(parentID: number) {
  return groups.value
    .filter((group) => group.parent_id === parentID && !group.system)
    .sort((a, b) => groupSortOrderValue(a) - groupSortOrderValue(b) || a.id - b.id)
}
const groupSortOrderMax = computed(() => {
  if (groupModalMode.value !== 'edit' || !contextGroup.value) return 1
  return Math.max(1, sameParentCustomGroups(contextGroup.value.parent_id).length)
})
function normalizeGroupSortInput(value: number, max: number) {
  const numeric = Number(value)
  if (!Number.isFinite(numeric)) return 1
  return Math.min(Math.max(Math.trunc(numeric), 1), Math.max(1, max))
}

const groupChildrenMap = computed(() => {
  const map = new Map<number, OutlookGroup[]>()
  for (const group of groups.value) {
    const list = map.get(group.parent_id) || []
    list.push(group)
    map.set(group.parent_id, list)
  }
  for (const list of map.values()) {
    list.sort((a, b) => Number(b.system) - Number(a.system) || groupSortOrderValue(a) - groupSortOrderValue(b) || a.id - b.id)
  }
  return map
})

function outlookGroupIDSet(groupID: number) {
  const ids = new Set<number>()
  const visit = (id: number) => {
    if (ids.has(id)) return
    ids.add(id)
    for (const child of groupChildrenMap.value.get(id) || []) {
      visit(child.id)
    }
  }
  visit(groupID)
  return ids
}

function outlookAccountMatchesGroupFilter(accountGroupID: number, groupID?: number) {
  if (!groupID || groupID === 1) return true
  return outlookGroupIDSet(groupID).has(accountGroupID)
}

const visibleGroups = computed(() => {
  const result: Array<OutlookGroup & { level: number; hasChildren: boolean }> = []
  const visit = (parentID: number, level: number) => {
    for (const group of groupChildrenMap.value.get(parentID) || []) {
      const hasChildren = (groupChildrenMap.value.get(group.id) || []).length > 0
      result.push({ ...group, level, hasChildren })
      if (hasChildren && expandedGroupIDs.value.includes(group.id)) {
        visit(group.id, level + 1)
      }
    }
  }
  visit(0, 0)
  return result
})

const currentGroup = computed(() => groups.value.find((item) => item.id === activeGroupID.value) || groups.value[0])
const allOutlookGroupCount = computed(() => groups.value.reduce((total, group) => {
  if (group.id === 1) return total
  return total + (Number(group.count) || 0)
}, 0))
function outlookGroupCount(group: OutlookGroup) {
  if (group.id === 1) return allOutlookGroupCount.value
  return Number(group.count) || 0
}
const filteredAccounts = computed(() => {
  return accounts.value
})
const selectedAccounts = computed(() => filteredAccounts.value.filter((item) => selectedIDs.value.includes(item.id)))
const allVisibleSelected = computed(() => filteredAccounts.value.length > 0 && filteredAccounts.value.every((item) => selectedIDs.value.includes(item.id)))
const activeAccountMenuItem = computed(() => accounts.value.find((item) => item.id === activeAccountMenuID.value) || null)
type OutlookSortKey = 'group' | 'email' | 'client' | 'created_at' | 'status' | 'remark'
function readPersistedOutlookSort() {
  try {
    const value = JSON.parse(localStorage.getItem(outlookSortStorageKey) || '{}')
    const key = ['group', 'email', 'client', 'created_at', 'status', 'remark'].includes(value.key) ? value.key as OutlookSortKey : 'created_at'
    const order = value.order === 'desc' ? 'desc' : 'asc'
    return { key, order }
  } catch {
    return { key: 'created_at' as OutlookSortKey, order: 'asc' as const }
  }
}
const persistedOutlookSort = readPersistedOutlookSort()
const outlookSortKey = ref<OutlookSortKey>(persistedOutlookSort.key)
const outlookSortOrder = ref<'asc' | 'desc'>(persistedOutlookSort.order)
const sortedAccounts = computed(() => {
  return filteredAccounts.value
})
const virtualOutlookStartIndex = computed(() => Math.max(0, Math.floor(outlookVirtualScrollTop.value / outlookVirtualRowHeight) - outlookVirtualOverscan))
const virtualOutlookVisibleCount = computed(() => Math.ceil(outlookVirtualViewportHeight.value / outlookVirtualRowHeight) + outlookVirtualOverscan * 2)
const virtualOutlookEndIndex = computed(() => Math.min(sortedAccounts.value.length, virtualOutlookStartIndex.value + virtualOutlookVisibleCount.value))
const virtualOutlookAccounts = computed(() => sortedAccounts.value.slice(virtualOutlookStartIndex.value, virtualOutlookEndIndex.value))
const virtualOutlookTopPadding = computed(() => virtualOutlookStartIndex.value * outlookVirtualRowHeight)
const virtualOutlookBottomPadding = computed(() => Math.max(0, (sortedAccounts.value.length - virtualOutlookEndIndex.value) * outlookVirtualRowHeight))
const displayedOutlookAccounts = computed(() => virtualOutlookAccounts.value)
const displayedOutlookTopPadding = computed(() => virtualOutlookTopPadding.value)
const displayedOutlookBottomPadding = computed(() => virtualOutlookBottomPadding.value)
const pageStart = computed(() => (accountTotal.value === 0 ? 0 : (accountPage.value - 1) * pageSize.value + 1))
const pageEnd = computed(() => Math.min(accountPage.value * pageSize.value, accountTotal.value))
const accountPaginationItems = computed(() => buildPaginationItems(accountPage.value, accountPages.value))
const activeReadMessages = computed(() => readMessages[readFolder.value])
const filteredReadMessages = computed(() => {
  const keyword = readSearchQuery.value.trim().toLowerCase()
  if (!keyword) return activeReadMessages.value
  return activeReadMessages.value.filter((message) => {
    return [message.subject, message.to, message.from].some((value) => String(value || '').toLowerCase().includes(keyword))
  })
})
const readTotal = computed(() => filteredReadMessages.value.length)
const readTotalPages = computed(() => Math.max(1, Math.ceil(readTotal.value / readPageSize.value)))
const readPaginationItems = computed(() => buildPaginationItems(readPage.value, readTotalPages.value))
const readVisibleMessages = computed(() => {
  const start = (readPage.value - 1) * readPageSize.value
  return filteredReadMessages.value.slice(start, start + readPageSize.value)
})
const readPageStart = computed(() => (readTotal.value === 0 ? 0 : (readPage.value - 1) * readPageSize.value + 1))
const readPageEnd = computed(() => Math.min(readTotal.value, readPage.value * readPageSize.value))
const selectableGroups = computed(() => {
  const result: OutlookGroup[] = []
  const visit = (parentID: number) => {
    for (const group of groupChildrenMap.value.get(parentID) || []) {
      if (group.id > 1 && !groupHasChildren(group.id)) {
        result.push(group)
      }
      if (groupHasChildren(group.id)) {
        visit(group.id)
      }
    }
  }
  visit(0)
  return result
})
function outlookGroupDisplayName(group: OutlookGroup) {
  if (group.parent_id === 0) return group.name
  const names = [group.name]
  let parentID = group.parent_id
  const visited = new Set<number>()
  while (parentID && !visited.has(parentID)) {
    visited.add(parentID)
    const parent = groups.value.find((item) => item.id === parentID)
    if (!parent) break
    names.unshift(parent.name)
    parentID = parent.parent_id
  }
  return names.join('\\')
}

function maskOutlookClientID(value: string) {
  const clientID = String(value || '').trim()
  if (!clientID) return '-'
  if (clientID.length <= 12) return '*'.repeat(clientID.length)
  return `${clientID.slice(0, 6)}******${clientID.slice(-6)}`
}

const defaultGroupID = computed(() => (currentGroup.value && currentGroup.value.id > 1 && !groupHasChildren(currentGroup.value.id) ? currentGroup.value.id : selectableGroups.value[0]?.id || 0))
function isSelectableOutlookGroup(groupID: number) {
  return selectableGroups.value.some((group) => group.id === groupID)
}
const parentGroupName = computed(() => {
  if (!contextGroup.value || contextGroup.value.parent_id === 0) return ''
  return groups.value.find((item) => item.id === contextGroup.value?.parent_id)?.name || ''
})
const groupModalParentName = computed(() => {
  if (groupModalMode.value === 'createChild') return contextGroup.value?.name || ''
  if (groupModalMode.value === 'edit') return parentGroupName.value
  return ''
})
const groupModalTitle = computed(() => {
  if (groupModalMode.value === 'edit') return '编辑分组'
  if (groupModalMode.value === 'createChild') return '添加子分组'
  return '添加分组'
})
const messageFolderName = computed(() => {
  if (selectedMessage.value?.folder === 'junkemail') return '垃圾箱'
  if (selectedMessage.value?.folder === 'deleteditems') return '已删除'
  return '收件箱'
})
watch(filteredAccounts, () => {
  const valid = new Set(filteredAccounts.value.map((item) => item.id))
  selectedIDs.value = selectedIDs.value.filter((id) => valid.has(id))
})

watch([sortedAccounts, pageSize, searchQuery], () => {
  void nextTick(updateOutlookVirtualViewport)
})

watch(searchQuery, () => {
  if (!accountAutoRefreshEnabled) return
  window.clearTimeout(accountSearchTimer)
  accountSearchTimer = window.setTimeout(() => {
    accountPage.value = 1
    loadAccounts()
  }, 300)
})

watch(() => accountForm.refresh_token, (value) => {
  window.clearTimeout(refreshTokenExchangeTimer)
  if (!extractOutlookCodeFromURL(normalizeOutlookURLText(value))) return
  refreshTokenExchangeTimer = window.setTimeout(() => {
    autoExchangeRefreshTokenInput(value)
  }, 50)
})

watch(groupNameScrollMax, (max) => {
  if (groupNameScrollX.value > max) {
    groupNameScrollX.value = max
  }
})

watch(visibleGroups, () => {
  void updateGroupNameScrollMax()
}, { flush: 'post' })

watch([readFolder, readPageSize, readSearchQuery], () => {
  readPage.value = 1
})

watch(readTotalPages, (pages) => {
  if (readPage.value > pages) {
    readPage.value = pages
  }
})

function currentOutlookAccountCacheGroupID() {
  return activeGroupID.value && activeGroupID.value !== 1 ? activeGroupID.value : 0
}

function currentOutlookAccountPageCacheKey() {
  return outlookAccountPageCacheKey({
    group_id: currentOutlookAccountCacheGroupID(),
    search: searchQuery.value.trim(),
    page: accountPage.value,
    page_size: pageSize.value,
    sort_by: outlookSortKey.value,
    sort_order: outlookSortOrder.value,
  })
}

function rememberCurrentOutlookAccountPage() {
  const normal = accounts.value.filter((item) => ['active', 'normal', 'ok', 'success'].includes(String(item.status || '').toLowerCase())).length
  accountPageCache.value = rememberOutlookAccountPage(
    accountPageCache.value,
    {
      items: accounts.value,
      total: accountTotal.value,
      page: accountPage.value,
      page_size: pageSize.value,
      pages: accountPages.value,
      normal,
      error: Math.max(0, accountTotal.value - normal),
    },
    {
      group_id: currentOutlookAccountCacheGroupID(),
      search: searchQuery.value.trim(),
      page: accountPage.value,
      page_size: pageSize.value,
      sort_by: outlookSortKey.value,
      sort_order: outlookSortOrder.value,
    }
  )
}

function applyOutlookAccountPageCacheEntry(entry: OutlookAccountPageCacheEntry) {
  accounts.value = entry.items
  accountTotal.value = Number(entry.total) || 0
  accountPages.value = Number(entry.pages) || 0
  accountPage.value = Number(entry.page) || accountPage.value
  pageSize.value = Number(entry.page_size) || pageSize.value
}

function restoreOutlookManagementCache() {
  try {
    const value = JSON.parse(localStorage.getItem(outlookManagementCacheKey) || 'null')
    if (!value || typeof value !== 'object') return
    if (Array.isArray(value.groups) && value.groups.length > 0) {
      groups.value = value.groups
    }
    accountPageCache.value = normalizeOutlookAccountPageCache(value.accountPages)
    if (value.pagination && typeof value.pagination === 'object') {
      accountPage.value = Number(value.pagination.page) || accountPage.value
      accountTotal.value = Number(value.pagination.total) || 0
      accountPages.value = Number(value.pagination.pages) || 0
      pageSize.value = Number(value.pagination.page_size) || pageSize.value
    }
    if (value.query && typeof value.query === 'object') {
      activeGroupID.value = Number(value.query.group_id) || activeGroupID.value
      searchQuery.value = String(value.query.search || '')
      if (['group', 'email', 'client', 'created_at', 'status', 'remark'].includes(value.query.sort_by)) {
        outlookSortKey.value = value.query.sort_by
      }
      outlookSortOrder.value = value.query.sort_order === 'desc' ? 'desc' : 'asc'
    }
    if (!groups.value.some((item) => item.id === activeGroupID.value)) {
      activeGroupID.value = groups.value[0]?.id || 0
    }
    const currentCachedPage = accountPageCache.value[currentOutlookAccountPageCacheKey()]
    if (currentCachedPage) {
      applyOutlookAccountPageCacheEntry(currentCachedPage)
    } else if (Array.isArray(value.accounts)) {
      accounts.value = value.accounts
    }
    saveOutlookManagementCache()
  } catch {
    // Ignore stale cache.
  }
}

function saveOutlookManagementCache() {
  try {
    rememberCurrentOutlookAccountPage()
    localStorage.setItem(
      outlookManagementCacheKey,
      JSON.stringify({
        groups: groups.value,
        accounts: accounts.value,
        accountPages: accountPageCache.value,
        pagination: {
          page: accountPage.value,
          page_size: pageSize.value,
          total: accountTotal.value,
          pages: accountPages.value,
        },
        query: {
          group_id: activeGroupID.value,
          search: searchQuery.value,
          sort_by: outlookSortKey.value,
          sort_order: outlookSortOrder.value,
        },
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

onMounted(() => {
  restoreOutlookManagementCache()
  accountAutoRefreshEnabled = true
  refreshAll()
  void nextTick(updateOutlookVirtualViewport)
  updateGroupNameScrollMax()
  window.addEventListener('message', handleOAuthMessage)
  window.addEventListener('resize', updateOutlookVirtualViewport)
  window.addEventListener('resize', updateGroupNameScrollMax)
  window.addEventListener(authSessionClearedEvent, clearReadSessionState)
  document.addEventListener('click', closeFloating)
})

onBeforeUnmount(() => {
  window.clearTimeout(accountSearchTimer)
  window.clearInterval(oauthResultPollTimer)
  window.removeEventListener('message', handleOAuthMessage)
  window.removeEventListener('resize', updateOutlookVirtualViewport)
  window.removeEventListener('resize', updateGroupNameScrollMax)
  window.removeEventListener(authSessionClearedEvent, clearReadSessionState)
  document.removeEventListener('click', closeFloating)
})

async function refreshAll() {
  loading.value = true
  try {
    await loadTablePageSettings()
    await Promise.all([loadGroups(), loadAccounts()])
    saveOutlookManagementCache()
  } finally {
    loading.value = false
  }
}

async function loadTablePageSettings() {
  try {
    const settings = await getAdminSettings()
    const defaultPageSize = Number(settings.table_default_page_size || fallbackTablePageSize)
    const nextDefaultPageSize = Number.isFinite(defaultPageSize) && defaultPageSize > 0 ? defaultPageSize : fallbackTablePageSize
    const nextPageSizeOptions = normalizePageSizeOptions(settings.table_page_size_options, nextDefaultPageSize)
    const persistedPageSize = readPersistedPageSize()

    pageSizeOptions.value = nextPageSizeOptions
    readPageSizeOptions.value = nextPageSizeOptions
    readPageSize.value = nextDefaultPageSize
    if (persistedPageSize > 0 && nextPageSizeOptions.includes(persistedPageSize)) {
      pageSize.value = persistedPageSize
    } else {
      pageSize.value = nextDefaultPageSize
    }
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取表格分页设置失败')
    const fallbackOptions = normalizePageSizeOptions(fallbackTablePageSizeOptions, fallbackTablePageSize)
    pageSizeOptions.value = fallbackOptions
    readPageSizeOptions.value = fallbackOptions
    readPageSize.value = fallbackTablePageSize
    if (!pageSize.value) {
      pageSize.value = fallbackTablePageSize
    }
  }
}

async function loadGroups() {
  try {
    groups.value = await listOutlookGroups()
    if (!groups.value.some((item) => item.id === activeGroupID.value)) {
      activeGroupID.value = groups.value[0]?.id || 0
    }
    saveOutlookManagementCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取微软邮箱分组失败')
  }
}

async function loadAccounts() {
  const requestID = ++accountRequestID
  try {
    const queryKey = [
      'outlook-accounts',
      currentGroup.value?.id && currentGroup.value.id !== 1 ? currentGroup.value.id : 0,
      searchQuery.value.trim(),
      accountPage.value,
      pageSize.value,
      outlookSortKey.value,
      outlookSortOrder.value,
    ]
    const params = {
      group_id: currentGroup.value?.id && currentGroup.value.id !== 1 ? currentGroup.value.id : undefined,
      search: searchQuery.value.trim(),
      page: accountPage.value,
      page_size: pageSize.value,
      sort_by: outlookSortKey.value,
      sort_order: outlookSortOrder.value,
    }
    const cached = queryClient.getQueryData<OutlookAccountListResponse>(queryKey)
    if (cached) {
      accounts.value = cached.items
      accountTotal.value = cached.total
      accountPages.value = cached.pages
      accountPage.value = cached.page
      saveOutlookManagementCache()
    }
    const response = await queryClient.fetchQuery({
      queryKey,
      queryFn: () => listOutlookAccounts(params),
      staleTime: 0,
    })
    if (requestID !== accountRequestID) return
    if (response.items.length === 0 && response.total > 0 && response.pages > 0 && accountPage.value > response.pages) {
      accountPage.value = response.pages
      loadAccounts()
      return
    }
    accounts.value = response.items
    accountTotal.value = response.total
    accountPages.value = response.pages
    accountPage.value = response.page
    if (outlookTableAreaRef.value) {
      outlookTableAreaRef.value.scrollTop = 0
    }
    outlookVirtualScrollTop.value = 0
    void nextTick(updateOutlookVirtualViewport)
    saveOutlookManagementCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取微软邮箱账号失败')
  }
}

function outlookSortValue(item: OutlookAccount) {
  switch (outlookSortKey.value) {
    case 'group':
      return item.group_name || ''
    case 'client':
      return item.client_id || ''
    case 'created_at':
      return item.created_at || ''
    case 'status':
      return statusLabel(item.status)
    case 'remark':
      return item.remark || ''
    case 'email':
    default:
      return item.email || ''
  }
}

function compareOutlookAccountRows(a: OutlookAccount, b: OutlookAccount) {
  return outlookSortValue(a).localeCompare(outlookSortValue(b), 'zh-Hans-CN', {
    numeric: true,
    sensitivity: 'base',
  }) * (outlookSortOrder.value === 'asc' ? 1 : -1)
}

function outlookAccountMatchesCurrentQuery(item: OutlookAccount) {
  const groupID = currentGroup.value?.id && currentGroup.value.id !== 1 ? currentGroup.value.id : undefined
  const keyword = searchQuery.value.trim().toLowerCase()
  const matchesGroup = outlookAccountMatchesGroupFilter(item.group_id, groupID)
  const matchesSearch = !keyword || item.email.toLowerCase().includes(keyword) || item.remark.toLowerCase().includes(keyword)
  return matchesGroup && matchesSearch
}

function recalculateOutlookAccountPages() {
  accountPages.value = accountTotal.value > 0 ? Math.ceil(accountTotal.value / pageSize.value) : 0
}

function applyOutlookAccountSnapshot(item: OutlookAccount, mode: 'create' | 'update') {
  const index = accounts.value.findIndex((account) => account.id === item.id)
  const matches = outlookAccountMatchesCurrentQuery(item)

  if (index >= 0) {
    if (matches) {
      const next = [...accounts.value]
      next[index] = item
      accounts.value = next.sort(compareOutlookAccountRows)
    } else {
      accounts.value = accounts.value.filter((account) => account.id !== item.id)
      accountTotal.value = Math.max(0, accountTotal.value - 1)
      selectedIDs.value = selectedIDs.value.filter((id) => id !== item.id)
      recalculateOutlookAccountPages()
    }
    saveOutlookManagementCache()
    return
  }

  if (!matches) return

  accountTotal.value += 1
  recalculateOutlookAccountPages()
  if (mode === 'create' && accounts.value.length < pageSize.value) {
    accounts.value = [...accounts.value, item].sort(compareOutlookAccountRows)
  }
  saveOutlookManagementCache()
}

function applyOutlookAccountDelete(id: number) {
  const existedOnPage = accounts.value.some((account) => account.id === id)
  accounts.value = accounts.value.filter((account) => account.id !== id)
  selectedIDs.value = selectedIDs.value.filter((selectedID) => selectedID !== id)
  if (existedOnPage) {
    accountTotal.value = Math.max(0, accountTotal.value - 1)
    recalculateOutlookAccountPages()
  }
  saveOutlookManagementCache()
}

function syncOutlookAccountsQuietly(refreshGroups = true) {
  void loadAccounts()
  if (refreshGroups) {
    void loadGroups()
  }
}

function currentOutlookAccountFilter(): AccountListFilter {
  return {
    group_id: currentGroup.value?.id && currentGroup.value.id !== 1 ? currentGroup.value.id : undefined,
    search: searchQuery.value.trim() || undefined,
  }
}

function waitForOutlookTask(task: BackgroundTask, onDone?: () => void, labels = { success: '成功', failed: '失败' }) {
  const showTaskResult = (type: 'success' | 'error' | 'warning', success: number, failed: number) => {
    const segments = [{ text: '任务完成：' }]
    if (success > 0) {
      segments.push({ text: `${labels.success} ${success} 个`, tone: labels.success === '正常' ? 'success' : 'normal' })
    }
    if (success > 0 && failed > 0) {
      segments.push({ text: '，' })
    }
    if (failed > 0) {
      segments.push({ text: `${labels.failed} ${failed} 个`, tone: labels.failed === '错误' ? 'error' : 'normal' })
    }
    appStore.showTaskResult(type, segments)
  }
  taskStore.trackTask(task, {
    onSettled(latest) {
      if (latest.status === 'success') {
        showTaskResult('success', latest.success, 0)
      } else if (latest.status === 'partial') {
        showTaskResult('warning', latest.success, latest.failed)
      } else if (labels.failed === '错误' && latest.failed > 0) {
        showTaskResult('error', 0, latest.failed)
      } else {
        appStore.showError(`任务失败：${latest.message || latest.failed + ' 个' + labels.failed}`)
      }
      onDone?.()
    },
  })
}

function waitForOutlookDataTask(task: BackgroundTask, onSuccess: (latest: BackgroundTask) => Promise<void> | void, failedMessage: string) {
  taskStore.trackTask(task, {
    async onSettled(latest) {
      if (latest.status === 'success') {
        await onSuccess(latest)
        return
      }
      appStore.showError(`任务失败：${latest.message || failedMessage}`)
    },
  })
}

function groupHasChildren(groupID: number) {
  return (groupChildrenMap.value.get(groupID) || []).length > 0
}

function selectGroup(group: OutlookGroup & { hasChildren?: boolean }) {
  activeGroupID.value = group.id
  accountPage.value = 1
  loadAccounts()
  const now = Date.now()
  if (lastGroupClickID === group.id && now - lastGroupClickAt < 220) {
    lastGroupClickAt = now
    return
  }
  lastGroupClickAt = now
  lastGroupClickID = group.id
  if (group.hasChildren) toggleExpanded(group.id)
}

function toggleExpanded(groupID: number) {
  if (expandedGroupIDs.value.includes(groupID)) {
    expandedGroupIDs.value = expandedGroupIDs.value.filter((id) => id !== groupID)
  } else {
    expandedGroupIDs.value = [...expandedGroupIDs.value, groupID]
  }
  localStorage.setItem(groupExpandedStorageKey, JSON.stringify(expandedGroupIDs.value))
}

function openGroupContextMenu(event: MouseEvent, group?: OutlookGroup) {
  event.preventDefault()
  event.stopPropagation()
  const target = group || currentGroup.value
  if (!target) return
  activeGroupID.value = target.id
  accountPage.value = 1
  loadAccounts()
  contextGroup.value = target
  groupMenuX.value = Math.min(event.clientX + 8, window.innerWidth - 190)
  groupMenuY.value = Math.min(event.clientY + 8, window.innerHeight - 190)
  groupMenuOpen.value = true
}

function canManageGroup(group: OutlookGroup | null) {
  return Boolean(group && !group.system)
}

function canAddChildGroup(group: OutlookGroup | null) {
  return Boolean(canManageGroup(group) && group?.parent_id === 0 && Number(group.count) === 0)
}

function syncGroupNameScroll(event: Event) {
  groupNameScrollX.value = (event.currentTarget as HTMLElement).scrollLeft
}

async function updateGroupNameScrollMax() {
  await nextTick()
  const list = outlookGroupListRef.value
  if (!list) {
    groupNameScrollMax.value = 0
    groupNameScrollX.value = 0
    return
  }

  const max = Array.from(list.querySelectorAll<HTMLElement>('.outlook-group-name-viewport')).reduce((currentMax, viewport) => {
    const inner = viewport.firstElementChild as HTMLElement | null
    if (!inner) return currentMax
    return Math.max(currentMax, Math.ceil(inner.scrollWidth - viewport.clientWidth))
  }, 0)

  groupNameScrollMax.value = Math.max(0, max)
  if (groupNameScrollX.value > groupNameScrollMax.value) {
    groupNameScrollX.value = groupNameScrollMax.value
  }
}

function selectPageSize(size: number) {
  pageSize.value = size
  pageSizeDropdownOpen.value = false
  localStorage.setItem(pageSizeStorageKey, String(size))
  accountPage.value = 1
  loadAccounts()
}

function changeAccountPage(page: number) {
  const maxPage = Math.max(accountPages.value, 1)
  const nextPage = Math.max(1, Math.min(page, maxPage))
  if (nextPage === accountPage.value) return
  accountPage.value = nextPage
  loadAccounts()
}

function jumpToAccountPage() {
  const page = Number(accountPageJump.value)
  if (!Number.isFinite(page)) return
  changeAccountPage(page)
  accountPageJump.value = ''
}

function updateOutlookVirtualViewport() {
  const area = outlookTableAreaRef.value
  if (!area) return
  outlookVirtualScrollTop.value = area.scrollTop
  outlookVirtualViewportHeight.value = area.clientHeight || outlookVirtualViewportHeight.value
}

function selectReadPageSize(size: number) {
  readPageSize.value = size
  readPage.value = 1
  readPageSizeDropdownOpen.value = false
}

function changeReadPage(direction: -1 | 1) {
  readPage.value = Math.min(readTotalPages.value, Math.max(1, readPage.value + direction))
}

function setReadPage(page: number) {
  readPage.value = Math.min(readTotalPages.value, Math.max(1, page))
}

function jumpToReadPage() {
  const page = Number(readPageJump.value)
  if (!Number.isFinite(page)) return
  setReadPage(page)
  readPageJump.value = ''
}

function setReadFolder(folder: OutlookReadFolder) {
  readFolder.value = folder
  readPage.value = 1
  selectedMessage.value = null
  if (readTarget.value) saveReadCache(readTarget.value.id)
}

function openGroupModal(mode: 'create' | 'createChild' | 'edit') {
  groupModalMode.value = mode
  groupName.value = mode === 'edit' ? contextGroup.value?.name || '' : ''
  groupSortOrder.value = mode === 'edit' && contextGroup.value ? normalizeGroupSortInput(groupSortOrderValue(contextGroup.value), sameParentCustomGroups(contextGroup.value.parent_id).length) : 1
  groupMenuOpen.value = false
  showGroupModal.value = true
}

async function saveGroup() {
  if (!groupName.value.trim()) {
    appStore.showError('请输入分组名称')
    return
  }
  groupSaving.value = true
  try {
    if (groupModalMode.value === 'edit' && contextGroup.value) {
      const sortOrder = normalizeGroupSortInput(groupSortOrder.value, groupSortOrderMax.value)
      groupSortOrder.value = sortOrder
      await updateOutlookGroup(contextGroup.value.id, { name: groupName.value.trim(), sort_order: sortOrder })
    } else {
      await createOutlookGroup({ name: groupName.value.trim(), parent_id: groupModalMode.value === 'createChild' ? contextGroup.value?.id || 0 : 0 })
    }
    showGroupModal.value = false
    await loadGroups()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存分组失败')
  } finally {
    groupSaving.value = false
  }
}

async function openDeleteGroupDialog() {
  if (!contextGroup.value) return
  const group = contextGroup.value
  groupMenuOpen.value = false
  const confirmed = await appStore.showConfirm({
    title: '删除分组',
    message: `确定删除分组 ${group.name} 吗？`,
    description: '分组下有子分组或账号时无法删除。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteOutlookGroup(group.id)
    appStore.showSuccess('分组已删除')
    await loadGroups()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除分组失败')
  }
}

function resetAccountForm() {
  editingID.value = null
  accountForm.email = ''
  accountForm.password = ''
  accountForm.client_id = ''
  accountForm.refresh_token = ''
  oauthResultFilled = false
  accountForm.group_id = defaultGroupID.value
  accountForm.remark = ''
  manualAuthURL.value = ''
}

function openAddModal() {
  resetAccountForm()
  accountModalOpen.value = true
}

function openEditModal(item: OutlookAccount) {
  editingID.value = item.id
  accountForm.email = item.email
  accountForm.password = ''
  accountForm.client_id = item.client_id || ''
  accountForm.refresh_token = ''
  accountForm.group_id = item.group_id
  accountForm.remark = item.remark
  manualAuthURL.value = ''
  accountModalOpen.value = true
}

async function saveAccount() {
  if (!accountForm.email.trim()) {
    appStore.showError('请输入邮箱')
    return
  }
  if (!accountForm.group_id || !isSelectableOutlookGroup(accountForm.group_id)) {
    appStore.showError('请选择可添加邮箱的分组')
    return
  }
  if (!editingID.value && !accountForm.refresh_token.trim()) {
    appStore.showError('请先授权或填写 Refresh Token')
    return
  }
  try {
    await normalizeRefreshTokenInput()
    const payload = {
      email: accountForm.email.trim(),
      password: accountForm.password,
      client_id: accountForm.client_id.trim(),
      refresh_token: accountForm.refresh_token.trim(),
      group_id: accountForm.group_id,
      remark: accountForm.remark.trim(),
    }
    if (editingID.value) {
      const saved = await updateOutlookAccount(editingID.value, payload)
      applyOutlookAccountSnapshot(saved, 'update')
    } else {
      const saved = await createOutlookAccount(payload)
      applyOutlookAccountSnapshot(saved, 'create')
    }
    accountModalOpen.value = false
    syncOutlookAccountsQuietly()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存微软邮箱失败')
  }
}

function extractOutlookCodeFromURL(value: string) {
  const raw = value.trim()
  if (!raw) return ''
  try {
    const parsed = new URL(raw)
    return parsed.searchParams.get('code') || ''
  } catch {
    return ''
  }
}

function normalizeOutlookURLText(value: string) {
  return value.trim().replace(/\s+/g, '').replace(/%24/g, '$')
}

function parseOutlookCode(value: string) {
  const raw = normalizeOutlookURLText(value)
  return extractOutlookCodeFromURL(raw) || (raw.startsWith('M.') || raw.includes('M.') ? raw : '')
}

async function normalizeRefreshTokenInput(value = accountForm.refresh_token) {
  if (oauthResultFilled && value === accountForm.refresh_token) {
    return
  }
  const normalizedValue = normalizeOutlookURLText(value)
  const currentValue = accountForm.refresh_token.trim()
  const code = parseOutlookCode(value)
  if (!code || currentValue.startsWith('M.R') || currentValue.startsWith('0.')) return
  if (exchangedOutlookCodes.has(code)) {
    appStore.showWarning('这个授权链接已经换过 token 了；微软授权 code 只能使用一次，如需重新授权请重新打开手动授权页。')
    return
  }

  accountForm.refresh_token = normalizedValue
  authBusy.value = true
  try {
    const result = await exchangeOutlookCode({
      code,
      client_id: accountForm.client_id.trim(),
      redirect_uri: defaultOutlookManualRedirectURI,
    })
    accountForm.client_id = result.client_id
    accountForm.refresh_token = result.refresh_token
    exchangedOutlookCodes.add(code)
    appStore.showSuccess('已自动将授权 code 换成 Refresh Token')
  } finally {
    authBusy.value = false
  }
}

async function autoExchangeRefreshTokenInput(value = accountForm.refresh_token) {
  const normalized = normalizeOutlookURLText(value)
  const code = extractOutlookCodeFromURL(normalized)
  if (!code || code === currentExchangeCode || authBusy.value) return

  currentExchangeCode = code
  try {
    await normalizeRefreshTokenInput(normalized)
  } catch (error) {
    const message = error instanceof Error ? error.message : '自动换取 token 失败'
    if (message.includes('expired') || message.includes('not valid') || message.includes('invalid')) {
      appStore.showError('授权 code 已失效或已使用，请重新点击“手动授权”获取新的链接。')
    } else {
      appStore.showError(message)
    }
  } finally {
    currentExchangeCode = ''
  }
}

function handleRefreshTokenPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData('text') || ''
  const normalized = normalizeOutlookURLText(text)
  if (!extractOutlookCodeFromURL(normalized)) return
  event.preventDefault()
  accountForm.refresh_token = normalized
  window.clearTimeout(refreshTokenExchangeTimer)
  refreshTokenExchangeTimer = window.setTimeout(() => {
    autoExchangeRefreshTokenInput(normalized)
  }, 0)
}

async function saveBatch() {
  if (!batchForm.content.trim()) {
    appStore.showError('请输入批量账号内容')
    return
  }
  if (!batchForm.group_id || !isSelectableOutlookGroup(batchForm.group_id)) {
    appStore.showError('请选择可添加邮箱的分组')
    return
  }
  try {
    await batchCreateOutlookAccounts({ content: batchForm.content, group_id: batchForm.group_id })
    batchModalOpen.value = false
    batchForm.content = ''
    await Promise.all([loadGroups(), loadAccounts()])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '批量导入失败')
  }
}

function openBatchModal() {
  batchForm.group_id = defaultGroupID.value
  batchModalOpen.value = true
}

function openImportDataModal() {
  moreActionsOpen.value = false
  importFileName.value = ''
  importFile.value = null
  importPassword.value = ''
  if (importFileInputRef.value) {
    importFileInputRef.value.value = ''
  }
  importModalOpen.value = true
}

function openExportDataModal() {
  moreActionsOpen.value = false
  exportPassword.value = ''
  exportModalOpen.value = true
}

function chooseImportFile() {
  importFileInputRef.value?.click()
}

function handleImportFileChange(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  importFileName.value = file.name
  importFile.value = file
}

async function exportOutlookDataFile() {
  if (!exportPassword.value.trim()) {
    appStore.showError('请输入导出密码')
    return
  }
  const ids = [...selectedIDs.value]
  const filter = ids.length > 0 ? undefined : currentOutlookAccountFilter()
  exportingOutlookData.value = true
  try {
    const task = await createOutlookDataExportTask(ids, exportPassword.value.trim(), filter)
    exportModalOpen.value = false
    appStore.showSuccess('导出任务已创建，完成后可在任务中心下载')
    waitForOutlookDataTask(task, async (latest) => {
      appStore.showSuccess(latest.message || '导出完成，请在右上角任务中心下载 ZIP')
    }, '导出微软邮箱数据失败')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '导出微软邮箱数据失败')
  } finally {
    exportingOutlookData.value = false
  }
}

async function startImportOutlookData() {
  if (!importFile.value) {
    appStore.showError('请选择要导入的 ZIP 文件')
    return
  }
  if (!importPassword.value.trim()) {
    appStore.showError('请输入导入密码')
    return
  }
  importingOutlookData.value = true
  try {
    const task = await createOutlookDataImportTask(importFile.value, importPassword.value.trim())
    importModalOpen.value = false
    appStore.showSuccess('导入任务已创建，可继续使用页面')
    waitForOutlookDataTask(task, async (latest) => {
      await refreshAll()
      appStore.showSuccess(latest.message || '导入微软邮箱数据完成')
    }, '导入微软邮箱数据失败')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '导入微软邮箱数据失败')
  } finally {
    importingOutlookData.value = false
  }
}

async function removeAccount(item: OutlookAccount) {
  const confirmed = await appStore.showConfirm({
    title: '删除微软邮箱',
    message: `确定删除 ${item.email} 吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteOutlookAccount(item.id)
    applyOutlookAccountDelete(item.id)
    syncOutlookAccountsQuietly()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除失败')
  }
}

async function removeSelected() {
  if (selectedIDs.value.length === 0) return
  const confirmed = await appStore.showConfirm({
    title: '批量删除微软邮箱',
    message: `确定删除选中的 ${selectedIDs.value.length} 个微软邮箱吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    const removingIDs = [...selectedIDs.value]
    await batchOutlookAction({ action: 'delete', ids: selectedIDs.value })
    removingIDs.forEach((id) => applyOutlookAccountDelete(id))
    selectedIDs.value = []
    syncOutlookAccountsQuietly()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '批量删除失败')
  }
}

async function runTest(item: OutlookAccount) {
  try {
    const result = await testOutlookAccount(item.id)
    appStore.showSuccess(result.message || 'Graph API 连接正常')
    await loadAccounts()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : 'Graph API 连接失败')
    await loadAccounts()
  }
}

async function testSelected() {
  if (selectedAccounts.value.length === 0) return
  for (const item of selectedAccounts.value) {
    try {
      await testOutlookAccount(item.id)
    } catch {
      // Keep testing the rest and refresh statuses at the end.
    }
  }
  appStore.showSuccess(`已测试 ${selectedAccounts.value.length} 个微软邮箱`)
  await loadAccounts()
}

async function removeSelectedV2() {
  const ids = [...selectedIDs.value]
  if (ids.length === 0 && accountTotal.value === 0) return
  const scopeText = ids.length > 0 ? `选中的 ${ids.length} 个微软邮箱` : `当前筛选的 ${accountTotal.value} 个微软邮箱`
  const confirmed = await appStore.showConfirm({
    title: '批量删除微软邮箱',
    message: `确定删除${scopeText}吗？`,
    description: '删除任务开始后无法撤销。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  const result = await batchOutlookAction({ action: 'delete', ids, filter: ids.length > 0 ? undefined : currentOutlookAccountFilter() })
  ids.forEach((id) => applyOutlookAccountDelete(id))
  selectedIDs.value = []
  appStore.showInfo('批量删除已开始')
  if (result && typeof result === 'object' && 'id' in result) {
    waitForOutlookTask(result, () => syncOutlookAccountsQuietly())
  } else {
    syncOutlookAccountsQuietly()
  }
}

async function testSelectedV2() {
  const ids = [...selectedIDs.value]
  if (ids.length === 0 && accountTotal.value === 0) return
  const result = await batchOutlookAction({ action: 'test', ids, filter: ids.length > 0 ? undefined : currentOutlookAccountFilter() })
  appStore.showInfo('批量测试已开始')
  if (result && typeof result === 'object' && 'id' in result) {
    waitForOutlookTask(result, () => syncOutlookAccountsQuietly(false), { success: '正常', failed: '错误' })
  } else {
    syncOutlookAccountsQuietly(false)
  }
}

function toggleAccountMenu(event: MouseEvent, item: OutlookAccount) {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  activeAccountMenuID.value = activeAccountMenuID.value === item.id ? null : item.id
  accountMenuX.value = Math.min(rect.left - 88, window.innerWidth - 150)
  accountMenuY.value = Math.min(rect.bottom + 8, window.innerHeight - 150)
}

function openRemarkModal(item: OutlookAccount) {
  activeAccountMenuID.value = null
  remarkTarget.value = item
  remarkText.value = item.remark || ''
  remarkModalOpen.value = true
}

async function saveRemark() {
  if (!remarkTarget.value) return
  remarkSaving.value = true
  try {
    const saved = await updateOutlookAccount(remarkTarget.value.id, {
      email: remarkTarget.value.email,
      client_id: '',
      group_id: remarkTarget.value.group_id,
      remark: remarkText.value.trim(),
    })
    applyOutlookAccountSnapshot(saved, 'update')
    remarkModalOpen.value = false
    syncOutlookAccountsQuietly(false)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存备注失败')
  } finally {
    remarkSaving.value = false
  }
}

async function copyText(value: string, message = '已复制') {
  try {
    await copyToClipboard(value)
    appStore.showSuccess(message)
  } catch {
    appStore.showError('复制失败')
  }
}

function toggleAll() {
  if (allVisibleSelected.value) {
    const visible = new Set(filteredAccounts.value.map((item) => item.id))
    selectedIDs.value = selectedIDs.value.filter((id) => !visible.has(id))
  } else {
    selectedIDs.value = Array.from(new Set([...selectedIDs.value, ...filteredAccounts.value.map((item) => item.id)]))
  }
}

function toggleOutlookSort(key: OutlookSortKey) {
  if (outlookSortKey.value === key) {
    outlookSortOrder.value = outlookSortOrder.value === 'asc' ? 'desc' : 'asc'
  } else {
    outlookSortKey.value = key
    outlookSortOrder.value = 'asc'
  }
  localStorage.setItem(outlookSortStorageKey, JSON.stringify({ key: outlookSortKey.value, order: outlookSortOrder.value }))
  accountPage.value = 1
  loadAccounts()
}

async function startOAuthPopup() {
  const clientID = defaultOutlookOAuthClientID
  authBusy.value = true
  try {
    const result = await getOutlookAuthorizeURL({ client_id: clientID, login_hint: accountForm.email.trim() })
    accountForm.client_id = result.client_id
    window.open(result.url, 'outlook-oauth', 'width=620,height=760')
    if (result.state) {
      pollOAuthResult(result.state)
    }
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '创建授权链接失败')
  } finally {
    authBusy.value = false
  }
}

function pollOAuthResult(state: string) {
  window.clearInterval(oauthResultPollTimer)
  let attempts = 0
  oauthResultPollTimer = window.setInterval(async () => {
    attempts += 1
    if (attempts > 80) {
      window.clearInterval(oauthResultPollTimer)
      return
    }
    try {
      const result = await getOutlookOAuthResult(state)
      if (result.status !== 'success') return
      window.clearInterval(oauthResultPollTimer)
      accountForm.client_id = result.client_id || accountForm.client_id
      accountForm.refresh_token = result.refresh_token || accountForm.refresh_token
      oauthResultFilled = Boolean(result.refresh_token)
      appStore.showSuccess('授权凭证已填入')
    } catch {
      if (attempts > 6) {
        window.clearInterval(oauthResultPollTimer)
      }
    }
  }, 1500)
}

function openOutlookAuthURL(clientID: string) {
  accountForm.client_id = clientID
  const params = new URLSearchParams({
    client_id: clientID,
    response_type: 'code',
    redirect_uri: defaultOutlookManualRedirectURI,
    scope: 'Mail.ReadWrite offline_access',
    response_mode: 'query',
  })
  if (accountForm.email.trim()) params.set('login_hint', accountForm.email.trim())
  manualAuthURL.value = `https://login.microsoftonline.com/common/oauth2/v2.0/authorize?${params.toString()}`
  window.open(manualAuthURL.value, '_blank')
  appStore.showInfo('授权后复制 https://localhost/?code=...，直接粘到 Refresh Token 框即可自动识别')
}

function buildManualAuthURL() {
  openOutlookAuthURL(defaultOutlookClientID)
}

function buildCustomAuthURL() {
  const clientID = accountForm.client_id.trim()
  if (!clientID) {
    appStore.showWarning('请输入自定义 Client ID')
    return
  }
  openOutlookAuthURL(clientID)
}

function handleOAuthMessage(event: MessageEvent) {
  if (event.origin !== window.location.origin) return
  if (event.data?.type !== 'outlook-oauth-callback') return
  if (!event.data.success) {
    appStore.showError(event.data.error || '授权失败')
    return
  }
  accountForm.client_id = event.data.data?.client_id || accountForm.client_id
  accountForm.refresh_token = event.data.data?.refresh_token || accountForm.refresh_token
  oauthResultFilled = Boolean(event.data.data?.refresh_token)
  appStore.showSuccess('授权凭证已填入')
}

function getReadCacheStorageKey(accountID: number) {
  return `${readCacheStoragePrefix}${accountID}`
}

function readStoredReadCache(accountID: number) {
  try {
    const value = JSON.parse(localStorage.getItem(getReadCacheStorageKey(accountID)) || 'null')
    if (!value || typeof value !== 'object') return null
    const inbox = Array.isArray(value.inbox) ? value.inbox : []
    const junkemail = Array.isArray(value.junkemail) ? value.junkemail : []
    const details = value.details && typeof value.details === 'object' ? value.details as Record<string, OutlookMessage> : {}
    const folder = value.folder === 'junkemail' ? 'junkemail' : 'inbox'
    return { inbox, junkemail, details, folder }
  } catch {
    return null
  }
}

function saveReadCache(accountID: number) {
  try {
    localStorage.setItem(
      getReadCacheStorageKey(accountID),
      JSON.stringify({
        inbox: readCache[accountID]?.inbox || [],
        junkemail: readCache[accountID]?.junkemail || [],
        details: readDetailCache[accountID] || {},
        folder: readFolder.value,
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; the in-memory cache still works for the current view.
  }
}

function clearReadCacheStorage() {
  Object.keys(localStorage).forEach((key) => {
    if (key.startsWith(readCacheStoragePrefix)) {
      localStorage.removeItem(key)
    }
  })
}

function clearReadSessionState() {
  readDetailRequestID += 1
  readWarmupRunID += 1
  readModalOpen.value = false
  readTarget.value = null
  readFolder.value = 'inbox'
  readSearchQuery.value = ''
  readLoading.value = false
  readDetailLoading.value = false
  readPage.value = 1
  readPageJump.value = ''
  selectedMessage.value = null
  readMessages.inbox = []
  readMessages.junkemail = []
  Object.keys(readCache).forEach((key) => {
    delete readCache[Number(key)]
  })
  Object.keys(readDetailCache).forEach((key) => {
    delete readDetailCache[Number(key)]
  })
  Object.keys(readDetailPending).forEach((key) => {
    delete readDetailPending[Number(key)]
  })
  clearReadCacheStorage()
}

function openReadModal(item: OutlookAccount) {
  readDetailRequestID += 1
  readWarmupRunID += 1
  readTarget.value = item
  readSearchQuery.value = ''
  readLimit.value = 5
  readPage.value = 1
  readDetailLoading.value = false
  selectedMessage.value = null
  const stored = readStoredReadCache(item.id)
  if (stored) {
    readCache[item.id] = { inbox: stored.inbox, junkemail: stored.junkemail }
    readDetailCache[item.id] = stored.details
  }
  const cached = readCache[item.id]
  readMessages.inbox = cached?.inbox || []
  readMessages.junkemail = cached?.junkemail || []
  readFolder.value = stored?.folder || 'inbox'
  readModalOpen.value = true
}

function cacheOutlookDetail(accountID: number, fallback: OutlookMessage, detail: OutlookMessage) {
  const detailMap = readDetailCache[accountID] || {}
  readDetailCache[accountID] = detailMap
  const mergedDetail = { ...fallback, ...detail, folder: fallback.folder || detail.folder }
  detailMap[fallback.id] = mergedDetail
  saveReadCache(accountID)
  return mergedDetail
}

function loadOutlookDetail(accountID: number, message: OutlookMessage) {
  const pendingMap = readDetailPending[accountID] || {}
  readDetailPending[accountID] = pendingMap
  if (!pendingMap[message.id]) {
    pendingMap[message.id] = getOutlookMessageDetail(accountID, message.id)
      .then((detail) => cacheOutlookDetail(accountID, message, detail))
      .catch(() => null)
      .finally(() => {
        delete pendingMap[message.id]
      })
  }
  return pendingMap[message.id]
}

async function warmOutlookMessageDetails(accountID: number, messages: OutlookMessage[], runID: number) {
  const detailMap = readDetailCache[accountID] || {}
  readDetailCache[accountID] = detailMap
  const pendingMessages = Array.from(new Map(messages.filter((message) => message.id && !detailMap[message.id]).map((message) => [message.id, message])).values())

  for (const message of pendingMessages) {
    if (runID !== readWarmupRunID) return
    try {
      const detail = await loadOutlookDetail(accountID, message)
      if (runID !== readWarmupRunID) return
      if (!detail?.html) continue
    } catch {
      // A failed warm cache should not block reading; click-through will retry.
    }
  }
}

function startOutlookDetailWarmup(accountID: number, inboxItems: OutlookMessage[], junkItems: OutlookMessage[]) {
  readWarmupRunID += 1
  const runID = readWarmupRunID
  const messages = [...inboxItems, ...junkItems]
  void warmOutlookMessageDetails(accountID, messages, runID)
}

async function fetchMessages() {
  if (!readTarget.value || readLoading.value) return
  readDetailRequestID += 1
  readWarmupRunID += 1
  readLoading.value = true
  readDetailLoading.value = false
  selectedMessage.value = null
  const limit = Math.min(100, Math.max(1, Number(readLimit.value) || 5))
  readLimit.value = limit
  const accountID = readTarget.value.id
  try {
    const [inbox, junk] = await Promise.all([
      listOutlookMessages(accountID, { folder: 'inbox', top: limit, skip: 0 }),
      listOutlookMessages(accountID, { folder: 'junkemail', top: limit, skip: 0 }),
    ])
    const inboxItems = inbox.items || []
    const junkItems = junk.items || []
    readMessages.inbox = inboxItems
    readMessages.junkemail = junkItems
    readCache[accountID] = {
      inbox: readMessages.inbox,
      junkemail: readMessages.junkemail,
    }
    readFolder.value = 'inbox'
    readSearchQuery.value = ''
    readPage.value = 1
    saveReadCache(accountID)
    appStore.showSuccess('收取邮件成功')
    startOutlookDetailWarmup(accountID, inboxItems, junkItems)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '收取邮件失败')
  } finally {
    readLoading.value = false
  }
}

async function openMessage(message: OutlookMessage) {
  if (!readTarget.value) return
  const cachedDetail = readDetailCache[readTarget.value.id]?.[message.id]
  if (cachedDetail?.html) {
    readDetailRequestID += 1
    readDetailLoading.value = false
    selectedMessage.value = { ...cachedDetail, folder: message.folder }
    return
  }

  const requestID = readDetailRequestID + 1
  readDetailRequestID = requestID
  readDetailLoading.value = true
  try {
    const detail = await loadOutlookDetail(readTarget.value.id, message)
    if (requestID !== readDetailRequestID) return
    if (!detail) {
      throw new Error('读取邮件详情失败')
    }
    selectedMessage.value = { ...detail, folder: message.folder }
  } catch (error) {
    if (requestID === readDetailRequestID) {
      appStore.showError(error instanceof Error ? error.message : '读取邮件详情失败')
    }
  } finally {
    if (requestID === readDetailRequestID) {
      readDetailLoading.value = false
    }
  }
}

function closeReadDetail() {
  readDetailRequestID += 1
  readDetailLoading.value = false
  selectedMessage.value = null
}

function statusLabel(status: string) {
  return ['active', 'normal', 'ok', 'success'].includes(status.toLowerCase()) ? '正常' : '错误'
}

function statusClass(status: string) {
  return ['active', 'normal', 'ok', 'success'].includes(status.toLowerCase()) ? 'badge-success' : 'badge-danger'
}

function shouldShowStatusReason(item: OutlookAccount) {
  return statusClass(item.status) === 'badge-danger' && Boolean(item.status_reason?.trim())
}

function closeFloating(event?: MouseEvent) {
  const target = event?.target as HTMLElement | undefined
  if (target?.closest('[data-outlook-floating]')) return
  if (!target?.closest('[data-outlook-more-actions]')) {
    moreActionsOpen.value = false
  }
  if (!target?.closest('[data-outlook-row-menu]')) {
    activeAccountMenuID.value = null
  }
  if (!target?.closest('[data-outlook-page-size-select]')) {
    pageSizeDropdownOpen.value = false
  }
  if (!target?.closest('[data-outlook-read-page-size-select]')) {
    readPageSizeDropdownOpen.value = false
  }
  groupMenuOpen.value = false
}
</script>

<template>
  <div class="outlook-page-layout min-h-[calc(100vh-8rem)] gap-3">
    <aside class="outlook-group-panel shrink-0 rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/50" @contextmenu="openGroupContextMenu">
      <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">邮箱分组</h2>
      </div>
      <div class="outlook-group-list-wrap">
        <div ref="outlookGroupListRef" class="outlook-group-list space-y-1 p-3">
          <button
            v-for="group in visibleGroups"
            :key="group.id"
            class="outlook-group-item flex w-full select-none items-center justify-between rounded-xl px-3 py-2.5 text-left text-sm transition-colors"
            :class="activeGroupID === group.id ? 'bg-primary-50 text-primary-700 dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 hover:bg-gray-50 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-700/70 dark:hover:text-white'"
            :style="{ paddingLeft: `${12 + group.level * 18}px` }"
            type="button"
            @click="selectGroup(group)"
            @dblclick.stop.prevent
            @contextmenu.stop="openGroupContextMenu($event, group)"
          >
            <span class="outlook-group-name-viewport">
              <span class="outlook-group-name-inner" :style="{ transform: `translateX(-${groupNameScrollX}px)` }">
                <span class="flex h-4 w-4 shrink-0 items-center justify-center" @click.stop="group.hasChildren && toggleExpanded(group.id)">
                  <ChevronDown v-if="group.hasChildren && expandedGroupIDs.includes(group.id)" class="h-4 w-4" />
                  <ChevronRight v-else-if="group.hasChildren" class="h-4 w-4" />
                  <Folder v-else class="h-4 w-4" />
                </span>
                <span class="outlook-group-name" :title="group.name">{{ group.name }}</span>
              </span>
            </span>
            <span v-if="!group.hasChildren" class="outlook-group-count rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-900 dark:text-dark-400">{{ outlookGroupCount(group) }}</span>
            <span v-else class="outlook-group-count-placeholder"></span>
          </button>
        </div>
        <div v-if="groupNameScrollMax > 0" class="outlook-group-horizontal-scroll">
          <div class="outlook-group-horizontal-scroll-body" @scroll="syncGroupNameScroll">
            <div :style="{ width: `calc(100% + ${groupNameScrollMax}px)` }"></div>
          </div>
        </div>
      </div>
    </aside>

    <section class="outlook-account-panel min-w-0 flex-1 overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/50">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div class="flex flex-wrap items-center gap-2">
          <button class="outlook-action-primary" type="button" @click="openAddModal"><Plus class="h-4 w-4" />添加微软邮箱</button>
          <button class="outlook-action-secondary" type="button" @click="openBatchModal"><Upload class="h-4 w-4" />批量添加微软邮箱</button>
          <div class="relative" data-outlook-more-actions>
            <button class="outlook-action-more" type="button" @click.stop="moreActionsOpen = !moreActionsOpen">
              <MoreHorizontal class="h-4 w-4" />
              <span>更多操作</span>
              <ChevronDown class="h-4 w-4 transition-transform" :class="{ 'rotate-180': moreActionsOpen }" />
            </button>
            <div v-if="moreActionsOpen" class="outlook-more-menu" @click.stop>
              <div class="outlook-more-menu-label">数据操作</div>
              <button class="outlook-more-menu-item" type="button" @click="openImportDataModal">
                <span class="outlook-more-menu-icon import"><Upload class="h-4 w-4" /></span>
                <span>导入</span>
              </button>
              <button class="outlook-more-menu-item" type="button" @click="openExportDataModal">
                <span class="outlook-more-menu-icon export"><Download class="h-4 w-4" /></span>
                <span>{{ selectedIDs.length > 0 ? '导出选中' : '导出' }}</span>
                <span v-if="selectedIDs.length > 0" class="outlook-more-selected-badge">已选 {{ selectedIDs.length }}</span>
              </button>
            </div>
          </div>
          <button class="outlook-action-refresh" type="button" :disabled="loading" @click="refreshAll"><RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" />刷新</button>
          <button v-if="selectedIDs.length > 0" class="outlook-toolbar-batch-button" type="button" @click="testSelectedV2">
            <Play class="h-4 w-4" />
            批量测试（{{ selectedIDs.length }}）
          </button>
          <button v-if="selectedIDs.length > 0" class="outlook-toolbar-batch-danger" type="button" @click="removeSelectedV2">
            <Trash2 class="h-4 w-4" />
            批量删除（{{ selectedIDs.length }}）
          </button>
        </div>
        <div class="search-clear-field relative max-w-full" style="width: min(350px, 100%); flex: 0 0 min(350px, 100%);">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
          <input v-model.trim="searchQuery" class="input search-clear-input h-9 pl-10 text-sm" type="text" placeholder="搜索微软邮箱或备注" />
          <button v-if="searchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="searchQuery = ''">
            <X class="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <div class="outlook-account-body flex-1">
        <div ref="outlookTableAreaRef" class="outlook-table-area" @scroll="updateOutlookVirtualViewport">
        <table class="outlook-account-table text-sm">
          <colgroup>
            <col class="outlook-col-select" />
            <col class="outlook-col-group" />
            <col class="outlook-col-email" />
            <col class="outlook-col-client" />
            <col class="outlook-col-created" />
            <col class="outlook-col-status" />
            <col class="outlook-col-remark" />
            <col class="outlook-col-actions" />
          </colgroup>
          <thead class="bg-gray-50 text-center text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="outlook-select-col px-5 py-3 font-medium"><input :checked="allVisibleSelected" type="checkbox" @change="toggleAll" /></th>
              <th class="px-5 py-3 font-medium">
                <button class="outlook-sort-button" type="button" @click="toggleOutlookSort('group')"><span class="outlook-sort-label">分组</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': outlookSortKey === 'group' && outlookSortOrder === 'asc' }" /></button>
              </th>
              <th class="px-5 py-3 font-medium">
                <button class="outlook-sort-button" type="button" @click="toggleOutlookSort('email')"><span class="outlook-sort-label">微软邮箱</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': outlookSortKey === 'email' && outlookSortOrder === 'asc' }" /></button>
              </th>
              <th class="px-5 py-3 font-medium">
                <button class="outlook-sort-button" type="button" @click="toggleOutlookSort('client')"><span class="outlook-sort-label">Client ID</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': outlookSortKey === 'client' && outlookSortOrder === 'asc' }" /></button>
              </th>
              <th class="px-5 py-3 font-medium">
                <button class="outlook-sort-button" type="button" @click="toggleOutlookSort('created_at')"><span class="outlook-sort-label">添加时间</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': outlookSortKey === 'created_at' && outlookSortOrder === 'asc' }" /></button>
              </th>
              <th class="px-5 py-3 font-medium">
                <button class="outlook-sort-button" type="button" @click="toggleOutlookSort('status')"><span class="outlook-sort-label">状态</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': outlookSortKey === 'status' && outlookSortOrder === 'asc' }" /></button>
              </th>
              <th class="px-5 py-3 font-medium">
                <button class="outlook-sort-button" type="button" @click="toggleOutlookSort('remark')"><span class="outlook-sort-label">备注</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': outlookSortKey === 'remark' && outlookSortOrder === 'asc' }" /></button>
              </th>
              <th class="sticky-col-right px-5 py-3 font-medium">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-if="displayedOutlookTopPadding > 0" aria-hidden="true">
              <td colspan="8" :style="{ height: `${displayedOutlookTopPadding}px`, padding: 0, border: 0 }"></td>
            </tr>
            <tr v-for="item in displayedOutlookAccounts" :key="item.id" class="outlook-virtual-row hover:bg-gray-50 dark:hover:bg-dark-800">
              <td class="outlook-select-col px-5 py-4"><input v-model="selectedIDs" :value="item.id" type="checkbox" /></td>
              <td class="px-5 py-4 text-gray-600 dark:text-gray-300" :title="item.group_name">
                <span class="outlook-group-table-name">{{ item.group_name }}</span>
              </td>
              <td class="px-5 py-4 font-medium text-gray-900 dark:text-white">
                <div class="outlook-email-cell">
                  <button class="outlook-email-link" type="button" @click="openReadModal(item)">{{ item.email }}</button>
                  <button class="outlook-email-copy-button" type="button" title="复制邮箱" @click.stop="copyText(item.email)"><Copy class="h-3.5 w-3.5" /></button>
                </div>
              </td>
              <td class="px-5 py-4 text-gray-500 dark:text-dark-400">
                <div class="outlook-client-lines" :title="`ID: ${item.client_id || '-'}${item.last_token_refresh_at ? ` / 续期: ${item.last_token_refresh_at}` : ''}`">
                  <div>ID: {{ maskOutlookClientID(item.client_id) }}</div>
                  <div v-if="item.last_token_refresh_at" class="outlook-token-refresh-line">续期: {{ item.last_token_refresh_at }}</div>
                </div>
              </td>
              <td class="px-5 py-4 text-gray-500 dark:text-dark-400" :title="item.created_at"><span class="outlook-created-time">{{ item.created_at }}</span></td>
              <td class="px-5 py-4">
                <div class="outlook-status-cell">
                  <span class="badge" :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
                  <span v-if="shouldShowStatusReason(item)" class="outlook-status-reason" tabindex="0" :aria-label="item.status_reason">
                    <CircleHelp class="h-3.5 w-3.5" />
                    <span class="outlook-status-tooltip">{{ item.status_reason }}</span>
                  </span>
                </div>
              </td>
              <td class="px-5 py-4 text-gray-500 dark:text-dark-400">{{ item.remark }}</td>
              <td class="sticky-col-right px-5 py-4 text-center">
                <div class="outlook-row-actions text-gray-500 dark:text-dark-400">
                  <button class="outlook-row-action-button hover:text-primary-600 dark:hover:text-primary-300" type="button" @click="openEditModal(item)">
                    <Pencil class="h-4 w-4" />
                    <span>编辑</span>
                  </button>
                  <button class="outlook-row-action-button hover:text-emerald-600 dark:hover:text-emerald-300" type="button" @click="runTest(item)">
                    <Play class="h-4 w-4" />
                    <span>测试</span>
                  </button>
                  <button class="outlook-row-action-button hover:text-red-600 dark:hover:text-red-400" type="button" @click="removeAccount(item)">
                    <Trash2 class="h-4 w-4" />
                    <span>删除</span>
                  </button>
                  <div data-outlook-row-menu>
                    <button class="outlook-row-action-button hover:text-gray-900 dark:hover:text-white" type="button" @click.stop="toggleAccountMenu($event, item)">
                      <MoreHorizontal class="h-4 w-4" />
                      <span>更多</span>
                    </button>
                  </div>
                </div>
              </td>
            </tr>
            <tr v-if="displayedOutlookBottomPadding > 0" aria-hidden="true">
              <td colspan="8" :style="{ height: `${displayedOutlookBottomPadding}px`, padding: 0, border: 0 }"></td>
            </tr>
          </tbody>
        </table>
        <div v-if="filteredAccounts.length === 0" class="p-8 text-center text-sm font-semibold text-gray-500 dark:text-dark-400">暂无微软邮箱账号</div>
        </div>
      </div>
      <div class="flex items-center justify-between border-t border-gray-200 bg-gray-50 px-5 py-3 dark:border-dark-700 dark:bg-dark-800">
        <PaginationBar
          :page="accountPage"
          :pages="Math.max(accountPages, 1)"
          :page-size="pageSize"
          :page-size-options="pageSizeOptions"
          :total="accountTotal"
          @page-change="changeAccountPage"
          @page-size-change="selectPageSize"
        />
        <div v-if="false" class="flex items-center gap-3">
          <p class="text-sm text-gray-700 dark:text-gray-300">
            显示 {{ pageStart }} 至 {{ pageEnd }} 共 {{ accountTotal }} 条结果
          </p>
          <div class="flex items-center gap-2">
            <span class="text-sm text-gray-700 dark:text-gray-300">每页:</span>
            <div class="page-size-select relative w-20" data-outlook-page-size-select>
              <button class="page-size-trigger" type="button" @click.stop="pageSizeDropdownOpen = !pageSizeDropdownOpen">
                <span>{{ pageSize }}</span>
                <ChevronDown class="h-4 w-4 transition-transform" :class="{ 'rotate-180': pageSizeDropdownOpen }" />
              </button>
              <div v-if="pageSizeDropdownOpen" class="page-size-menu">
                <button
                  v-for="size in pageSizeOptions"
                  :key="size"
                  class="page-size-option"
                  :class="{ 'page-size-option-active': size === pageSize }"
                  type="button"
                  @click="selectPageSize(size)"
                >
                  <span>{{ size }}</span>
                  <Check v-if="size === pageSize" class="h-4 w-4" />
                </button>
              </div>
            </div>
          </div>
        </div>

        <div v-if="false" class="compact-pagination">
          <button class="pagination-arrow-button relative inline-flex items-center rounded-l-md border border-gray-300 bg-white px-2 py-2 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600" type="button" :disabled="accountPage <= 1" @click="changeAccountPage(accountPage - 1)">
            ‹
          </button>
          <template v-for="item in accountPaginationItems" :key="item.key">
            <span v-if="item.type === 'ellipsis'" class="pagination-ellipsis relative inline-flex items-center border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400">...</span>
            <button
              v-else
              class="pagination-page-button relative inline-flex items-center border px-4 py-2 text-sm font-medium"
              :class="item.page === accountPage ? 'z-10 border-primary-500 bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400' : 'border-gray-300 bg-white text-gray-500 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600'"
              type="button"
              @click="changeAccountPage(item.page)"
            >
              {{ item.page }}
            </button>
          </template>
          <button class="pagination-arrow-button relative inline-flex items-center rounded-r-md border border-gray-300 bg-white px-2 py-2 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600" type="button" :disabled="accountPage >= Math.max(accountPages, 1)" @click="changeAccountPage(accountPage + 1)">
            ›
          </button>
          <form class="page-jump-form" @submit.prevent="jumpToAccountPage">
            <input
              v-model.trim="accountPageJump"
              class="page-jump-input"
              type="text"
              inputmode="numeric"
              pattern="[0-9]*"
              min="1"
              :max="Math.max(accountPages, 1)"
              :placeholder="String(accountPage)"
              aria-label="跳转页码"
            />
            <button class="page-jump-button" type="submit" title="跳转页码">
              <ChevronRight class="h-4 w-4" />
            </button>
          </form>
        </div>
      </div>
    </section>

    <Teleport to="body">
      <div v-if="groupMenuOpen" data-outlook-floating class="outlook-context-menu fixed w-44 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 shadow-xl dark:border-dark-600 dark:bg-dark-800" :style="{ left: `${groupMenuX}px`, top: `${groupMenuY}px` }" @click.stop>
        <button class="outlook-context-item" type="button" @click="openGroupModal('create')"><Plus class="h-4 w-4" />添加分组</button>
        <button class="outlook-context-item" type="button" :disabled="!canAddChildGroup(contextGroup)" @click="openGroupModal('createChild')"><FolderPlus class="h-4 w-4" />添加子分组</button>
        <button class="outlook-context-item" type="button" :disabled="!canManageGroup(contextGroup)" @click="openGroupModal('edit')"><Pencil class="h-4 w-4" />编辑分组</button>
        <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
        <button class="outlook-context-item text-red-600 dark:text-red-400" type="button" :disabled="!canManageGroup(contextGroup)" @click="openDeleteGroupDialog"><Trash2 class="h-4 w-4" />删除分组</button>
      </div>

      <div v-if="showGroupModal" class="outlook-modal-mask">
        <div class="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <h3 class="text-lg font-bold">{{ groupModalTitle }}</h3>
          <label class="mt-5 block">
            <span class="input-label">上级分组</span>
            <input class="input" type="text" :value="groupModalParentName" disabled />
          </label>
          <label v-if="groupModalMode === 'edit'" class="mt-5 block">
            <span class="input-label">序号</span>
            <input v-model.number="groupSortOrder" class="input" type="number" min="1" :max="groupSortOrderMax" step="1" @keyup.enter="saveGroup" />
          </label>
          <label class="mt-5 block">
            <span class="input-label">分组名称</span>
            <input v-model.trim="groupName" class="input" placeholder="请输入分组名称" @keyup.enter="saveGroup" />
          </label>
          <div class="mt-6 flex justify-end gap-2">
            <button class="btn btn-secondary" type="button" @click="showGroupModal = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="groupSaving" @click="saveGroup">{{ groupSaving ? '保存中...' : '保存' }}</button>
          </div>
        </div>
      </div>

      <div v-if="accountModalOpen" class="outlook-modal-mask">
        <div class="outlook-account-modal rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold">{{ editingID ? '编辑微软邮箱' : '添加微软邮箱' }}</h3>
            <button class="modal-close-button" type="button" @click="accountModalOpen = false"><X class="h-5 w-5" /></button>
          </div>
          <div class="outlook-modal-scroll grid gap-4 p-6 md:grid-cols-2">
            <label>
              <span class="input-label">邮箱 *</span>
              <input v-model.trim="accountForm.email" class="input" placeholder="name@outlook.com" />
            </label>
            <label>
              <span class="input-label">密码</span>
              <input v-model="accountForm.password" class="input" type="password" autocomplete="new-password" :placeholder="editingID ? '留空不修改' : '可选，用于导入导出记录'" />
            </label>
            <label>
              <span class="input-label">分组 *</span>
              <select v-model.number="accountForm.group_id" class="input">
                <option v-if="selectableGroups.length === 0" :value="0" disabled>暂无可添加邮箱的分组</option>
                <option v-for="group in selectableGroups" :key="group.id" :value="group.id">{{ outlookGroupDisplayName(group) }}</option>
              </select>
            </label>
            <label>
              <span class="input-label">Client ID</span>
              <input v-model.trim="accountForm.client_id" class="input" :placeholder="editingID ? '当前账号使用的 Client ID' : '可选，填写后可用于自定义授权'" />
            </label>
            <label class="md:col-span-2">
              <span class="input-label">Refresh Token {{ editingID ? '' : '*' }}</span>
              <textarea v-model.trim="accountForm.refresh_token" class="input outlook-token-textarea" :placeholder="editingID ? '留空不修改；可粘贴 https://localhost/?code=... 自动换 token' : '可粘贴 Refresh Token，或粘贴 https://localhost/?code=... 自动换 token'" @paste="handleRefreshTokenPaste"></textarea>
            </label>
            <label class="md:col-span-2">
              <span class="input-label">备注</span>
              <input v-model.trim="accountForm.remark" class="input" placeholder="可选" />
            </label>
            <div class="md:col-span-2 rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800">
              <div class="flex flex-wrap items-center gap-2">
                <button class="btn btn-primary" type="button" :disabled="authBusy" @click="startOAuthPopup"><KeyRound class="h-4 w-4" />一键授权</button>
                <button class="btn btn-primary" type="button" :disabled="authBusy" @click="buildManualAuthURL"><ExternalLink class="h-4 w-4" />手动授权</button>
                <button class="btn btn-primary" type="button" :disabled="authBusy" @click="buildCustomAuthURL"><Pencil class="h-4 w-4" />自定义授权</button>
              </div>
              <div class="mt-3 space-y-1 text-xs leading-5 text-gray-500 dark:text-dark-400">
                <p>手动授权：使用 Thunderbird 公共 Client ID，授权后把 https://localhost/?code=... 粘到 Refresh Token 框会自动换 token。</p>
                <p>自定义授权：先填写自己的 Client ID，再点击自定义授权，授权后同样粘贴回调地址自动换 token。</p>
              </div>
            </div>
          </div>
          <div class="flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="accountModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" @click="saveAccount">保存</button>
          </div>
        </div>
      </div>

      <div v-if="batchModalOpen" class="outlook-modal-mask">
        <div class="w-full max-w-2xl rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold">批量添加微软邮箱</h3>
            <button class="modal-close-button" type="button" @click="batchModalOpen = false"><X class="h-5 w-5" /></button>
          </div>
          <div class="grid gap-4 p-6">
            <label>
              <span class="input-label">分组</span>
              <select v-model.number="batchForm.group_id" class="input">
                <option v-if="selectableGroups.length === 0" :value="0" disabled>暂无可添加邮箱的分组</option>
                <option v-for="group in selectableGroups" :key="group.id" :value="group.id">{{ outlookGroupDisplayName(group) }}</option>
              </select>
            </label>
            <label>
              <span class="input-label">账号数据</span>
              <textarea v-model="batchForm.content" class="input outlook-batch-textarea" placeholder="邮箱----密码----client_id----refresh_token"></textarea>
            </label>
          </div>
          <div class="flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="batchModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" @click="saveBatch">导入</button>
          </div>
        </div>
      </div>

      <div v-if="exportModalOpen" class="outlook-modal-mask center-mail-modal">
        <div class="outlook-import-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold text-gray-900 dark:text-white">导出数据</h3>
            <button class="modal-close-button" type="button" @click="exportModalOpen = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="outlook-modal-scroll-body p-6">
            <p class="text-sm text-gray-600 dark:text-dark-300">导出加密 ZIP 文件，里面包含微软邮箱账号、密码、Refresh Token 和全部分组。</p>
            <div class="outlook-import-warning">请妥善保存导出密码。导入或解压这个 ZIP 文件时必须输入同一个密码。</div>
            <label class="mt-5 block">
              <span class="input-label">ZIP 密码 *</span>
              <input v-model="exportPassword" class="input" type="password" autocomplete="new-password" placeholder="请输入导出 ZIP 密码" @keyup.enter="exportOutlookDataFile" />
            </label>
          </div>
          <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="exportModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="exportingOutlookData || !exportPassword.trim()" @click="exportOutlookDataFile">{{ exportingOutlookData ? '导出中...' : '开始导出' }}</button>
          </div>
        </div>
      </div>

      <div v-if="importModalOpen" class="outlook-modal-mask center-mail-modal">
        <div class="outlook-import-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold text-gray-900 dark:text-white">导入数据</h3>
            <button class="modal-close-button" type="button" @click="importModalOpen = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="outlook-modal-scroll-body p-6">
            <p class="text-sm text-gray-600 dark:text-dark-300">上传导出的加密 ZIP 文件以批量导入微软邮箱账号与分组。</p>
            <div class="outlook-import-warning">导入文件包含微软邮箱密码/Refresh Token；同一分组内重复邮箱会覆盖，不同分组的同邮箱会保留为独立记录。</div>
            <label class="mt-5 block">
              <span class="input-label">数据文件</span>
              <div class="outlook-import-file-box">
                <div class="min-w-0">
                  <div class="truncate text-sm font-bold text-gray-800 dark:text-dark-100">{{ importFileName || '请选择数据文件' }}</div>
                  <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">ZIP (.zip)</div>
                </div>
                <button class="outlook-import-file-button" type="button" @click="chooseImportFile">选择文件</button>
                <input ref="importFileInputRef" class="hidden" type="file" accept=".zip,application/zip,application/x-zip-compressed" @change="handleImportFileChange" />
              </div>
            </label>
            <label class="mt-5 block">
              <span class="input-label">ZIP 密码 *</span>
              <input v-model="importPassword" class="input" type="password" autocomplete="current-password" placeholder="请输入导出时设置的 ZIP 密码" @keyup.enter="startImportOutlookData" />
            </label>
          </div>
          <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="importModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="importingOutlookData || !importFile || !importPassword.trim()" @click="startImportOutlookData">{{ importingOutlookData ? '导入中...' : '开始导入' }}</button>
          </div>
        </div>
      </div>

      <div
        v-if="activeAccountMenuID && activeAccountMenuItem"
        data-outlook-row-menu
        class="outlook-row-menu fixed w-36 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 text-left shadow-xl dark:border-dark-600 dark:bg-dark-800"
        :style="{ left: `${accountMenuX}px`, top: `${accountMenuY}px` }"
        @click.stop
      >
        <button class="outlook-row-menu-item" type="button" @click="openReadModal(activeAccountMenuItem); activeAccountMenuID = null">
          <Inbox class="h-4 w-4" />
          <span>收件</span>
        </button>
        <button class="outlook-row-menu-item" type="button" @click="openRemarkModal(activeAccountMenuItem)">
          <StickyNote class="h-4 w-4" />
          <span>备注</span>
        </button>
      </div>

      <div v-if="remarkModalOpen" class="outlook-modal-mask">
        <div class="outlook-remark-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold text-gray-900 dark:text-white">编辑备注</h3>
            <button class="modal-close-button" type="button" @click="remarkModalOpen = false"><X class="h-5 w-5" /></button>
          </div>
          <div class="p-5">
            <label class="block">
              <span class="input-label">备注</span>
              <textarea v-model.trim="remarkText" class="input outlook-remark-textarea" placeholder="请输入备注"></textarea>
            </label>
          </div>
          <div class="flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="remarkModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="remarkSaving" @click="saveRemark">{{ remarkSaving ? '保存中...' : '保存' }}</button>
          </div>
        </div>
      </div>

      <div v-if="readModalOpen && readTarget" class="outlook-modal-mask center-mail-modal">
        <div class="outlook-read-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-base font-bold text-gray-900 dark:text-white">收件</h3>
            <label v-if="!selectedMessage" class="outlook-read-search search-clear-field">
              <Search class="h-4 w-4 text-gray-400 dark:text-dark-400" />
              <input v-model.trim="readSearchQuery" class="search-clear-input" type="search" placeholder="搜索标题 / 收件人 / 发件人" />
              <button v-if="readSearchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="readSearchQuery = ''">
                <X class="h-3.5 w-3.5" />
              </button>
            </label>
            <button class="modal-close-button" type="button" @click="readModalOpen = false"><X class="h-5 w-5" /></button>
          </div>
          <div class="outlook-read-body">
            <aside class="outlook-read-sidebar">
              <div class="text-xs text-gray-500 dark:text-dark-400">当前邮箱</div>
              <div class="mt-2 break-all text-sm font-bold leading-6 text-gray-900 dark:text-white">{{ readTarget.email }}</div>
              <label class="mt-5 block">
                <span class="input-label">收取封数</span>
                <input v-model.number="readLimit" class="input" min="1" max="100" type="number" />
              </label>
              <button class="mt-3 w-full rounded-lg bg-primary-600 px-4 py-2.5 text-sm font-bold text-white hover:bg-primary-500 disabled:cursor-not-allowed disabled:opacity-60" type="button" :disabled="readLoading" @click="fetchMessages">{{ readLoading ? '收取中...' : '收取邮件' }}</button>
              <div class="mt-3 grid gap-2">
                <button class="outlook-folder-button" :class="{ active: readFolder === 'inbox' }" type="button" @click="setReadFolder('inbox')">
                  <Inbox class="h-4 w-4" />
                  <span>收件箱</span>
                </button>
                <button class="outlook-folder-button" :class="{ active: readFolder === 'junkemail' }" type="button" @click="setReadFolder('junkemail')">
                  <Trash2 class="h-4 w-4" />
                  <span>垃圾箱</span>
                </button>
              </div>
            </aside>
            <section v-if="!selectedMessage" class="outlook-message-list">
              <table class="w-full table-fixed text-left text-sm">
                <thead class="bg-gray-50 text-gray-700 dark:bg-dark-800 dark:text-dark-200">
                  <tr>
                    <th class="outlook-receive-col-subject px-3 py-3 font-bold">标题</th>
                    <th class="outlook-receive-col-address px-3 py-3 font-bold">收件人</th>
                    <th class="outlook-receive-col-address px-3 py-3 font-bold">发件人</th>
                    <th class="outlook-receive-col-time px-3 py-3 font-bold">时间</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="message in readVisibleMessages" :key="message.id" class="outlook-message-row" @click="openMessage(message)">
                    <td class="truncate px-3 py-4 text-gray-700 dark:text-dark-200" :title="message.subject">{{ message.subject || '无标题' }}</td>
                    <td class="truncate px-3 py-4 text-gray-500 dark:text-dark-400" :title="mailContactEmails(message.to)">{{ mailContactEmails(message.to) || '-' }}</td>
                    <td class="truncate px-3 py-4 text-gray-500 dark:text-dark-400" :title="mailContactEmails(message.from)">{{ mailContactEmails(message.from) || '-' }}</td>
                    <td class="whitespace-nowrap px-3 py-4 text-gray-500 dark:text-dark-400" :title="message.time">{{ message.time || '-' }}</td>
                  </tr>
                  <tr v-if="readVisibleMessages.length === 0">
                    <td class="px-3 py-4 text-gray-400 dark:text-dark-400" colspan="4">暂无邮件</td>
                  </tr>
                </tbody>
              </table>
            </section>
            <section v-else class="outlook-detail-panel">
              <div class="outlook-detail-sticky">
                <button class="outlook-detail-back" type="button" @click="closeReadDetail">返回列表</button>
                <h4 class="mt-3 text-base font-bold text-gray-900 dark:text-white">{{ selectedMessage.subject || '无标题' }}</h4>
                <div class="mt-4 grid gap-2 text-sm text-gray-500 dark:text-dark-300">
                  <div>发件人：{{ mailContactDetail(selectedMessage.from) || '-' }}</div>
                  <div>收件人：{{ mailContactDetail(selectedMessage.to) || '-' }}</div>
                  <div>时间：{{ selectedMessage.time || '-' }}</div>
                  <div>所属：{{ messageFolderName }}</div>
                </div>
              </div>
              <div v-if="readDetailLoading" class="mt-5 text-sm text-gray-400 dark:text-dark-400">读取中...</div>
              <SafeMailFrame
                v-else
                class="outlook-detail-content"
                :html="selectedMessage.html"
                :text="selectedMessage.body || selectedMessage.body_preview"
                :title="selectedMessage.subject || '邮件正文'"
              />
            </section>
          </div>
          <div v-if="!selectedMessage" class="outlook-read-footer">
            <PaginationBar
              :page="readPage"
              :pages="readTotalPages"
              :page-size="readPageSize"
              :page-size-options="readPageSizeOptions"
              :total="readTotal"
              @page-change="setReadPage"
              @page-size-change="selectReadPageSize"
            />
            <div v-if="false" class="outlook-read-footer-left">
              <span>显示 {{ readPageStart }} 至 {{ readPageEnd }} 共 {{ readTotal }} 条结果</span>
              <span>每页:</span>
              <div class="page-size-select relative w-20" data-outlook-read-page-size-select>
                <button class="page-size-trigger" type="button" @click.stop="readPageSizeDropdownOpen = !readPageSizeDropdownOpen">
                  <span>{{ readPageSize }}</span>
                  <ChevronDown class="h-4 w-4 transition-transform" :class="{ 'rotate-180': readPageSizeDropdownOpen }" />
                </button>
                <div v-if="readPageSizeDropdownOpen" class="page-size-menu">
                  <button v-for="size in readPageSizeOptions" :key="size" class="page-size-option" :class="{ 'page-size-option-active': size === readPageSize }" type="button" @click="selectReadPageSize(size)">
                    <span>{{ size }}</span>
                    <Check v-if="size === readPageSize" class="h-4 w-4" />
                  </button>
                </div>
              </div>
            </div>
            <div v-if="false" class="receive-page-numbers">
              <button class="receive-page-square receive-page-arrow receive-page-prev" type="button" :disabled="readPage <= 1" @click="changeReadPage(-1)">‹</button>
              <template v-for="item in readPaginationItems" :key="item.key">
                <span v-if="item.type === 'ellipsis'" class="receive-page-ellipsis">...</span>
                <button v-else class="receive-page-square receive-page-number" :class="{ active: item.page === readPage }" type="button" @click="setReadPage(item.page)">{{ item.page }}</button>
              </template>
              <button class="receive-page-square receive-page-arrow receive-page-next" type="button" :disabled="readPage >= readTotalPages" @click="changeReadPage(1)">›</button>
              <form class="page-jump-form" @submit.prevent="jumpToReadPage">
                <input
                  v-model.trim="readPageJump"
                  class="page-jump-input"
                  type="text"
                  inputmode="numeric"
                  pattern="[0-9]*"
                  min="1"
                  :max="readTotalPages"
                  :placeholder="String(readPage)"
                  aria-label="跳转页码"
                />
                <button class="page-jump-button" type="submit" title="跳转页码">
                  <ChevronRight class="h-4 w-4" />
                </button>
              </form>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.outlook-page-layout {
  display: flex;
  align-items: stretch;
  min-width: 0;
  width: 100%;
}

.outlook-group-panel {
  display: flex;
  flex-direction: column;
  width: 224px;
  min-height: calc(100vh - 8rem);
  max-height: calc(100vh - 8rem);
  overflow: hidden;
}

.outlook-group-panel > div:first-child {
  padding: 0.8rem 1rem;
}

.outlook-group-panel h2 {
  font-size: 0.95rem;
}

.outlook-group-list-wrap {
  min-height: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.outlook-group-list {
  min-height: 0;
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
}

.outlook-group-list::-webkit-scrollbar {
  width: 0.55rem;
  height: 0.55rem;
}

.outlook-group-list::-webkit-scrollbar-track {
  background: transparent;
}

.outlook-group-list::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.55);
}

.dark .outlook-group-list::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.75);
}

.outlook-group-item {
  -webkit-tap-highlight-color: transparent;
  min-height: 2.15rem;
  padding-top: 0.45rem !important;
  padding-bottom: 0.45rem !important;
  font-size: 0.8125rem;
}

.outlook-group-name-viewport {
  display: block;
  min-width: 0;
  flex: 1;
  overflow: hidden;
}

.outlook-group-name-inner {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 0.5rem;
  transition: transform 0.06s linear;
  will-change: transform;
}

.outlook-group-name {
  display: block;
  flex: 0 0 auto;
  min-width: 0;
  overflow: visible;
  white-space: nowrap;
  word-break: keep-all;
  line-height: 1.15;
}

.outlook-group-count {
  flex-shrink: 0;
  min-width: 1.35rem;
  margin-left: 0.75rem;
  text-align: center;
}

.outlook-group-count-placeholder {
  flex-shrink: 0;
  width: 1.35rem;
  margin-left: 0.75rem;
}

.outlook-group-horizontal-scroll {
  flex-shrink: 0;
  padding: 0 0.75rem 0.35rem;
}

.outlook-group-horizontal-scroll-body {
  width: 100%;
  height: 0.75rem;
  overflow-x: auto;
  overflow-y: hidden;
}

.outlook-group-horizontal-scroll-body > div {
  height: 1px;
}

.outlook-group-horizontal-scroll-body::-webkit-scrollbar {
  height: 0.55rem;
}

.outlook-group-horizontal-scroll-body::-webkit-scrollbar-track {
  background: transparent;
}

.outlook-group-horizontal-scroll-body::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.55);
}

.dark .outlook-group-horizontal-scroll-body::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.75);
}

.outlook-group-item:focus {
  outline: none;
}

.outlook-group-item:active {
  transform: none;
}

.dark .outlook-group-item:hover,
.dark .outlook-group-item:focus-visible {
  background: rgb(51 65 85 / 0.7) !important;
  color: rgb(255 255 255) !important;
}

.dark .outlook-group-item.bg-primary-50,
.dark .outlook-group-item.dark\:bg-dark-700 {
  background: rgb(51 65 85) !important;
}

.outlook-account-panel {
  display: flex;
  min-width: 0;
  max-width: 100%;
  flex: 1 1 0;
  flex-direction: column;
  min-height: calc(100vh - 8rem);
}

.outlook-account-panel > div:first-child {
  padding: 0.75rem 1rem;
}

.outlook-account-body {
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.outlook-action-primary,
.outlook-action-secondary,
.outlook-action-more,
.outlook-action-refresh,
.outlook-action-danger {
  display: inline-flex;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  border-radius: 0.65rem;
  padding: 0 0.85rem;
  font-size: 0.8125rem;
  font-weight: 600;
  transition: transform 0.15s ease, box-shadow 0.15s ease, background-color 0.15s ease, color 0.15s ease;
}

.outlook-action-primary {
  background: linear-gradient(135deg, rgb(20 184 166), rgb(13 148 136));
  color: white;
  box-shadow: 0 12px 22px rgb(20 184 166 / 0.22);
}

.outlook-action-secondary {
  border: 1px solid rgb(20 184 166 / 0.35);
  background: rgb(240 253 250);
  color: rgb(15 118 110);
}

.outlook-action-more {
  border: 1px solid rgb(148 163 184 / 0.55);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
  min-width: 8.5rem;
}

.outlook-action-refresh {
  border: 1px solid rgb(148 163 184 / 0.45);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
}

.outlook-action-danger {
  background: rgb(239 68 68);
  color: white;
}

.outlook-action-primary:hover,
.outlook-action-secondary:hover,
.outlook-action-more:hover,
.outlook-action-refresh:hover,
.outlook-action-danger:hover {
  transform: translateY(-1px);
}

.outlook-action-refresh:disabled {
  cursor: wait;
  opacity: 0.72;
}

.outlook-action-refresh:disabled:hover {
  transform: none;
}

.dark .outlook-action-secondary {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: rgb(94 234 212);
}

html.dark .outlook-action-more,
html.dark .outlook-action-refresh {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.outlook-more-menu {
  position: absolute;
  left: 0;
  top: calc(100% + 0.55rem);
  z-index: 30;
  width: 14.5rem;
  overflow: hidden;
  border: 1px solid rgb(226 232 240);
  border-radius: 0.75rem;
  background: rgb(255 255 255);
  padding: 0.65rem;
  color: rgb(51 65 85);
  box-shadow: 0 18px 38px rgb(15 23 42 / 0.16);
}

.outlook-more-menu-label {
  margin-bottom: 0.35rem;
  padding: 0 0.25rem;
  font-size: 0.75rem;
  color: rgb(100 116 139);
}

.outlook-more-menu-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.75rem;
  border-radius: 0.6rem;
  padding: 0.65rem 0.5rem;
  font-size: 0.875rem;
  font-weight: 700;
  color: rgb(51 65 85);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.outlook-more-menu-item:hover {
  background: rgb(241 245 249);
  color: rgb(15 23 42);
}

.outlook-more-menu-icon {
  display: inline-flex;
  height: 2rem;
  width: 2rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.65rem;
}

.outlook-more-menu-icon.import {
  background: rgb(204 251 241);
  color: rgb(15 118 110);
}

.outlook-more-menu-icon.export {
  background: rgb(219 234 254);
  color: rgb(37 99 235);
}

.outlook-more-selected-badge {
  margin-left: auto;
  border-radius: 999px;
  background: rgb(204 251 241);
  padding: 0.2rem 0.55rem;
  font-size: 0.72rem;
  color: rgb(15 118 110);
}

html.dark .outlook-more-menu {
  border-color: rgb(71 85 105 / 0.55);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
  box-shadow: 0 18px 38px rgb(2 6 23 / 0.3);
}

html.dark .outlook-more-menu-label {
  color: rgb(148 163 184);
}

html.dark .outlook-more-menu-item {
  color: rgb(226 232 240);
}

html.dark .outlook-more-menu-item:hover {
  background: rgb(51 65 85 / 0.72);
  color: white;
}

html.dark .outlook-more-menu-icon.import {
  background: rgb(20 184 166 / 0.16);
  color: rgb(45 212 191);
}

html.dark .outlook-more-menu-icon.export {
  background: rgb(59 130 246 / 0.16);
  color: rgb(96 165 250);
}

html.dark .outlook-more-selected-badge {
  background: rgb(20 184 166 / 0.16);
  color: rgb(94 234 212);
}

.outlook-toolbar-batch-button,
.outlook-toolbar-batch-danger {
  display: inline-flex;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  border-radius: 0.65rem;
  padding: 0 0.85rem;
  font-size: 0.8125rem;
  font-weight: 700;
  color: white;
  transition: transform 0.15s ease, box-shadow 0.15s ease, background-color 0.15s ease;
}

.outlook-toolbar-batch-button {
  background: rgb(37 99 235);
  box-shadow: 0 10px 20px rgb(37 99 235 / 0.18);
}

.outlook-toolbar-batch-button:hover {
  background: rgb(29 78 216);
  transform: translateY(-1px);
}

.outlook-toolbar-batch-danger {
  background: rgb(239 68 68);
  box-shadow: 0 10px 20px rgb(239 68 68 / 0.18);
}

.outlook-toolbar-batch-danger:hover {
  background: rgb(220 38 38);
  transform: translateY(-1px);
}

.outlook-table-area {
  --outlook-col-select: 3.1rem;
  --outlook-col-group: 14rem;
  --outlook-col-email: 19rem;
  --outlook-col-client: 12rem;
  --outlook-col-created: 10rem;
  --outlook-col-status: 5.2rem;
  --outlook-col-remark: 15.5rem;
  --outlook-col-actions: 170px;
  --outlook-table-min-width: calc(
    var(--outlook-col-select) +
    var(--outlook-col-group) +
    var(--outlook-col-email) +
    var(--outlook-col-client) +
    var(--outlook-col-created) +
    var(--outlook-col-status) +
    var(--outlook-col-remark) +
    var(--outlook-col-actions)
  );
  --outlook-table-divider: rgb(148 163 184 / 0.08);
  flex: 1;
  min-height: 100%;
  min-width: 0;
  width: 100%;
  max-width: 100%;
  max-height: min(62vh, 720px);
  overflow-x: auto;
  overflow-y: auto;
}

.outlook-virtual-row {
  height: 74px;
}

.dark .outlook-table-area {
  --outlook-table-divider: rgb(148 163 184 / 0.12);
}

.outlook-account-table {
  width: max(100%, var(--outlook-table-min-width));
  min-width: var(--outlook-table-min-width);
  table-layout: fixed;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 0.8125rem;
}

.outlook-account-table th {
  border-right: 1px solid var(--outlook-table-divider);
}

.outlook-account-table th,
.outlook-account-table td {
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.65rem 0.8rem !important;
}

.dark .outlook-account-table th,
.dark .outlook-account-table td {
  border-bottom-color: rgb(51 65 85);
}

.outlook-col-select { width: var(--outlook-col-select); }
.outlook-col-group { width: var(--outlook-col-group); }
.outlook-col-email { width: var(--outlook-col-email); }
.outlook-col-client { width: var(--outlook-col-client); }
.outlook-col-created { width: var(--outlook-col-created); }
.outlook-col-status { width: var(--outlook-col-status); }
.outlook-col-remark { width: var(--outlook-col-remark); }
.outlook-col-actions { width: var(--outlook-col-actions); }

.outlook-status-cell {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  position: relative;
  width: 100%;
}

.outlook-account-table td:nth-child(6) .badge {
  white-space: nowrap;
}

.outlook-status-reason {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1rem;
  height: 1rem;
  color: rgb(248 113 113);
  cursor: help;
  outline: none;
}

.outlook-status-reason:hover,
.outlook-status-reason:focus-visible {
  color: rgb(239 68 68);
}

.outlook-status-tooltip {
  position: absolute;
  z-index: 80;
  top: calc(100% + 0.55rem);
  left: 50%;
  width: max-content;
  max-width: 18rem;
  transform: translateX(-50%);
  border-radius: 0.5rem;
  background: rgb(15 23 42);
  color: white;
  padding: 0.55rem 0.7rem;
  text-align: left;
  font-size: 0.75rem;
  line-height: 1.45;
  white-space: normal;
  overflow-wrap: anywhere;
  box-shadow: 0 18px 35px rgb(15 23 42 / 0.26);
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.outlook-status-tooltip::before {
  content: '';
  position: absolute;
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 0.35rem solid transparent;
  border-bottom-color: rgb(15 23 42);
}

.outlook-status-reason:hover .outlook-status-tooltip,
.outlook-status-reason:focus-visible .outlook-status-tooltip {
  opacity: 1;
  transform: translateX(-50%) translateY(0.1rem);
}

.outlook-account-table th:last-child,
.outlook-account-table td:last-child {
  border-right: 0;
}

.outlook-select-col {
  width: var(--outlook-col-select) !important;
  text-align: center;
}

.outlook-select-col input {
  height: 0.95rem;
  width: 0.95rem;
  accent-color: rgb(20 184 166);
}

.outlook-account-table th:nth-child(2),
.outlook-account-table td:nth-child(2) {
  width: var(--outlook-col-group);
}

.outlook-account-table th:nth-child(3),
.outlook-account-table td:nth-child(3) {
  width: var(--outlook-col-email);
}

.outlook-account-table th:nth-child(4),
.outlook-account-table td:nth-child(4) {
  width: var(--outlook-col-client);
}

.outlook-account-table th:nth-child(5),
.outlook-account-table td:nth-child(5) {
  width: var(--outlook-col-created);
}

.outlook-account-table th:nth-child(6),
.outlook-account-table td:nth-child(6) {
  width: var(--outlook-col-status);
  padding-left: 0.35rem !important;
  padding-right: 0.35rem !important;
  text-align: center;
}

.outlook-account-table th:nth-child(7),
.outlook-account-table td:nth-child(7) {
  width: var(--outlook-col-remark);
}

.outlook-account-table td:nth-child(2),
.outlook-account-table td:nth-child(3),
.outlook-account-table td:nth-child(4),
.outlook-account-table td:nth-child(5),
.outlook-account-table td:nth-child(7) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.outlook-account-table thead th {
  position: sticky;
  top: 0;
  z-index: 15;
  background: rgb(249 250 251);
  white-space: nowrap;
}

.dark .outlook-account-table thead th {
  background: rgb(30 41 59);
}

.outlook-account-table thead .sticky-col-right {
  z-index: 25;
}

.outlook-account-table td:nth-child(4) {
  line-height: 1.45;
}

.outlook-group-table-name,
.outlook-created-time {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.outlook-client-lines {
  display: grid;
  gap: 0.2rem;
  min-width: 0;
  line-height: 1.45;
}

.outlook-client-lines > div {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.outlook-token-refresh-line {
  font-size: 0.75rem;
  font-weight: 600;
  color: rgb(20 184 166);
}

.dark .outlook-token-refresh-line {
  color: rgb(94 234 212);
}

.outlook-sort-button {
  display: inline-flex;
  width: 100%;
  min-width: max-content;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
  color: inherit;
  transition: color 0.15s ease;
}

.outlook-sort-button::before {
  content: '';
  width: 0.875rem;
  flex: 0 0 0.875rem;
}

.outlook-sort-button > svg {
  flex: 0 0 0.875rem;
}

.outlook-account-table th:nth-child(6) .outlook-sort-button {
  min-width: 0;
  gap: 0.15rem;
}

.outlook-account-table th:nth-child(6) .outlook-sort-button::before {
  display: none;
}

.outlook-account-table th:nth-child(6) .outlook-sort-button > svg {
  margin-right: -0.25rem;
}

.outlook-sort-label {
  flex: 0 0 auto;
  white-space: nowrap;
}

.outlook-sort-button:hover {
  color: rgb(20 184 166);
}

.outlook-email-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.35rem;
}

.outlook-email-link {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
  color: inherit;
  font: inherit;
  transition: color 0.15s ease;
}

.outlook-email-link:hover {
  color: rgb(20 184 166);
  text-decoration: underline;
  text-underline-offset: 3px;
}

.outlook-email-copy-button {
  display: inline-flex;
  width: 1.45rem;
  height: 1.45rem;
  flex: 0 0 1.45rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.45rem;
  color: rgb(148 163 184);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.outlook-email-copy-button:hover {
  background: rgb(20 184 166 / 0.12);
  color: rgb(20 184 166);
}

.sticky-col-right {
  position: sticky;
  right: 0;
  z-index: 10;
  width: var(--outlook-col-actions);
  min-width: var(--outlook-col-actions);
  overflow: visible;
  background: white;
  background-clip: padding-box;
  box-shadow: -18px 0 28px rgb(15 23 42 / 0.1);
}

.sticky-col-right::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 1px;
  background: var(--outlook-table-divider);
  pointer-events: none;
}

thead .sticky-col-right {
  z-index: 20;
  background: rgb(249 250 251);
}

.dark .sticky-col-right {
  background: rgb(15 23 42);
  box-shadow: -18px 0 30px rgb(0 0 0 / 0.3);
}

.dark thead .sticky-col-right {
  background: rgb(30 41 59);
}

tbody tr:hover .sticky-col-right {
  background: rgb(249 250 251);
}

.dark tbody tr:hover .sticky-col-right {
  background: rgb(30 41 59);
}

.page-size-trigger {
  display: inline-flex;
  height: 2.25rem;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  border-radius: 0.75rem;
  border: 1px solid rgb(20 184 166);
  background: rgb(255 255 255);
  padding: 0 0.75rem;
  font-size: 0.875rem;
  color: rgb(17 24 39);
  outline: none;
}

html.dark .page-size-trigger {
  background: rgb(30 41 59);
  color: rgb(255 255 255);
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
  bottom: calc(100% + 0.5rem);
  left: 0;
  z-index: 60;
  width: 7rem;
  overflow: hidden;
  border-radius: 0.75rem;
  border: 1px solid rgb(203 213 225);
  background: rgb(255 255 255);
  box-shadow: 0 18px 40px rgb(15 23 42 / 0.25);
}

html.dark .page-size-menu {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59);
}

.page-size-option {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  padding: 0.65rem 0.9rem;
  text-align: left;
  font-size: 0.875rem;
  color: rgb(51 65 85);
}

.page-size-option:hover {
  background: rgb(241 245 249);
}

.page-size-option-active {
  color: rgb(20 184 166);
}

html.dark .page-size-option {
  color: rgb(226 232 240);
}

html.dark .page-size-option:hover {
  background: rgb(51 65 85);
}

html.dark .page-size-option-active {
  color: rgb(45 212 191);
}

.outlook-row-menu {
  z-index: 2147483647;
  pointer-events: auto;
  box-shadow: 0 18px 40px rgb(15 23 42 / 0.2);
}

.outlook-row-actions {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
}

.outlook-row-action-button {
  display: inline-flex;
  width: 1.95rem;
  flex-shrink: 0;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.15rem;
  line-height: 1;
  transition: color 0.15s ease;
}

.outlook-row-action-button span {
  display: block;
  white-space: nowrap;
  word-break: keep-all;
  writing-mode: horizontal-tb;
  font-size: 0.6875rem;
  line-height: 0.9rem;
}

.outlook-row-menu-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.65rem;
  padding: 0.65rem 0.85rem;
  font-size: 0.8125rem;
  color: rgb(55 65 81);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.outlook-row-menu-item:hover {
  background: rgb(243 244 246);
  color: rgb(17 24 39);
}

.dark .outlook-row-menu-item {
  color: rgb(226 232 240);
}

.dark .outlook-row-menu-item:hover {
  background: rgb(51 65 85);
  color: white;
}

.outlook-remark-modal {
  width: min(30rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
}

.outlook-remark-textarea {
  min-height: 8rem !important;
  resize: vertical;
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

.outlook-account-modal {
  width: min(52rem, calc(100vw - 1rem));
  max-height: calc(100vh - 1rem);
  overflow: hidden;
}

.outlook-modal-scroll {
  max-height: calc(100vh - 12rem);
  overflow-y: auto;
}

.outlook-token-textarea {
  min-height: 6rem;
  resize: vertical;
}

.outlook-batch-textarea {
  min-height: 14rem;
  resize: vertical;
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

.dark .outlook-import-warning {
  border-color: rgb(245 158 11 / 0.28);
  background: rgb(245 158 11 / 0.1);
  color: rgb(252 211 77);
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

.dark .outlook-import-file-box {
  border-color: rgb(71 85 105);
  background: rgb(15 23 42 / 0.35);
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

.dark .outlook-import-file-button {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: rgb(94 234 212);
}

.outlook-context-menu {
  z-index: 2147483647;
}

.outlook-context-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.7rem;
  padding: 0.65rem 0.9rem;
  text-align: left;
  font-size: 0.875rem;
  color: rgb(55 65 81);
}

.outlook-context-item:hover {
  background: rgb(243 244 246);
}

.outlook-context-item:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.dark .outlook-context-item {
  color: rgb(226 232 240);
}

.dark .outlook-context-item:hover {
  background: rgb(51 65 85);
}

.outlook-read-modal {
  width: min(100rem, calc(100vw - 1rem));
  height: min(48rem, calc(100vh - 1rem));
  max-height: calc(100vh - 1rem);
  overflow: hidden;
}

.outlook-read-body {
  display: grid;
  grid-template-columns: 8.75rem minmax(0, 1fr);
  gap: 1.9rem;
  min-height: 0;
  flex: 1;
  padding: 1.6rem 2.4rem 1rem 1.6rem;
}

.outlook-read-search {
  display: inline-flex;
  width: min(30rem, 45vw);
  height: 2.35rem;
  align-items: center;
  gap: 0.55rem;
  border-radius: 0.7rem;
  border: 1px solid rgb(203 213 225);
  background: rgb(255 255 255);
  padding: 0 0.8rem;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.04);
}

.outlook-read-search input {
  min-width: 0;
  flex: 1;
  border: 0;
  background: transparent;
  color: rgb(15 23 42);
  font-size: 0.875rem;
  outline: none;
}

.outlook-read-search input::placeholder {
  color: rgb(148 163 184);
}

html.dark .outlook-read-search {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59 / 0.58);
  box-shadow: none;
}

html.dark .outlook-read-search input {
  color: rgb(226 232 240);
}

html.dark .outlook-read-search input::placeholder {
  color: rgb(148 163 184);
}

.outlook-read-sidebar {
  align-self: start;
  border-radius: 0.7rem;
  background: rgb(248 250 252);
  padding: 1rem 0.9rem;
}

.dark .outlook-read-sidebar {
  background: rgb(15 23 42 / 0.72);
}

.outlook-folder-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  min-height: 2.4rem;
  border: 1px solid rgb(226 232 240);
  border-radius: 0.65rem;
  background: white;
  color: rgb(51 65 85);
  font-size: 0.8125rem;
  font-weight: 700;
  transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease;
}

.outlook-folder-button.active {
  border-color: rgb(99 102 241);
  background: rgb(238 242 255);
  color: rgb(79 70 229);
}

.dark .outlook-folder-button {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42);
  color: rgb(203 213 225);
}

.dark .outlook-folder-button.active {
  border-color: rgb(45 212 191);
  background: rgb(20 184 166 / 0.14);
  color: rgb(94 234 212);
}

.outlook-message-list {
  min-width: 0;
  overflow: auto;
}

.outlook-message-list table {
  min-width: 58rem;
  border-collapse: collapse;
}

.outlook-receive-col-subject {
  width: 42%;
}

.outlook-receive-col-address {
  width: 22%;
}

.outlook-receive-col-time {
  width: 14%;
  min-width: 11.5rem;
}

.outlook-message-list th,
.outlook-message-list td {
  border-bottom: 1px solid rgb(226 232 240);
}

.outlook-message-row {
  cursor: pointer;
  transition: background-color 0.15s ease;
}

.outlook-message-row:hover {
  background: rgb(241 245 249 / 0.8);
}

.dark .outlook-message-row:hover {
  background: rgb(30 41 59 / 0.74);
}

.dark .outlook-message-list th,
.dark .outlook-message-list td {
  border-bottom-color: rgb(51 65 85);
}

.outlook-detail-panel {
  min-width: 0;
  overflow: auto;
  padding-right: 0.75rem;
}

.outlook-detail-sticky {
  position: sticky;
  top: 0;
  z-index: 5;
  margin-right: -0.75rem;
  padding: 0 0.75rem 0.85rem 0;
  background: white;
}

.dark .outlook-detail-sticky {
  background: rgb(15 23 42);
}

.outlook-detail-back {
  border-radius: 0.45rem;
  background: rgb(99 102 241);
  padding: 0.35rem 0.65rem;
  color: white;
  font-size: 0.75rem;
  font-weight: 700;
}

.outlook-detail-content {
  margin-top: 1rem;
  min-height: 0;
}

.outlook-detail-content :deep(a),
.outlook-detail-plain :deep(a) {
  color: rgb(37 99 235);
  text-decoration: underline;
  text-underline-offset: 2px;
  overflow-wrap: anywhere;
}

.dark .outlook-detail-content {
  color: inherit;
}

.dark .outlook-detail-content :deep(a),
.dark .outlook-detail-plain :deep(a) {
  color: rgb(94 234 212);
}

.outlook-detail-plain {
  font-family: inherit;
  line-height: 1.65;
}

.outlook-read-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1.5rem;
  min-height: 4.4rem;
  margin-left: 12.5rem;
  margin-right: 2.4rem;
  border-top: 1px solid rgb(241 245 249);
  color: rgb(100 116 139);
  font-size: 0.8125rem;
}

.outlook-read-footer-left {
  display: inline-flex;
  align-items: center;
  gap: 0.75rem;
}

.receive-page-numbers {
  display: inline-flex;
  align-items: center;
}

.receive-page-square {
  display: inline-flex;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(203 213 225);
  padding: 0;
  background: rgb(255 255 255);
  color: rgb(71 85 105);
  font-weight: 700;
  transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease;
}

.receive-page-arrow {
  min-width: 1.95rem;
}

.receive-page-number {
  min-width: 2.25rem;
  padding: 0 0.6rem;
}

.receive-page-square + .receive-page-square {
  margin-left: -1px;
}

.receive-page-ellipsis {
  display: inline-flex;
  min-width: 2rem;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(203 213 225);
  margin-left: -1px;
  padding: 0 0.5rem;
  background: rgb(255 255 255);
  color: rgb(100 116 139);
  font-weight: 700;
}

.receive-page-numbers .page-jump-form {
  margin-left: 0.35rem;
}

.receive-page-numbers .page-jump-input {
  width: 3.35rem;
}

.receive-page-numbers .page-jump-button {
  width: 1.95rem;
  min-width: 1.95rem;
  padding: 0;
}

.receive-page-square:not(:disabled):hover {
  background: rgb(248 250 252);
  color: rgb(15 23 42);
}

.receive-page-prev {
  border-radius: 0.5rem 0 0 0.5rem;
}

.receive-page-next {
  border-radius: 0 0.5rem 0.5rem 0;
}

.receive-page-square.active {
  z-index: 1;
  border-color: rgb(20 184 166);
  background: rgb(240 253 250);
  color: rgb(13 148 136);
}

.receive-page-square:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.dark .outlook-read-footer {
  border-top-color: rgb(30 41 59);
  color: rgb(148 163 184);
}

html.dark .receive-page-square,
html.dark .receive-page-ellipsis {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59);
  color: rgb(148 163 184);
}

html.dark .receive-page-square:not(:disabled):hover {
  background: rgb(51 65 85 / 0.72);
  color: white;
}

html.dark .receive-page-square.active {
  border-color: rgb(20 184 166);
  background: rgb(15 118 110 / 0.16);
  color: rgb(45 212 191);
}

@media (max-width: 1023px) {
  .outlook-page-layout {
    flex-direction: column;
  }

  .outlook-group-panel {
    width: 100%;
    min-height: 12rem;
    max-height: 16rem;
  }

  .outlook-account-panel {
    flex: 0 0 auto;
    min-height: calc(100vh - 24rem);
  }

  .outlook-read-body {
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
    overflow: auto;
    padding: 1rem;
  }

  .outlook-read-footer {
    align-items: flex-start;
    flex-direction: column;
    gap: 0.75rem;
    margin: 0 1rem;
    padding: 0.9rem 0;
  }

  .outlook-read-footer-left {
    flex-wrap: wrap;
  }
}

@media (min-width: 1600px) {
  .outlook-table-area {
    max-height: min(68vh, 900px);
  }
}

@media (max-width: 767px) {
  .outlook-page-layout {
    gap: 0.75rem;
  }

  .outlook-group-panel {
    min-height: 9rem;
    max-height: 12rem;
    border-radius: 0.875rem;
  }

  .outlook-account-panel {
    flex: 0 0 auto;
    min-height: 0;
    border-radius: 0.875rem;
  }

  .outlook-account-panel > div:first-child {
    align-items: stretch;
    padding: 0.75rem;
  }

  .outlook-account-panel > div:first-child > div:first-child {
    width: 100%;
  }

  .outlook-account-panel .search-clear-field {
    width: 100% !important;
    flex: 1 1 100% !important;
  }

  .outlook-table-area {
    flex: 0 0 auto;
    min-height: 18rem;
    max-height: 60svh;
  }

  .outlook-account-body {
    flex: 0 0 auto;
  }

  .outlook-account-panel > div:last-child {
    align-items: stretch;
    flex-direction: column;
    gap: 0.75rem;
    padding: 0.75rem;
  }

  .outlook-action-primary,
  .outlook-action-secondary,
  .outlook-action-more,
  .outlook-action-refresh,
  .outlook-toolbar-batch-button,
  .outlook-toolbar-batch-danger {
    flex: 1 1 9rem;
    min-width: 0;
    padding: 0 0.75rem;
    font-size: 0.8125rem;
  }

  .outlook-more-menu {
    right: auto;
    left: 0;
  }

  .outlook-modal-mask {
    align-items: stretch;
    padding: 0.5rem;
  }

  .outlook-account-modal,
  .outlook-remark-modal,
  :global(.outlook-import-modal),
  .outlook-read-modal {
    width: calc(100vw - 1rem);
    max-height: calc(100svh - 1rem);
    border-radius: 0.875rem;
  }

  .outlook-read-modal {
    height: calc(100svh - 1rem);
  }

  .outlook-modal-scroll {
    max-height: calc(100svh - 10rem);
  }
}

@media (max-width: 420px) {
  .outlook-action-primary,
  .outlook-action-secondary,
  .outlook-action-more,
  .outlook-action-refresh,
  .outlook-toolbar-batch-button,
  .outlook-toolbar-batch-danger {
    flex-basis: 100%;
    width: 100%;
  }
}
</style>
