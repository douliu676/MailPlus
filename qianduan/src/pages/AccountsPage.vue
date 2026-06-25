<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { Check, ChevronDown, ChevronRight, CircleHelp, Copy, Download, Folder, FolderPlus, Inbox, MoreHorizontal, Pencil, Play, Plus, RefreshCw, Search, Send, Server, StickyNote, Trash2, Upload, X } from 'lucide-vue-next'
import PaginationBar from '../components/PaginationBar.vue'
import SafeMailFrame from '../components/SafeMailFrame.vue'
import { useAppStore } from '../stores/app'
import { useTaskStore } from '../stores/tasks'
import { getAdminSettings } from '../api/adminSettings'
import { createMailGroup, deleteMailGroup, listMailGroups, updateMailGroup, type MailGroup } from '../api/mailGroups'
import { batchCreateMailAccounts, batchMailAction, createMailAccount, createMailDataExportTask, createMailDataImportTask, createMailServer, deleteMailAccount, deleteMailServer, listMailAccounts, listMailServers, receiveMailDetail, receiveMailMessages, sendMailAccountMessage, testMailAccount, updateMailAccount, updateMailServer, type AccountListFilter, type BackgroundTask, type MailAccount, type MailAccountListResponse, type MailServer, type ReceivedMailDetail, type ReceivedMailMessage, type SaveMailAccountPayload } from '../api/mailAccounts'
import { copyToClipboard } from '../utils/clipboard'
import { mailContactDetail, mailContactEmails } from '../utils/mailContacts'
import { mailAccountPageCacheKey, mailManagementCacheKey, normalizeMailAccountPageCache, rememberMailAccountPage, type MailAccountPageCacheEntry } from '../utils/mailManagementCache'
import { authSessionClearedEvent } from '../api/session'

const appStore = useAppStore()
const taskStore = useTaskStore()
const queryClient = useQueryClient()
const searchQuery = ref('')
const activeGroupID = ref(0)
const pageSizeStorageKey = 'mail_accounts_page_size'
const fallbackTablePageSize = 20
const fallbackTablePageSizeOptions = [10, 20, 50, 100]
const groupExpandedStorageKey = 'mail_group_expanded_ids'
const mailSortStorageKey = 'mail_accounts_sort'
function readPersistedPageSize() {
  const value = Number(localStorage.getItem(pageSizeStorageKey))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function normalizePageSizeOptions(values: unknown, defaultPageSize: number) {
  const options = Array.isArray(values) ? values : fallbackTablePageSizeOptions
  const result = options
    .map((value) => Number(value))
    .filter((value) => Number.isFinite(value) && value > 0)

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

const pageSize = ref(readPersistedPageSize() || fallbackTablePageSize)
const accountPage = ref(1)
const accountTotal = ref(0)
const accountPages = ref(0)
const accountPageJump = ref('')
const mailVirtualScrollTop = ref(0)
const mailVirtualViewportHeight = ref(520)
const mailVirtualRowHeight = 74
const mailVirtualOverscan = 6
const pageSizeDropdownOpen = ref(false)
const pageSizeOptions = ref<number[]>([fallbackTablePageSize])
const groupMenuOpen = ref(false)
const groupMenuX = ref(0)
const groupMenuY = ref(0)
const groupNameScrollX = ref(0)
const groupNameScrollMax = ref(0)
const contextGroup = ref<MailGroup | null>(null)
const expandedGroupIDs = ref<number[]>(readPersistedExpandedGroupIDs())
const showGroupModal = ref(false)
const showAddMailModal = ref(false)
const showBatchMailModal = ref(false)
const showServerModal = ref(false)
const moreActionsOpen = ref(false)
const importModalOpen = ref(false)
const exportModalOpen = ref(false)
const importFileName = ref('')
const importFile = ref<File | null>(null)
const importPassword = ref('')
const exportPassword = ref('')
const exportingMailData = ref(false)
const importFileInputRef = ref<HTMLInputElement | null>(null)
const importingMailData = ref(false)
const groupModalMode = ref<'create' | 'createChild' | 'edit'>('create')
const groupName = ref('')
const groupSortOrder = ref(1)
const groupSaving = ref(false)
const mailSaving = ref(false)
const batchSaving = ref(false)
const serverSaving = ref(false)
const serverRefreshing = ref(false)
const serverSearchQuery = ref('')
const mailRefreshing = ref(false)
const editingServerID = ref<number | null>(null)
const editingMailID = ref<number | null>(null)
const activeMailMenuID = ref<number | null>(null)
let accountAutoRefreshEnabled = false
type MailSortKey = 'group' | 'email' | 'server' | 'created_at' | 'status' | 'remark'
const selectedMailIDs = ref<number[]>([])
function readPersistedMailSort() {
  try {
    const value = JSON.parse(localStorage.getItem(mailSortStorageKey) || '{}')
    const key = ['group', 'email', 'server', 'created_at', 'status', 'remark'].includes(value.key) ? value.key as MailSortKey : 'created_at'
    const order = value.order === 'desc' ? 'desc' : 'asc'
    return { key, order }
  } catch {
    return { key: 'created_at' as MailSortKey, order: 'asc' as const }
  }
}
const persistedMailSort = readPersistedMailSort()
const mailSortKey = ref<MailSortKey>(persistedMailSort.key)
const mailSortOrder = ref<'asc' | 'desc'>(persistedMailSort.order)
const mailMenuX = ref(0)
const mailMenuY = ref(0)
const receiveModalOpen = ref(false)
const receiveTarget = ref<MailAccount | null>(null)
const receiveLimit = ref(5)
const receiveFolder = ref<'inbox' | 'trash'>('inbox')
const receiveSearchQuery = ref('')
const receiveLoading = ref(false)
const receiveDetailLoading = ref(false)
const receivePageSize = ref(fallbackTablePageSize)
const receivePage = ref(1)
const receivePageJump = ref('')
const receivePageSizeDropdownOpen = ref(false)
const receivePageSizeOptions = ref<number[]>([fallbackTablePageSize])
const receiveMessages = reactive<{ inbox: ReceivedMailMessage[]; trash: ReceivedMailMessage[] }>({
  inbox: [],
  trash: [],
})
const receiveCache = reactive<Record<number, { inbox: ReceivedMailMessage[]; trash: ReceivedMailMessage[] }>>({})
const receiveDetailCache = reactive<Record<number, Record<string, ReceivedMailDetail>>>({})
const receiveDetailPending: Record<number, Record<string, Promise<ReceivedMailDetail | null>>> = {}
const receiveCacheStoragePrefix = 'mail_receive_cache_v1:'
const selectedReceiveMessage = ref<ReceivedMailDetail | null>(null)
const sendModalOpen = ref(false)
const sendTarget = ref<MailAccount | null>(null)
const sendSending = ref(false)
const remarkModalOpen = ref(false)
const remarkSaving = ref(false)
const remarkTarget = ref<MailAccount | null>(null)
const remarkText = ref('')
const mailTableAreaRef = ref<HTMLElement | null>(null)
const mailGroupListRef = ref<HTMLElement | null>(null)
const mailColumnDividerLefts = ref<number[]>([])
let lastGroupClickAt = 0
let lastGroupClickID = 0
let receiveDetailRequestID = 0
let receiveWarmupRunID = 0

const fallbackGroups: MailGroup[] = [
  { id: 1, parent_id: 0, name: '全部邮箱', system: true, sort_order: 0, count: 0, created_at: '' },
  { id: 2, parent_id: 0, name: '默认分组', system: true, sort_order: 0, count: 0, created_at: '' },
]

const groups = ref<MailGroup[]>(fallbackGroups)

const accounts = ref<MailAccount[]>([])
const accountPageCache = ref<Record<string, MailAccountPageCacheEntry>>({})
const servers = ref<MailServer[]>([])
const selectedServerIDs = ref<number[]>([])

const mailForm = reactive({
  email: '',
  password: '',
  server_id: 0,
  group_id: 2,
  imap_host: '',
  smtp_host: '',
  imap_protocol: 'IMAP',
  imap_port: 993,
  imap_ssl: true,
  smtp_protocol: 'SMTP(SSL)',
  smtp_port: 465,
  smtp_ssl: true,
  remark: '',
})

const batchForm = reactive({
  content: '',
  server_id: 0,
  group_id: 2,
  imap_host: '',
  smtp_host: '',
  imap_protocol: 'IMAP',
  imap_port: 993,
  imap_ssl: true,
  smtp_protocol: 'SMTP(SSL)',
  smtp_port: 465,
  smtp_ssl: true,
})

const serverForm = reactive({
  name: '',
  imap_host: '',
  smtp_host: '',
})

const sendForm = reactive({
  nickname: '',
  recipient: '',
  subject: '',
  body: '',
})

const currentGroup = computed(() => groups.value.find((item) => item.id === activeGroupID.value) || groups.value[0] || fallbackGroups[0])
const allMailGroupCount = computed(() => groups.value.reduce((total, group) => {
  if (group.id === 1) return total
  return total + (Number(group.count) || 0)
}, 0))
function mailGroupCount(group: MailGroup) {
  if (group.id === 1) return allMailGroupCount.value
  return Number(group.count) || 0
}
const parentGroupName = computed(() => {
  if (!contextGroup.value || contextGroup.value.parent_id === 0) return ''
  return groups.value.find((item) => item.id === contextGroup.value?.parent_id)?.name || '无限别名'
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
function groupSortOrderValue(group: MailGroup) {
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
  const map = new Map<number, MailGroup[]>()
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
function groupHasChildren(groupID: number) {
  return (groupChildrenMap.value.get(groupID) || []).length > 0
}
function mailGroupIDSet(groupID: number) {
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
function mailAccountMatchesGroupFilter(accountGroupID: number, groupID?: number) {
  if (!groupID || groupID === 1) return true
  return mailGroupIDSet(groupID).has(accountGroupID)
}
function canGroupReceiveMail(groupID: number) {
  return groupID > 1 && !groupHasChildren(groupID)
}
const selectableGroups = computed(() => {
  const result: MailGroup[] = []
  const visit = (parentID: number) => {
    const children = groupChildrenMap.value.get(parentID) || []
    for (const group of children) {
      const hasChildren = groupHasChildren(group.id)
      if (canGroupReceiveMail(group.id)) {
        result.push(group)
      }
      if (hasChildren) {
        visit(group.id)
      }
    }
  }
  visit(0)
  return result
})
const defaultGroupID = computed(() => (canGroupReceiveMail(activeGroupID.value) ? activeGroupID.value : selectableGroups.value[0]?.id || 0))
function isSelectableMailGroup(groupID: number) {
  return selectableGroups.value.some((group) => group.id === groupID)
}
const serverOptions = computed(() => servers.value)
const filteredServers = computed(() => {
  const keyword = serverSearchQuery.value.trim().toLowerCase()
  if (!keyword) return servers.value
  return servers.value.filter((server) => server.name.toLowerCase().includes(keyword))
})
const activeMailMenuItem = computed(() => accounts.value.find((item) => item.id === activeMailMenuID.value) || null)
function groupDisplayName(group: MailGroup) {
  if (group.parent_id === 0) return group.name
  const parent = groups.value.find((item) => item.id === group.parent_id)
  return parent ? `${parent.name}\\${group.name}` : group.name
}

const visibleGroups = computed(() => {
  const result: Array<MailGroup & { level: number; hasChildren: boolean }> = []
  const visit = (parentID: number, level: number) => {
    const children = groupChildrenMap.value.get(parentID) || []
    for (const group of children) {
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

const filteredAccounts = computed(() => {
  return accounts.value
})
function mailSortValue(item: MailAccount, key: MailSortKey) {
  if (key === 'group') return item.group_name || ''
  if (key === 'server') return `${item.imap_host || ''} ${item.smtp_host || ''}`
  if (key === 'status') return mailStatusLabel(item.status)
  return String(item[key] || '')
}

function isMailStatusNormal(status: string) {
  return ['active', 'normal', 'ok', 'success'].includes(status.toLowerCase())
}

function mailStatusLabel(status: string) {
  return isMailStatusNormal(status) ? '正常' : '错误'
}

function mailStatusClass(status: string) {
  return isMailStatusNormal(status) ? 'badge-success' : 'badge-danger'
}

function shouldShowMailStatusReason(item: MailAccount) {
  return !isMailStatusNormal(item.status) && Boolean(item.status_reason?.trim())
}

const sortedAccounts = computed(() => {
  return filteredAccounts.value
})
const virtualMailStartIndex = computed(() => Math.max(0, Math.floor(mailVirtualScrollTop.value / mailVirtualRowHeight) - mailVirtualOverscan))
const virtualMailVisibleCount = computed(() => Math.ceil(mailVirtualViewportHeight.value / mailVirtualRowHeight) + mailVirtualOverscan * 2)
const virtualMailEndIndex = computed(() => Math.min(sortedAccounts.value.length, virtualMailStartIndex.value + virtualMailVisibleCount.value))
const virtualMailAccounts = computed(() => sortedAccounts.value.slice(virtualMailStartIndex.value, virtualMailEndIndex.value))
const virtualMailTopPadding = computed(() => virtualMailStartIndex.value * mailVirtualRowHeight)
const virtualMailBottomPadding = computed(() => Math.max(0, (sortedAccounts.value.length - virtualMailEndIndex.value) * mailVirtualRowHeight))
const displayedMailAccounts = computed(() => virtualMailAccounts.value)
const displayedMailTopPadding = computed(() => virtualMailTopPadding.value)
const displayedMailBottomPadding = computed(() => virtualMailBottomPadding.value)
const visibleMailIDs = computed(() => sortedAccounts.value.map((item) => item.id))
const selectedMailAccounts = computed(() => sortedAccounts.value.filter((item) => selectedMailIDs.value.includes(item.id)))
const allVisibleMailSelected = computed(() => visibleMailIDs.value.length > 0 && visibleMailIDs.value.every((id) => selectedMailIDs.value.includes(id)))
const pageStart = computed(() => (accountTotal.value === 0 ? 0 : (accountPage.value - 1) * pageSize.value + 1))
const pageEnd = computed(() => Math.min(accountPage.value * pageSize.value, accountTotal.value))
const accountPaginationItems = computed(() => buildPaginationItems(accountPage.value, accountPages.value))
const activeReceiveMessages = computed(() => receiveMessages[receiveFolder.value])
const filteredReceiveMessages = computed(() => {
  const keyword = receiveSearchQuery.value.trim().toLowerCase()
  if (!keyword) return activeReceiveMessages.value
  return activeReceiveMessages.value.filter((message) => {
    return [message.subject, message.to, message.from].some((value) => String(value || '').toLowerCase().includes(keyword))
  })
})
const receiveTotal = computed(() => filteredReceiveMessages.value.length)
const receiveTotalPages = computed(() => Math.max(1, Math.ceil(receiveTotal.value / receivePageSize.value)))
const receivePaginationItems = computed(() => buildPaginationItems(receivePage.value, receiveTotalPages.value))
const receiveVisibleMessages = computed(() => {
  const start = (receivePage.value - 1) * receivePageSize.value
  return filteredReceiveMessages.value.slice(start, start + receivePageSize.value)
})
const receivePageStart = computed(() => (receiveTotal.value === 0 ? 0 : (receivePage.value - 1) * receivePageSize.value + 1))
const receivePageEnd = computed(() => Math.min(receiveTotal.value, receivePage.value * receivePageSize.value))
watch(groupNameScrollMax, (max) => {
  if (groupNameScrollX.value > max) {
    groupNameScrollX.value = max
  }
})

watch(visibleGroups, () => {
  void updateGroupNameScrollMax()
}, { flush: 'post' })

watch([sortedAccounts, pageSize, searchQuery], () => {
  updateMailColumnDividers()
})

watch(searchQuery, () => {
  if (!accountAutoRefreshEnabled) return
  window.clearTimeout(accountSearchTimer)
  accountSearchTimer = window.setTimeout(() => {
    accountPage.value = 1
    loadAccounts()
  }, 300)
})

watch([receiveFolder, receivePageSize, receiveSearchQuery], () => {
  receivePage.value = 1
})

watch(receiveTotalPages, (pages) => {
  if (receivePage.value > pages) {
    receivePage.value = pages
  }
})

watch(filteredAccounts, () => {
  const validIDs = new Set(filteredAccounts.value.map((item) => item.id))
  selectedMailIDs.value = selectedMailIDs.value.filter((id) => validIDs.has(id))
})

function currentMailAccountCacheGroupID() {
  return activeGroupID.value && activeGroupID.value !== 1 ? activeGroupID.value : 0
}

function currentMailAccountPageCacheKey() {
  return mailAccountPageCacheKey({
    group_id: currentMailAccountCacheGroupID(),
    search: searchQuery.value.trim(),
    page: accountPage.value,
    page_size: pageSize.value,
    sort_by: mailSortKey.value,
    sort_order: mailSortOrder.value,
  })
}

function rememberCurrentMailAccountPage() {
  const normal = accounts.value.filter((item) => ['active', 'normal', 'ok', 'success'].includes(String(item.status || '').toLowerCase())).length
  accountPageCache.value = rememberMailAccountPage(
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
      group_id: currentMailAccountCacheGroupID(),
      search: searchQuery.value.trim(),
      page: accountPage.value,
      page_size: pageSize.value,
      sort_by: mailSortKey.value,
      sort_order: mailSortOrder.value,
    }
  )
}

function applyMailAccountPageCacheEntry(entry: MailAccountPageCacheEntry) {
  accounts.value = entry.items
  accountTotal.value = Number(entry.total) || 0
  accountPages.value = Number(entry.pages) || 0
  accountPage.value = Number(entry.page) || accountPage.value
  pageSize.value = Number(entry.page_size) || pageSize.value
}

function restoreMailManagementCache() {
  try {
    const value = JSON.parse(localStorage.getItem(mailManagementCacheKey) || 'null')
    if (!value || typeof value !== 'object') return
    if (Array.isArray(value.groups) && value.groups.length > 0) {
      groups.value = value.groups
    }
    if (Array.isArray(value.servers)) {
      servers.value = value.servers
    }
    accountPageCache.value = normalizeMailAccountPageCache(value.accountPages)
    if (value.pagination && typeof value.pagination === 'object') {
      accountPage.value = Number(value.pagination.page) || accountPage.value
      accountTotal.value = Number(value.pagination.total) || 0
      accountPages.value = Number(value.pagination.pages) || 0
      pageSize.value = Number(value.pagination.page_size) || pageSize.value
    }
    if (value.query && typeof value.query === 'object') {
      activeGroupID.value = Number(value.query.group_id) || activeGroupID.value
      searchQuery.value = String(value.query.search || '')
      if (['group', 'email', 'server', 'created_at', 'status', 'remark'].includes(value.query.sort_by)) {
        mailSortKey.value = value.query.sort_by
      }
      mailSortOrder.value = value.query.sort_order === 'desc' ? 'desc' : 'asc'
    }
    if (!groups.value.some((item) => item.id === activeGroupID.value)) {
      activeGroupID.value = groups.value[0]?.id || 0
    }
    const currentCachedPage = accountPageCache.value[currentMailAccountPageCacheKey()]
    if (currentCachedPage) {
      applyMailAccountPageCacheEntry(currentCachedPage)
    } else if (Array.isArray(value.accounts)) {
      accounts.value = value.accounts
    }
    saveMailManagementCache()
  } catch {
    // Ignore stale cache.
  }
}

function saveMailManagementCache() {
  try {
    rememberCurrentMailAccountPage()
    localStorage.setItem(
      mailManagementCacheKey,
      JSON.stringify({
        groups: groups.value,
        accounts: accounts.value,
        accountPages: accountPageCache.value,
        servers: servers.value,
        pagination: {
          page: accountPage.value,
          page_size: pageSize.value,
          total: accountTotal.value,
          pages: accountPages.value,
        },
        query: {
          group_id: activeGroupID.value,
          search: searchQuery.value,
          sort_by: mailSortKey.value,
          sort_order: mailSortOrder.value,
        },
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

async function loadGroups() {
  try {
    groups.value = await listMailGroups()
    if (groups.value.length === 0) {
      groups.value = fallbackGroups
    }
    if (!groups.value.some((item) => item.id === activeGroupID.value)) {
      activeGroupID.value = groups.value[0]?.id || 0
    }
    saveMailManagementCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取邮箱分组失败')
  }
}

async function loadServers() {
  try {
    servers.value = await listMailServers()
    const serverIDs = new Set(servers.value.map((item) => item.id))
    selectedServerIDs.value = selectedServerIDs.value.filter((id) => serverIDs.has(id))
    saveMailManagementCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取服务器失败')
  }
}

let accountRequestID = 0
let accountSearchTimer: number | undefined

function accountListGroupID() {
  return currentGroup.value?.id && currentGroup.value.id !== 1 ? currentGroup.value.id : undefined
}

const mailAccountQueryKey = computed(() => [
  'mail-accounts',
  accountListGroupID() || 0,
  searchQuery.value.trim(),
  accountPage.value,
  pageSize.value,
  mailSortKey.value,
  mailSortOrder.value,
])

async function loadAccounts() {
  const requestID = ++accountRequestID
  try {
    const queryKey = mailAccountQueryKey.value
    const params = {
      group_id: accountListGroupID(),
      search: searchQuery.value.trim(),
      page: accountPage.value,
      page_size: pageSize.value,
      sort_by: mailSortKey.value,
      sort_order: mailSortOrder.value,
    }
    const cached = queryClient.getQueryData<MailAccountListResponse>(queryKey)
    if (cached) {
      accounts.value = cached.items
      accountTotal.value = cached.total
      accountPages.value = cached.pages
      accountPage.value = cached.page
      saveMailManagementCache()
    }
    const response = await queryClient.fetchQuery({
      queryKey,
      queryFn: () => listMailAccounts(params),
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
    const accountIDs = new Set(accounts.value.map((item) => item.id))
    selectedMailIDs.value = selectedMailIDs.value.filter((id) => accountIDs.has(id))
    if (mailTableAreaRef.value) {
      mailTableAreaRef.value.scrollTop = 0
    }
    mailVirtualScrollTop.value = 0
    saveMailManagementCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取邮箱账号失败')
  }
}

function compareMailAccountRows(a: MailAccount, b: MailAccount) {
  return mailSortValue(a, mailSortKey.value).localeCompare(mailSortValue(b, mailSortKey.value), 'zh-Hans-CN', {
    numeric: true,
    sensitivity: 'base',
  }) * (mailSortOrder.value === 'asc' ? 1 : -1)
}

function mailAccountMatchesCurrentQuery(item: MailAccount) {
  const groupID = accountListGroupID()
  const keyword = searchQuery.value.trim().toLowerCase()
  const matchesGroup = mailAccountMatchesGroupFilter(item.group_id, groupID)
  const matchesSearch = !keyword || item.email.toLowerCase().includes(keyword) || item.remark.toLowerCase().includes(keyword)
  return matchesGroup && matchesSearch
}

function recalculateAccountPages() {
  accountPages.value = accountTotal.value > 0 ? Math.ceil(accountTotal.value / pageSize.value) : 0
}

function applyMailAccountSnapshot(item: MailAccount, mode: 'create' | 'update') {
  const index = accounts.value.findIndex((account) => account.id === item.id)
  const matches = mailAccountMatchesCurrentQuery(item)

  if (index >= 0) {
    if (matches) {
      const next = [...accounts.value]
      next[index] = item
      accounts.value = next.sort(compareMailAccountRows)
    } else {
      accounts.value = accounts.value.filter((account) => account.id !== item.id)
      accountTotal.value = Math.max(0, accountTotal.value - 1)
      selectedMailIDs.value = selectedMailIDs.value.filter((id) => id !== item.id)
      recalculateAccountPages()
    }
    saveMailManagementCache()
    return
  }

  if (!matches) return

  accountTotal.value += 1
  recalculateAccountPages()
  if (mode === 'create' && accounts.value.length < pageSize.value) {
    accounts.value = [...accounts.value, item].sort(compareMailAccountRows)
  }
  saveMailManagementCache()
}

function applyMailAccountDelete(id: number) {
  const existedOnPage = accounts.value.some((account) => account.id === id)
  accounts.value = accounts.value.filter((account) => account.id !== id)
  selectedMailIDs.value = selectedMailIDs.value.filter((selectedID) => selectedID !== id)
  if (existedOnPage) {
    accountTotal.value = Math.max(0, accountTotal.value - 1)
    recalculateAccountPages()
  }
  saveMailManagementCache()
}

function syncMailAccountsQuietly(refreshGroups = true) {
  void loadAccounts()
  if (refreshGroups) {
    void loadGroups()
  }
}

function currentMailAccountFilter(): AccountListFilter {
  return {
    group_id: accountListGroupID(),
    search: searchQuery.value.trim() || undefined,
  }
}

function waitForMailTask(task: BackgroundTask, onDone?: () => void, labels = { success: '成功', failed: '失败' }) {
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

function waitForMailDataTask(task: BackgroundTask, onSuccess: (latest: BackgroundTask) => Promise<void> | void, failedMessage: string) {
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

async function loadTablePageSettings() {
  try {
    const settings = await getAdminSettings()
    const defaultPageSize = Number(settings.table_default_page_size || fallbackTablePageSize)
    const nextDefaultPageSize = Number.isFinite(defaultPageSize) && defaultPageSize > 0 ? defaultPageSize : fallbackTablePageSize
    const nextPageSizeOptions = normalizePageSizeOptions(settings.table_page_size_options, nextDefaultPageSize)
    const persistedPageSize = readPersistedPageSize()

    pageSizeOptions.value = nextPageSizeOptions
    receivePageSizeOptions.value = nextPageSizeOptions
    receivePageSize.value = nextDefaultPageSize

    if (persistedPageSize > 0 && nextPageSizeOptions.includes(persistedPageSize)) {
      pageSize.value = persistedPageSize
    } else {
      pageSize.value = nextDefaultPageSize
    }
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取表格分页设置失败')
    const fallbackOptions = normalizePageSizeOptions(fallbackTablePageSizeOptions, fallbackTablePageSize)
    pageSizeOptions.value = fallbackOptions
    receivePageSizeOptions.value = fallbackOptions
    receivePageSize.value = fallbackTablePageSize
    if (!pageSize.value) {
      pageSize.value = fallbackTablePageSize
    }
  }
}

async function refreshServers() {
  if (serverRefreshing.value) return
  serverRefreshing.value = true
  try {
    await loadServers()
    resetServerForm()
    serverSearchQuery.value = ''
    selectedServerIDs.value = []
  } finally {
    serverRefreshing.value = false
  }
}

async function refreshMailManagement() {
  if (mailRefreshing.value) return
  mailRefreshing.value = true
  try {
    await Promise.all([loadGroups(), loadServers(), loadAccounts()])
    saveMailManagementCache()
  } finally {
    mailRefreshing.value = false
  }
}

function toggleMailSort(key: MailSortKey) {
  if (mailSortKey.value === key) {
    mailSortOrder.value = mailSortOrder.value === 'asc' ? 'desc' : 'asc'
  } else {
    mailSortKey.value = key
    mailSortOrder.value = 'asc'
  }
  localStorage.setItem(mailSortStorageKey, JSON.stringify({ key: mailSortKey.value, order: mailSortOrder.value }))
  accountPage.value = 1
  loadAccounts()
}

function toggleAllVisibleMail() {
  if (allVisibleMailSelected.value) {
    const visibleIDs = new Set(visibleMailIDs.value)
    selectedMailIDs.value = selectedMailIDs.value.filter((id) => !visibleIDs.has(id))
    return
  }
  selectedMailIDs.value = Array.from(new Set([...selectedMailIDs.value, ...visibleMailIDs.value]))
}

function selectAllFilteredMail() {
  selectedMailIDs.value = visibleMailIDs.value
}

function clearSelectedMail() {
  selectedMailIDs.value = []
}

function toggleGroupExpanded(group: MailGroup & { hasChildren?: boolean }) {
  if (!group.hasChildren) return
  if (expandedGroupIDs.value.includes(group.id)) {
    expandedGroupIDs.value = expandedGroupIDs.value.filter((id) => id !== group.id)
    localStorage.setItem(groupExpandedStorageKey, JSON.stringify(expandedGroupIDs.value))
    return
  }
  expandedGroupIDs.value = [...expandedGroupIDs.value, group.id]
  localStorage.setItem(groupExpandedStorageKey, JSON.stringify(expandedGroupIDs.value))
}

function selectGroup(group: MailGroup & { hasChildren?: boolean }) {
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
  toggleGroupExpanded(group)
}

function openGroupContextMenu(event: MouseEvent, group?: MailGroup) {
  event.preventDefault()
  event.stopPropagation()
  const targetGroup = group || currentGroup.value
  activeGroupID.value = targetGroup.id
  accountPage.value = 1
  loadAccounts()
  contextGroup.value = targetGroup
  groupMenuX.value = Math.min(event.clientX + 10, window.innerWidth - 190)
  groupMenuY.value = Math.min(event.clientY + 10, window.innerHeight - 190)
  groupMenuOpen.value = true
}

function closeGroupContextMenu() {
  groupMenuOpen.value = false
}

function showPendingToast() {
  appStore.showInfo('邮箱管理接口待接入')
}

function resetMailForm() {
  editingMailID.value = null
  mailForm.email = ''
  mailForm.password = ''
  mailForm.server_id = 0
  mailForm.group_id = defaultGroupID.value
  mailForm.imap_host = ''
  mailForm.smtp_host = ''
  mailForm.imap_protocol = 'IMAP'
  mailForm.imap_port = 993
  mailForm.imap_ssl = true
  mailForm.smtp_protocol = 'SMTP(SSL)'
  mailForm.smtp_port = 465
  mailForm.smtp_ssl = true
  mailForm.remark = ''
}

function fillMailForm(item: MailAccount) {
  editingMailID.value = item.id
  mailForm.email = item.email
  mailForm.password = ''
  mailForm.server_id = item.server_id
  mailForm.group_id = item.group_id || 2
  mailForm.imap_host = item.imap_host
  mailForm.smtp_host = item.smtp_host
  mailForm.imap_protocol = item.imap_protocol || 'IMAP'
  mailForm.imap_port = item.imap_port || 993
  mailForm.imap_ssl = item.imap_ssl
  mailForm.smtp_protocol = item.smtp_protocol || 'SMTP(SSL)'
  mailForm.smtp_port = item.smtp_port || 465
  mailForm.smtp_ssl = item.smtp_ssl
  mailForm.remark = item.remark || ''
}

function mailPayload(): SaveMailAccountPayload {
  return { ...mailForm }
}

function syncReceiveDefaults(form: typeof mailForm | typeof batchForm) {
  if (form.imap_protocol === 'POP3') {
    form.imap_port = form.imap_ssl ? 995 : 110
    return
  }
  form.imap_port = form.imap_ssl ? 993 : 143
}

function syncSendDefaults(form: typeof mailForm | typeof batchForm) {
  if (form.smtp_protocol === 'SMTP(STARTTLS)') {
    form.smtp_ssl = true
    form.smtp_port = 587
    return
  }
  if (form.smtp_protocol === 'SMTP') {
    form.smtp_ssl = false
    form.smtp_port = 25
    return
  }
  form.smtp_ssl = true
  form.smtp_port = 465
}

function toggleReceiveSSL(form: typeof mailForm | typeof batchForm) {
  syncReceiveDefaults(form)
}

function toggleSendSSL(form: typeof mailForm | typeof batchForm) {
  if (!form.smtp_ssl) {
    form.smtp_port = 25
    form.smtp_protocol = 'SMTP'
    return
  }
  if (form.smtp_protocol === 'SMTP(STARTTLS)') {
    form.smtp_port = 587
    return
  }
  form.smtp_port = 465
  form.smtp_protocol = 'SMTP(SSL)'
}

function resetBatchForm() {
  batchForm.content = ''
  batchForm.server_id = 0
  batchForm.group_id = defaultGroupID.value
  batchForm.imap_host = ''
  batchForm.smtp_host = ''
  batchForm.imap_protocol = 'IMAP'
  batchForm.imap_port = 993
  batchForm.imap_ssl = true
  batchForm.smtp_protocol = 'SMTP(SSL)'
  batchForm.smtp_port = 465
  batchForm.smtp_ssl = true
}

function resetServerForm() {
  editingServerID.value = null
  serverForm.name = ''
  serverForm.imap_host = ''
  serverForm.smtp_host = ''
}

function applySelectedServer(form: typeof mailForm | typeof batchForm) {
  const server = servers.value.find((item) => item.id === form.server_id)
  if (!server) return
  form.imap_host = server.imap_host
  form.smtp_host = server.smtp_host
}

async function openAddMailModal() {
  resetMailForm()
  await loadServers()
  showAddMailModal.value = true
}

async function openEditMailModal(item: MailAccount) {
  activeMailMenuID.value = null
  fillMailForm(item)
  await loadServers()
  showAddMailModal.value = true
}

async function openBatchMailModal() {
  resetBatchForm()
  await loadServers()
  showBatchMailModal.value = true
}

async function openServerManager() {
  resetServerForm()
  serverSearchQuery.value = ''
  selectedServerIDs.value = []
  await loadServers()
  showServerModal.value = true
}

function openImportMailDataModal() {
  moreActionsOpen.value = false
  importFileName.value = ''
  importFile.value = null
  importPassword.value = ''
  if (importFileInputRef.value) {
    importFileInputRef.value.value = ''
  }
  importModalOpen.value = true
}

function openExportMailDataModal() {
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

async function exportMailDataFile() {
  if (!exportPassword.value.trim()) {
    appStore.showError('请输入导出密码')
    return
  }
  const ids = [...selectedMailIDs.value]
  const filter = ids.length > 0 ? undefined : currentMailAccountFilter()
  exportingMailData.value = true
  try {
    const task = await createMailDataExportTask(ids, exportPassword.value.trim(), filter)
    exportModalOpen.value = false
    appStore.showSuccess('导出任务已创建，完成后可在任务中心下载')
    waitForMailDataTask(task, async (latest) => {
      appStore.showSuccess(latest.message || '导出完成，请在右上角任务中心下载 ZIP')
    }, '导出邮箱数据失败')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '导出邮箱数据失败')
  } finally {
    exportingMailData.value = false
  }
}

async function startImportMailData() {
  if (!importFile.value) {
    appStore.showError('请选择要导入的 ZIP 文件')
    return
  }
  if (!importPassword.value.trim()) {
    appStore.showError('请输入导入密码')
    return
  }
  importingMailData.value = true
  try {
    const task = await createMailDataImportTask(importFile.value, importPassword.value.trim())
    importModalOpen.value = false
    appStore.showSuccess('导入任务已创建，可继续使用页面')
    waitForMailDataTask(task, async (latest) => {
      await refreshMailManagement()
      appStore.showSuccess(latest.message || '导入邮箱数据完成')
    }, '导入邮箱数据失败')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '导入邮箱数据失败')
  } finally {
    importingMailData.value = false
  }
}

async function saveMailAccount() {
  if (!mailForm.group_id || !isSelectableMailGroup(mailForm.group_id)) {
    appStore.showError('请选择可添加邮箱的分组')
    return
  }
  mailSaving.value = true
  try {
    if (editingMailID.value) {
      const saved = await updateMailAccount(editingMailID.value, mailPayload())
      applyMailAccountSnapshot(saved, 'update')
      appStore.showSuccess('邮箱已更新')
    } else {
      const saved = await createMailAccount(mailPayload())
      applyMailAccountSnapshot(saved, 'create')
      appStore.showSuccess('邮箱已添加')
    }
    showAddMailModal.value = false
    resetMailForm()
    syncMailAccountsQuietly()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '添加邮箱失败')
  } finally {
    mailSaving.value = false
  }
}

async function removeMailAccount(item: MailAccount) {
  activeMailMenuID.value = null
  const confirmed = await appStore.showConfirm({
    title: '删除邮箱',
    message: `确定删除 ${item.email} 吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteMailAccount(item.id)
    applyMailAccountDelete(item.id)
    appStore.showSuccess('邮箱已删除')
    syncMailAccountsQuietly()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除邮箱失败')
  }
}

async function removeSelectedMailAccounts() {
  if (selectedMailAccounts.value.length === 0) return
  const deletingItems = [...selectedMailAccounts.value]
  const confirmed = await appStore.showConfirm({
    title: '批量删除邮箱',
    message: `确定删除选中的 ${deletingItems.length} 个邮箱吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  let successCount = 0
  for (const item of deletingItems) {
    try {
      await deleteMailAccount(item.id)
      successCount += 1
    } catch {
      // Keep deleting the remaining selected accounts.
    }
  }
  selectedMailIDs.value = []
  await loadAccounts()
  await loadGroups()
  if (successCount === deletingItems.length) {
    appStore.showSuccess(`已删除 ${successCount} 个邮箱`)
  } else {
    appStore.showError(`已删除 ${successCount} 个邮箱，${deletingItems.length - successCount} 个失败`)
  }
}

async function runMailTest(item: MailAccount, type: 'all' | 'receive' | 'send') {
  activeMailMenuID.value = null
  try {
    await testMailAccount(item.id, type)
    appStore.showSuccess('邮箱连接测试成功')
  } catch (error) {
    appStore.showError('邮箱连接测试失败')
  } finally {
    syncMailAccountsQuietly(false)
  }
}

async function testSelectedMailAccounts() {
  if (selectedMailAccounts.value.length === 0) return
  const testingItems = [...selectedMailAccounts.value]
  let successCount = 0
  for (const item of testingItems) {
    try {
      await testMailAccount(item.id, 'all')
      successCount += 1
    } catch {
      // Keep testing the remaining selected accounts.
    }
  }
  if (successCount === testingItems.length) {
    appStore.showSuccess('邮箱连接测试成功')
  } else {
    appStore.showError('邮箱连接测试失败')
  }
  await loadAccounts()
}

async function removeSelectedMailAccountsV2() {
  const ids = [...selectedMailIDs.value]
  if (ids.length === 0 && accountTotal.value === 0) return
  const scopeText = ids.length > 0 ? `选中的 ${ids.length} 个邮箱` : `当前筛选的 ${accountTotal.value} 个邮箱`
  const confirmed = await appStore.showConfirm({
    title: '批量删除邮箱',
    message: `确定删除${scopeText}吗？`,
    description: '删除任务开始后无法撤销。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  const task = await batchMailAction({ action: 'delete', ids, filter: ids.length > 0 ? undefined : currentMailAccountFilter() })
  ids.forEach((id) => applyMailAccountDelete(id))
  selectedMailIDs.value = []
  appStore.showInfo('批量删除已开始')
  waitForMailTask(task, () => syncMailAccountsQuietly())
}

async function testSelectedMailAccountsV2() {
  const ids = [...selectedMailIDs.value]
  if (ids.length === 0 && accountTotal.value === 0) return
  const task = await batchMailAction({ action: 'test', ids, filter: ids.length > 0 ? undefined : currentMailAccountFilter(), test_type: 'all' })
  appStore.showInfo('批量测试已开始')
  waitForMailTask(task, () => syncMailAccountsQuietly(false), { success: '正常', failed: '错误' })
}

function openMailSendEntry(item: MailAccount) {
  activeMailMenuID.value = null
  sendTarget.value = item
  sendForm.nickname = ''
  sendForm.recipient = ''
  sendForm.subject = ''
  sendForm.body = ''
  sendSending.value = false
  sendModalOpen.value = true
}

function getReceiveCacheStorageKey(accountID: number) {
  return `${receiveCacheStoragePrefix}${accountID}`
}

function readStoredReceiveCache(accountID: number) {
  try {
    const value = JSON.parse(localStorage.getItem(getReceiveCacheStorageKey(accountID)) || 'null')
    if (!value || typeof value !== 'object') return null
    const inbox = Array.isArray(value.inbox) ? value.inbox : []
    const trash = Array.isArray(value.trash) ? value.trash : []
    const details = value.details && typeof value.details === 'object' ? value.details as Record<string, ReceivedMailDetail> : {}
    const folder = value.folder === 'trash' ? 'trash' : 'inbox'
    return { inbox, trash, details, folder }
  } catch {
    return null
  }
}

function saveReceiveCache(accountID: number) {
  try {
    localStorage.setItem(
      getReceiveCacheStorageKey(accountID),
      JSON.stringify({
        inbox: receiveCache[accountID]?.inbox || [],
        trash: receiveCache[accountID]?.trash || [],
        details: receiveDetailCache[accountID] || {},
        folder: receiveFolder.value,
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; the in-memory cache still works for the current view.
  }
}

function clearReceiveCacheStorage() {
  Object.keys(localStorage).forEach((key) => {
    if (key.startsWith(receiveCacheStoragePrefix)) {
      localStorage.removeItem(key)
    }
  })
}

function clearReceiveSessionState() {
  receiveDetailRequestID += 1
  receiveWarmupRunID += 1
  receiveModalOpen.value = false
  receiveTarget.value = null
  receiveFolder.value = 'inbox'
  receiveSearchQuery.value = ''
  receiveLoading.value = false
  receiveDetailLoading.value = false
  receivePage.value = 1
  receivePageJump.value = ''
  selectedReceiveMessage.value = null
  receiveMessages.inbox = []
  receiveMessages.trash = []
  Object.keys(receiveCache).forEach((key) => {
    delete receiveCache[Number(key)]
  })
  Object.keys(receiveDetailCache).forEach((key) => {
    delete receiveDetailCache[Number(key)]
  })
  Object.keys(receiveDetailPending).forEach((key) => {
    delete receiveDetailPending[Number(key)]
  })
  clearReceiveCacheStorage()
}

function openMailReceiveModal(item: MailAccount) {
  activeMailMenuID.value = null
  receiveDetailRequestID += 1
  receiveWarmupRunID += 1
  receiveTarget.value = item
  receiveLimit.value = 5
  receiveSearchQuery.value = ''
  receivePage.value = 1
  receiveDetailLoading.value = false
  selectedReceiveMessage.value = null
  const stored = readStoredReceiveCache(item.id)
  if (stored) {
    receiveCache[item.id] = { inbox: stored.inbox, trash: stored.trash }
    receiveDetailCache[item.id] = stored.details
  }
  const cached = receiveCache[item.id]
  receiveMessages.inbox = cached?.inbox || []
  receiveMessages.trash = cached?.trash || []
  receiveFolder.value = stored?.folder || 'inbox'
  receiveModalOpen.value = true
}

async function copyMailAddress(email: string) {
  try {
    await copyToClipboard(email)
    appStore.showSuccess('邮箱已复制')
  } catch {
    appStore.showError('复制失败')
  }
}

async function fetchReceiveMessages() {
  if (!receiveTarget.value || receiveLoading.value) return
  receiveDetailRequestID += 1
  receiveWarmupRunID += 1
  receiveLoading.value = true
  receiveDetailLoading.value = false
  selectedReceiveMessage.value = null
  try {
    const result = await receiveMailMessages(receiveTarget.value.id, Number(receiveLimit.value) || 5)
    receiveMessages.inbox = result.inbox || []
    receiveMessages.trash = result.trash || []
    receiveCache[receiveTarget.value.id] = {
      inbox: receiveMessages.inbox,
      trash: receiveMessages.trash,
    }
    receiveFolder.value = 'inbox'
    receiveSearchQuery.value = ''
    receivePage.value = 1
    saveReceiveCache(receiveTarget.value.id)
    appStore.showSuccess('收取邮件成功')
    startReceiveDetailWarmup(receiveTarget.value.id, receiveMessages.inbox, receiveMessages.trash)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '收取邮件失败')
  } finally {
    receiveLoading.value = false
  }
}

async function sendMailMessage() {
  if (!sendTarget.value || sendSending.value) return
  if (!sendForm.recipient.trim()) {
    appStore.showError('请填写收件人')
    return
  }
  sendSending.value = true
  try {
    await sendMailAccountMessage(sendTarget.value.id, {
      nickname: sendForm.nickname,
      recipient: sendForm.recipient,
      subject: sendForm.subject,
      body: sendForm.body,
    })
    appStore.showSuccess('发送成功')
    sendModalOpen.value = false
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '发送失败')
  } finally {
    sendSending.value = false
  }
}

function openMailRemarkModal(item: MailAccount) {
  activeMailMenuID.value = null
  remarkTarget.value = item
  remarkText.value = item.remark || ''
  remarkModalOpen.value = true
}

async function saveMailRemark() {
  if (!remarkTarget.value) return
  remarkSaving.value = true
  const item = remarkTarget.value
  try {
    const saved = await updateMailAccount(item.id, {
      email: item.email,
      password: '',
      group_id: item.group_id,
      server_id: item.server_id,
      imap_host: item.imap_host,
      smtp_host: item.smtp_host,
      imap_protocol: item.imap_protocol,
      imap_port: item.imap_port,
      imap_ssl: item.imap_ssl,
      smtp_protocol: item.smtp_protocol,
      smtp_port: item.smtp_port,
      smtp_ssl: item.smtp_ssl,
      remark: remarkText.value,
    })
    applyMailAccountSnapshot(saved, 'update')
    appStore.showSuccess('备注已更新')
    remarkModalOpen.value = false
    remarkTarget.value = null
    syncMailAccountsQuietly(false)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '备注保存失败')
  } finally {
    remarkSaving.value = false
  }
}

function toggleMailMenu(event: MouseEvent, item: MailAccount) {
  event.stopPropagation()
  if (activeMailMenuID.value === item.id) {
    activeMailMenuID.value = null
    return
  }
  const trigger = event.currentTarget as HTMLElement
  const rect = trigger.getBoundingClientRect()
  const menuWidth = 144
  const menuHeight = 132
  const nextX = Math.max(8, Math.min(rect.right - menuWidth, window.innerWidth - menuWidth - 8))
  const downY = rect.bottom + 8
  const nextY = downY + menuHeight > window.innerHeight ? Math.max(8, rect.top - menuHeight - 8) : downY
  mailMenuX.value = nextX
  mailMenuY.value = nextY
  activeMailMenuID.value = item.id
}

async function saveBatchMailAccounts() {
  if (!batchForm.group_id || !isSelectableMailGroup(batchForm.group_id)) {
    appStore.showError('请选择可添加邮箱的分组')
    return
  }
  batchSaving.value = true
  try {
    const created = await batchCreateMailAccounts({ ...batchForm })
    appStore.showSuccess(`已添加 ${created.length} 个邮箱`)
    showBatchMailModal.value = false
    await loadAccounts()
    await loadGroups()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '批量添加失败')
  } finally {
    batchSaving.value = false
  }
}

function editServer(server: MailServer) {
  editingServerID.value = server.id
  serverForm.name = server.name
  serverForm.imap_host = server.imap_host
  serverForm.smtp_host = server.smtp_host
}

async function saveServer() {
  serverSaving.value = true
  try {
    if (editingServerID.value) {
      await updateMailServer(editingServerID.value, { ...serverForm })
      appStore.showSuccess('服务器已更新')
    } else {
      await createMailServer({ ...serverForm })
      appStore.showSuccess('服务器已添加')
    }
    resetServerForm()
    await loadServers()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存服务器失败')
  } finally {
    serverSaving.value = false
  }
}

async function removeServer(server: MailServer) {
  const confirmed = await appStore.showConfirm({
    title: '删除服务器',
    message: `确定删除服务器 ${server.name} 吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteMailServer(server.id)
    appStore.showSuccess('服务器已删除')
    selectedServerIDs.value = selectedServerIDs.value.filter((id) => id !== server.id)
    await loadServers()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除服务器失败')
  }
}

async function removeSelectedServers() {
  if (selectedServerIDs.value.length === 0) return
  const deletingIDs = [...selectedServerIDs.value]
  const confirmed = await appStore.showConfirm({
    title: '批量删除服务器',
    message: `确定删除选中的 ${deletingIDs.length} 个服务器吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    for (const id of deletingIDs) {
      await deleteMailServer(id)
    }
    appStore.showSuccess(`已删除 ${deletingIDs.length} 个服务器`)
    selectedServerIDs.value = []
    await loadServers()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '批量删除服务器失败')
    await loadServers()
  }
}

function canManageGroup(group: MailGroup | null) {
  return Boolean(group && !group.system)
}

function canAddChildGroup(group: MailGroup | null) {
  return Boolean(canManageGroup(group) && group?.parent_id === 0 && Number(group.count) === 0)
}

function openGroupModal(mode: 'create' | 'createChild' | 'edit') {
  closeGroupContextMenu()
  groupModalMode.value = mode
  groupName.value = mode === 'edit' ? contextGroup.value?.name || '' : ''
  groupSortOrder.value = mode === 'edit' && contextGroup.value ? normalizeGroupSortInput(groupSortOrderValue(contextGroup.value), sameParentCustomGroups(contextGroup.value.parent_id).length) : 1
  showGroupModal.value = true
}

async function saveGroup() {
  if (!groupName.value.trim()) {
    appStore.showError('分组名称不能为空')
    return
  }

  groupSaving.value = true
  try {
    if (groupModalMode.value === 'edit' && contextGroup.value) {
      const sortOrder = normalizeGroupSortInput(groupSortOrder.value, groupSortOrderMax.value)
      groupSortOrder.value = sortOrder
      await updateMailGroup(contextGroup.value.id, { name: groupName.value.trim(), sort_order: sortOrder })
      appStore.showSuccess('分组已更新')
    } else {
      const parentID = groupModalMode.value === 'createChild' ? contextGroup.value?.id || 0 : 0
      await createMailGroup({ name: groupName.value.trim(), parent_id: parentID })
      if (parentID > 0 && !expandedGroupIDs.value.includes(parentID)) {
        expandedGroupIDs.value = [...expandedGroupIDs.value, parentID]
      }
      appStore.showSuccess('分组已创建')
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
  closeGroupContextMenu()
  const confirmed = await appStore.showConfirm({
    title: '删除分组',
    message: `确定要删除分组 ${group.name} 吗？`,
    description: '如果分组下有子分组或邮箱账号，将无法删除。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteMailGroup(group.id)
    appStore.showSuccess('分组已删除')
    await loadGroups()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除分组失败')
  }
}

function selectPageSize(size: number) {
  pageSize.value = size
  localStorage.setItem(pageSizeStorageKey, String(size))
  pageSizeDropdownOpen.value = false
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

function selectReceivePageSize(size: number) {
  receivePageSize.value = size
  receivePage.value = 1
  receivePageSizeDropdownOpen.value = false
}

function changeReceivePage(direction: -1 | 1) {
  receivePage.value = Math.min(receiveTotalPages.value, Math.max(1, receivePage.value + direction))
}

function setReceivePage(page: number) {
  receivePage.value = Math.min(receiveTotalPages.value, Math.max(1, page))
}

function jumpToReceivePage() {
  const page = Number(receivePageJump.value)
  if (!Number.isFinite(page)) return
  setReceivePage(page)
  receivePageJump.value = ''
}

function setReceiveFolder(folder: 'inbox' | 'trash') {
  receiveFolder.value = folder
  receivePage.value = 1
  selectedReceiveMessage.value = null
  if (receiveTarget.value) saveReceiveCache(receiveTarget.value.id)
}

function receiveMessageKey(message: Pick<ReceivedMailMessage, 'folder' | 'mailbox' | 'uid'>) {
  return `${message.folder}:${message.mailbox}:${message.uid}`
}

function cacheReceiveDetail(accountID: number, fallback: ReceivedMailMessage, detail: ReceivedMailDetail) {
  const detailMap = receiveDetailCache[accountID] || {}
  receiveDetailCache[accountID] = detailMap
  const key = receiveMessageKey(fallback)
  const mergedDetail = { ...fallback, ...detail, folder: fallback.folder, mailbox: fallback.mailbox, uid: fallback.uid }
  detailMap[key] = mergedDetail
  saveReceiveCache(accountID)
  return mergedDetail
}

function loadReceiveDetail(accountID: number, message: ReceivedMailMessage) {
  const pendingMap = receiveDetailPending[accountID] || {}
  receiveDetailPending[accountID] = pendingMap
  const key = receiveMessageKey(message)
  if (!pendingMap[key]) {
    pendingMap[key] = receiveMailDetail(accountID, message)
      .then((detail) => cacheReceiveDetail(accountID, message, detail))
      .catch(() => null)
      .finally(() => {
        delete pendingMap[key]
      })
  }
  return pendingMap[key]
}

async function warmReceiveDetails(accountID: number, messages: ReceivedMailMessage[], runID: number) {
  const detailMap = receiveDetailCache[accountID] || {}
  receiveDetailCache[accountID] = detailMap
  const pendingMessages = messages.filter((message) => message.uid > 0 && !detailMap[receiveMessageKey(message)])

  for (const message of pendingMessages) {
    if (runID !== receiveWarmupRunID) return
    await loadReceiveDetail(accountID, message)
  }
}

function startReceiveDetailWarmup(accountID: number, inboxItems: ReceivedMailMessage[], trashItems: ReceivedMailMessage[]) {
  receiveWarmupRunID += 1
  const runID = receiveWarmupRunID
  void warmReceiveDetails(accountID, [...inboxItems, ...trashItems], runID)
}

async function openReceiveDetail(message: ReceivedMailMessage) {
  if (!receiveTarget.value || message.uid <= 0) return
  const key = receiveMessageKey(message)
  const cachedDetail = receiveDetailCache[receiveTarget.value.id]?.[key]
  if (cachedDetail) {
    receiveDetailRequestID += 1
    receiveDetailLoading.value = false
    selectedReceiveMessage.value = cachedDetail
    return
  }

  const requestID = receiveDetailRequestID + 1
  receiveDetailRequestID = requestID
  receiveDetailLoading.value = true
  try {
    const detail = await loadReceiveDetail(receiveTarget.value.id, message)
    if (requestID !== receiveDetailRequestID) return
    if (!detail) throw new Error('读取邮件失败')
    selectedReceiveMessage.value = detail
  } catch (error) {
    if (requestID === receiveDetailRequestID) {
      appStore.showError(error instanceof Error ? error.message : '读取邮件失败')
    }
  } finally {
    if (requestID === receiveDetailRequestID) {
      receiveDetailLoading.value = false
    }
  }
}

function closeReceiveDetail() {
  receiveDetailRequestID += 1
  receiveDetailLoading.value = false
  selectedReceiveMessage.value = null
}

function syncGroupNameScroll(event: Event) {
  groupNameScrollX.value = (event.currentTarget as HTMLElement).scrollLeft
}

async function updateGroupNameScrollMax() {
  await nextTick()
  const list = mailGroupListRef.value
  if (!list) {
    groupNameScrollMax.value = 0
    groupNameScrollX.value = 0
    return
  }

  const max = Array.from(list.querySelectorAll<HTMLElement>('.mail-group-name-viewport')).reduce((currentMax, viewport) => {
    const inner = viewport.firstElementChild as HTMLElement | null
    if (!inner) return currentMax
    return Math.max(currentMax, Math.ceil(inner.scrollWidth - viewport.clientWidth))
  }, 0)

  groupNameScrollMax.value = Math.max(0, max)
  if (groupNameScrollX.value > groupNameScrollMax.value) {
    groupNameScrollX.value = groupNameScrollMax.value
  }
}

async function updateMailColumnDividers() {
  await nextTick()
  const area = mailTableAreaRef.value
  if (!area) {
    mailColumnDividerLefts.value = []
    return
  }
  mailVirtualScrollTop.value = area.scrollTop
  mailVirtualViewportHeight.value = area.clientHeight || mailVirtualViewportHeight.value
  const areaRect = area.getBoundingClientRect()
  const headers = Array.from(area.querySelectorAll<HTMLTableCellElement>('thead th'))
  mailColumnDividerLefts.value = headers.slice(0, -1).map((header) => {
    const rect = header.getBoundingClientRect()
    return Math.round(rect.right - areaRect.left + area.scrollLeft)
  })
}

function handleClickOutside(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (target.closest('[data-group-context-menu]')) {
    return
  }
  if (!target.closest('[data-page-size-select]')) {
    pageSizeDropdownOpen.value = false
  }
  if (!target.closest('[data-receive-page-size-select]')) {
    receivePageSizeDropdownOpen.value = false
  }
  if (!target.closest('[data-mail-more-actions]')) {
    moreActionsOpen.value = false
  }
  if (!target.closest('[data-mail-row-menu]')) {
    activeMailMenuID.value = null
  }
  closeGroupContextMenu()
}

onMounted(async () => {
  restoreMailManagementCache()
  await loadTablePageSettings()
  accountAutoRefreshEnabled = true
  refreshMailManagement()
  updateMailColumnDividers()
  updateGroupNameScrollMax()
  window.addEventListener('resize', updateMailColumnDividers)
  window.addEventListener('resize', updateGroupNameScrollMax)
  window.addEventListener(authSessionClearedEvent, clearReceiveSessionState)
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  window.clearTimeout(accountSearchTimer)
  window.removeEventListener('resize', updateMailColumnDividers)
  window.removeEventListener('resize', updateGroupNameScrollMax)
  window.removeEventListener(authSessionClearedEvent, clearReceiveSessionState)
  document.removeEventListener('click', handleClickOutside)
})
</script>

<template>
  <div class="mail-page-layout min-h-[calc(100vh-8rem)] gap-3">
    <aside class="mail-group-panel shrink-0 rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/50" @contextmenu="openGroupContextMenu">
      <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div>
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">邮箱分组</h2>
        </div>
      </div>

      <div class="mail-group-list-wrap">
        <div ref="mailGroupListRef" class="mail-group-list space-y-1 p-3">
          <button
            v-for="group in visibleGroups"
            :key="group.id"
            class="mail-group-item flex w-full select-none items-center justify-between rounded-xl px-3 py-2.5 text-left text-sm transition-colors"
            :class="activeGroupID === group.id
              ? 'bg-primary-50 text-primary-700 dark:bg-dark-700 dark:text-primary-300'
              : 'text-gray-600 hover:bg-gray-50 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-700/70 dark:hover:text-white'"
            :style="{ paddingLeft: `${12 + group.level * 18}px` }"
            type="button"
            @click="selectGroup(group)"
            @dblclick.stop.prevent
            @contextmenu.stop="openGroupContextMenu($event, group)"
          >
            <span class="mail-group-name-viewport">
              <span class="mail-group-name-inner" :style="{ transform: `translateX(-${groupNameScrollX}px)` }">
                <span class="flex h-4 w-4 shrink-0 items-center justify-center" @click.stop="toggleGroupExpanded(group)">
                  <ChevronDown v-if="group.hasChildren && expandedGroupIDs.includes(group.id)" class="h-4 w-4" />
                  <ChevronRight v-else-if="group.hasChildren" class="h-4 w-4" />
                  <Folder v-else class="h-4 w-4" />
                </span>
                <span class="mail-group-name" :title="group.name">{{ group.name }}</span>
              </span>
            </span>
            <span v-if="!group.hasChildren" class="mail-group-count rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-900 dark:text-dark-400">{{ mailGroupCount(group) }}</span>
            <span v-else class="mail-group-count-placeholder"></span>
          </button>
        </div>
        <div v-if="groupNameScrollMax > 0" class="mail-group-horizontal-scroll">
          <div class="mail-group-horizontal-scroll-body" @scroll="syncGroupNameScroll">
            <div :style="{ width: `calc(100% + ${groupNameScrollMax}px)` }"></div>
          </div>
        </div>
      </div>

      <Teleport to="body">
        <div
          v-if="groupMenuOpen"
          data-group-context-menu
          class="group-context-menu w-44 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 shadow-xl shadow-black/10 dark:border-dark-600 dark:bg-dark-800 dark:shadow-black/30"
          :style="{ left: `${groupMenuX}px`, top: `${groupMenuY}px` }"
          @click.stop
          @contextmenu.prevent.stop
        >
          <button class="context-menu-item" type="button" @click.stop="openGroupModal('create')">
            <Plus class="h-4 w-4" />
            <span>添加分组</span>
          </button>
          <button class="context-menu-item" type="button" :disabled="!canAddChildGroup(contextGroup)" @click.stop="openGroupModal('createChild')">
            <FolderPlus class="h-4 w-4" />
            <span>添加子分组</span>
          </button>
          <button class="context-menu-item" type="button" :disabled="!canManageGroup(contextGroup)" @click.stop="openGroupModal('edit')">
            <Pencil class="h-4 w-4" />
            <span>编辑分组</span>
          </button>
          <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
          <button class="context-menu-item text-red-600 dark:text-red-400" type="button" :disabled="!canManageGroup(contextGroup)" @click.stop="openDeleteGroupDialog">
            <Trash2 class="h-4 w-4" />
            <span>删除分组</span>
          </button>
        </div>
      </Teleport>
    </aside>

    <section class="mail-account-panel min-w-0 flex-1 overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/50">
      <div class="mail-account-toolbar flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div class="flex flex-wrap items-center gap-2">
          <button class="mail-action-primary" type="button" @click="openAddMailModal">
            <Plus class="h-4 w-4" />
            添加邮箱
          </button>
          <button class="mail-action-secondary" type="button" @click="openBatchMailModal">
            <Upload class="h-4 w-4" />
            批量添加邮箱
          </button>
          <button class="mail-action-tertiary" type="button" @click="openServerManager">
            <Server class="h-4 w-4" />
            添加服务器地址
          </button>
          <div class="relative" data-mail-more-actions>
            <button class="mail-action-more" type="button" @click.stop="moreActionsOpen = !moreActionsOpen">
              <MoreHorizontal class="h-4 w-4" />
              <span>更多操作</span>
              <ChevronDown class="h-4 w-4 transition-transform" :class="{ 'rotate-180': moreActionsOpen }" />
            </button>
            <div v-if="moreActionsOpen" class="mail-more-menu" @click.stop>
              <div class="mail-more-menu-label">数据操作</div>
              <button class="mail-more-menu-item" type="button" @click="openImportMailDataModal">
                <span class="mail-more-menu-icon import"><Upload class="h-4 w-4" /></span>
                <span>导入</span>
              </button>
              <button class="mail-more-menu-item" type="button" @click="openExportMailDataModal">
                <span class="mail-more-menu-icon export"><Download class="h-4 w-4" /></span>
                <span>{{ selectedMailIDs.length > 0 ? '导出选中' : '导出' }}</span>
                <span v-if="selectedMailIDs.length > 0" class="mail-more-selected-badge">已选 {{ selectedMailIDs.length }}</span>
              </button>
            </div>
          </div>
          <button class="mail-action-refresh" type="button" title="刷新" :disabled="mailRefreshing" @click="refreshMailManagement">
            <RefreshCw class="h-4 w-4" :class="{ 'mail-refresh-icon-spinning': mailRefreshing }" />
            刷新
          </button>
          <button v-if="selectedMailIDs.length > 0" class="mail-toolbar-batch-button" type="button" @click="testSelectedMailAccountsV2">
            <Play class="h-4 w-4" />
            批量测试（{{ selectedMailIDs.length }}）
          </button>
          <button v-if="selectedMailIDs.length > 0" class="mail-toolbar-batch-danger" type="button" @click="removeSelectedMailAccountsV2">
            <Trash2 class="h-4 w-4" />
            批量删除（{{ selectedMailIDs.length }}）
          </button>
        </div>
        <div class="search-clear-field relative max-w-full" style="width: min(350px, 100%); flex: 0 0 min(350px, 100%);">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
          <input v-model.trim="searchQuery" class="input search-clear-input h-9 pl-10 text-sm" type="text" placeholder="搜索邮箱或备注" />
          <button v-if="searchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="searchQuery = ''">
            <X class="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <div class="mail-account-body flex-1">
        <div ref="mailTableAreaRef" class="mail-table-area relative overflow-x-auto" @scroll="updateMailColumnDividers">
          <div class="mail-column-divider-layer" aria-hidden="true">
            <span
              v-for="(left, index) in mailColumnDividerLefts"
              :key="index"
              class="mail-column-divider"
              :style="{ left: `${left}px` }"
            ></span>
          </div>
          <table class="mail-account-table text-sm">
            <colgroup>
              <col class="mail-col-select" />
              <col class="mail-col-group" />
              <col class="mail-col-email" />
              <col class="mail-col-server" />
              <col class="mail-col-created" />
              <col class="mail-col-status" />
              <col class="mail-col-remark" />
              <col class="mail-col-actions" />
            </colgroup>
            <thead class="bg-gray-50 text-center text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
              <tr>
                <th class="mail-select-col px-5 py-3 font-medium">
                  <input :checked="allVisibleMailSelected" type="checkbox" @change="toggleAllVisibleMail" />
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleMailSort('group')"><span class="mail-sort-label">分组</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': mailSortKey === 'group' && mailSortOrder === 'asc' }" /></button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleMailSort('email')"><span class="mail-sort-label">邮箱</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': mailSortKey === 'email' && mailSortOrder === 'asc' }" /></button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleMailSort('server')"><span class="mail-sort-label">服务器（收/发）</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': mailSortKey === 'server' && mailSortOrder === 'asc' }" /></button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleMailSort('created_at')"><span class="mail-sort-label">添加时间</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': mailSortKey === 'created_at' && mailSortOrder === 'asc' }" /></button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleMailSort('status')"><span class="mail-sort-label">状态</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': mailSortKey === 'status' && mailSortOrder === 'asc' }" /></button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleMailSort('remark')"><span class="mail-sort-label">备注</span><ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': mailSortKey === 'remark' && mailSortOrder === 'asc' }" /></button>
                </th>
                <th class="sticky-col sticky-col-right px-5 py-3 text-center font-medium">操作</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
              <tr v-if="displayedMailTopPadding > 0" aria-hidden="true">
                <td colspan="8" :style="{ height: `${displayedMailTopPadding}px`, padding: 0, border: 0 }"></td>
              </tr>
              <tr v-for="item in displayedMailAccounts" :key="item.id" class="mail-virtual-row hover:bg-gray-50 dark:hover:bg-dark-800">
                <td class="mail-select-col px-5 py-4">
                  <input v-model="selectedMailIDs" :value="item.id" type="checkbox" />
                </td>
                <td class="px-5 py-4 text-gray-600 dark:text-gray-300" :title="item.group_name">{{ item.group_name }}</td>
                <td class="px-5 py-4 font-medium text-gray-900 dark:text-white" :title="item.email">
                  <div class="mail-email-cell">
                    <button class="mail-email-link" type="button" @click="openMailReceiveModal(item)">{{ item.email }}</button>
                    <button class="mail-email-copy-button" type="button" title="复制邮箱" @click.stop="copyMailAddress(item.email)">
                      <Copy class="h-3.5 w-3.5" />
                    </button>
                  </div>
                </td>
                <td class="px-5 py-4 text-gray-500 dark:text-dark-400">
                  <div class="mail-server-lines" :title="`收：${item.imap_host || '-'} / 发：${item.smtp_host || '-'}`">
                    <div>收：{{ item.imap_host || '-' }}</div>
                    <div>发：{{ item.smtp_host || '-' }}</div>
                  </div>
                </td>
                <td class="px-5 py-4 text-gray-500 dark:text-dark-400">{{ item.created_at }}</td>
                <td class="px-5 py-4">
                  <div class="mail-status-cell">
                    <span class="badge" :class="mailStatusClass(item.status)">
                      {{ mailStatusLabel(item.status) }}
                    </span>
                    <span v-if="shouldShowMailStatusReason(item)" class="mail-status-reason" tabindex="0" :aria-label="item.status_reason">
                      <CircleHelp class="h-3.5 w-3.5" />
                      <span class="mail-status-tooltip">{{ item.status_reason }}</span>
                    </span>
                  </div>
                </td>
                <td class="px-5 py-4 text-gray-500 dark:text-dark-400">{{ item.remark || '' }}</td>
                <td class="sticky-col sticky-col-right px-5 py-4 text-center">
                  <div class="mail-row-actions text-gray-500 dark:text-dark-400">
                    <button class="mail-row-action-button hover:text-primary-600 dark:hover:text-primary-300" type="button" @click="openEditMailModal(item)">
                      <Pencil class="h-4 w-4" />
                      <span>编辑</span>
                    </button>
                    <button class="mail-row-action-button hover:text-emerald-600 dark:hover:text-emerald-300" type="button" @click="runMailTest(item, 'all')">
                      <Play class="h-4 w-4" />
                      <span>测试</span>
                    </button>
                    <button class="mail-row-action-button hover:text-red-600 dark:hover:text-red-400" type="button" @click="removeMailAccount(item)">
                      <Trash2 class="h-4 w-4" />
                      <span>删除</span>
                    </button>
                    <div data-mail-row-menu>
                      <button class="mail-row-action-button hover:text-gray-900 dark:hover:text-white" type="button" @click.stop="toggleMailMenu($event, item)">
                        <MoreHorizontal class="h-4 w-4" />
                        <span>更多</span>
                      </button>
                    </div>
                  </div>
                </td>
              </tr>
              <tr v-if="displayedMailBottomPadding > 0" aria-hidden="true">
                <td colspan="8" :style="{ height: `${displayedMailBottomPadding}px`, padding: 0, border: 0 }"></td>
              </tr>
            </tbody>
          </table>
          <div v-if="sortedAccounts.length === 0" class="mail-empty-state p-8 text-center text-sm font-semibold text-gray-500 dark:text-dark-400">
            暂无邮箱账号
          </div>
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
            <div class="page-size-select relative w-20" data-page-size-select>
              <button
                class="page-size-trigger"
                type="button"
                @click.stop="pageSizeDropdownOpen = !pageSizeDropdownOpen"
              >
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

    <div v-if="showGroupModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div class="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-dark-700 dark:bg-dark-900">
        <h3 class="text-lg font-bold text-gray-900 dark:text-white">{{ groupModalTitle }}</h3>
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
          <input v-model.trim="groupName" class="input" type="text" placeholder="请输入分组名称" @keyup.enter="saveGroup" />
        </label>
        <div class="mt-6 flex justify-end gap-2">
          <button class="btn btn-secondary" type="button" @click="showGroupModal = false">取消</button>
          <button class="btn btn-primary" type="button" :disabled="groupSaving" @click="saveGroup">
            {{ groupSaving ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>

    <Teleport to="body">
      <div v-if="showAddMailModal" class="mail-modal-mask center-mail-modal">
        <div class="mail-form-modal mail-account-form-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
        <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-dark-700">
          <h3 class="text-base font-bold text-gray-900 dark:text-white">{{ editingMailID ? '编辑邮箱' : '添加邮箱' }}</h3>
          <button class="modal-close-button" type="button" @click="showAddMailModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <div class="mail-modal-body mail-modal-scroll-body grid gap-3 p-5 md:grid-cols-2">
          <label><span class="input-label">邮箱账号 *</span><input v-model.trim="mailForm.email" class="input" placeholder="例: user@example.com" /></label>
          <label><span class="input-label">邮箱密码 *</span><input v-model="mailForm.password" class="input" type="password" :placeholder="editingMailID ? '留空则不修改' : '邮箱密码或授权码'" /></label>
          <label>
            <span class="input-label">选择服务器</span>
            <select v-model.number="mailForm.server_id" class="input" @change="applySelectedServer(mailForm)">
              <option :value="0">选择已有服务器或手动输入</option>
              <option v-for="server in serverOptions" :key="server.id" :value="server.id">{{ server.name }}</option>
            </select>
          </label>
          <label><span class="input-label">收件服务器地址 *</span><input v-model.trim="mailForm.imap_host" class="input" placeholder="例: imap.qq.com" /></label>
          <label>
            <span class="input-label">分组</span>
            <select v-model.number="mailForm.group_id" class="input">
              <option v-if="selectableGroups.length === 0" :value="0" disabled>暂无可添加邮箱的分组</option>
              <option v-if="selectableGroups.length > 0" :value="0">请选择分组</option>
              <option v-for="group in selectableGroups" :key="group.id" :value="group.id">{{ groupDisplayName(group) }}</option>
            </select>
          </label>
          <label><span class="input-label">发件服务器地址 *</span><input v-model.trim="mailForm.smtp_host" class="input" placeholder="例: smtp.qq.com" /></label>
          <label><span class="input-label">收件协议 *</span><select v-model="mailForm.imap_protocol" class="input" @change="syncReceiveDefaults(mailForm)"><option>IMAP</option><option>POP3</option></select></label>
          <div class="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3">
            <label><span class="input-label">收件端口 *</span><input v-model.number="mailForm.imap_port" class="input" type="number" /></label>
            <label class="mb-3 flex items-center gap-2 text-xs text-gray-700 dark:text-dark-300"><input v-model="mailForm.imap_ssl" type="checkbox" @change="toggleReceiveSSL(mailForm)" />收件启用SSL</label>
          </div>
          <label><span class="input-label">发件协议 *</span><select v-model="mailForm.smtp_protocol" class="input" @change="syncSendDefaults(mailForm)"><option>SMTP(SSL)</option><option>SMTP(STARTTLS)</option><option>SMTP</option></select></label>
          <div class="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3">
            <label><span class="input-label">发件端口 *</span><input v-model.number="mailForm.smtp_port" class="input" type="number" /></label>
            <label class="mb-3 flex items-center gap-2 text-xs text-gray-700 dark:text-dark-300"><input v-model="mailForm.smtp_ssl" type="checkbox" @change="toggleSendSSL(mailForm)" />发件启用SSL</label>
          </div>
          <label class="md:col-span-2"><span class="input-label">备注</span><textarea v-model.trim="mailForm.remark" class="input mail-remark-textarea" placeholder="输入备注信息"></textarea></label>
        </div>
        <div class="shrink-0 flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700">
          <button class="btn btn-secondary" type="button" @click="showAddMailModal = false">取消</button>
          <button class="btn btn-primary" type="button" :disabled="mailSaving" @click="saveMailAccount">{{ mailSaving ? '保存中...' : (editingMailID ? '保存' : '添加') }}</button>
        </div>
      </div>
      </div>

      <div v-if="showBatchMailModal" class="mail-modal-mask center-mail-modal">
        <div class="mail-form-modal mail-account-form-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
        <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-dark-700">
          <h3 class="text-base font-bold text-gray-900 dark:text-white">批量添加邮箱</h3>
          <button class="modal-close-button" type="button" @click="showBatchMailModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <div class="mail-modal-body mail-modal-scroll-body grid gap-3 p-5 md:grid-cols-2">
          <label class="md:col-span-2">
            <span class="input-label">批量邮箱内容 *</span>
            <textarea v-model.trim="batchForm.content" class="input batch-mail-content-textarea font-mono" placeholder="格式：账号----密码（每行一个）&#10;例如：&#10;user1@example.com----password1&#10;user2@example.com----password2"></textarea>
          </label>
          <label>
            <span class="input-label">选择服务器</span>
            <select v-model.number="batchForm.server_id" class="input" @change="applySelectedServer(batchForm)">
              <option :value="0">选择已有服务器或手动输入</option>
              <option v-for="server in serverOptions" :key="server.id" :value="server.id">{{ server.name }}</option>
            </select>
          </label>
          <label><span class="input-label">收件服务器地址 *</span><input v-model.trim="batchForm.imap_host" class="input" placeholder="例: imap.qq.com" /></label>
          <label>
            <span class="input-label">分组</span>
            <select v-model.number="batchForm.group_id" class="input">
              <option v-if="selectableGroups.length === 0" :value="0" disabled>暂无可添加邮箱的分组</option>
              <option v-if="selectableGroups.length > 0" :value="0">请选择分组</option>
              <option v-for="group in selectableGroups" :key="group.id" :value="group.id">{{ groupDisplayName(group) }}</option>
            </select>
          </label>
          <label><span class="input-label">发件服务器地址 *</span><input v-model.trim="batchForm.smtp_host" class="input" placeholder="例: smtp.qq.com" /></label>
          <label><span class="input-label">收件协议 *</span><select v-model="batchForm.imap_protocol" class="input" @change="syncReceiveDefaults(batchForm)"><option>IMAP</option><option>POP3</option></select></label>
          <div class="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3">
            <label><span class="input-label">收件端口 *</span><input v-model.number="batchForm.imap_port" class="input" type="number" /></label>
            <label class="mb-3 flex items-center gap-2 text-xs text-gray-700 dark:text-dark-300"><input v-model="batchForm.imap_ssl" type="checkbox" @change="toggleReceiveSSL(batchForm)" />收件启用SSL</label>
          </div>
          <label><span class="input-label">发件协议 *</span><select v-model="batchForm.smtp_protocol" class="input" @change="syncSendDefaults(batchForm)"><option>SMTP(SSL)</option><option>SMTP(STARTTLS)</option><option>SMTP</option></select></label>
          <div class="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3">
            <label><span class="input-label">发件端口 *</span><input v-model.number="batchForm.smtp_port" class="input" type="number" /></label>
            <label class="mb-3 flex items-center gap-2 text-xs text-gray-700 dark:text-dark-300"><input v-model="batchForm.smtp_ssl" type="checkbox" @change="toggleSendSSL(batchForm)" />发件启用SSL</label>
          </div>
        </div>
        <div class="shrink-0 flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700">
          <button class="btn btn-secondary" type="button" @click="showBatchMailModal = false">取消</button>
          <button class="btn btn-primary" type="button" :disabled="batchSaving" @click="saveBatchMailAccounts">{{ batchSaving ? '添加中...' : '确认' }}</button>
        </div>
      </div>
      </div>

      <div v-if="exportModalOpen" class="mail-modal-mask center-mail-modal">
        <div class="mail-import-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold text-gray-900 dark:text-white">导出数据</h3>
            <button class="modal-close-button" type="button" @click="exportModalOpen = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-modal-scroll-body p-6">
            <p class="text-sm text-gray-600 dark:text-dark-300">导出加密 ZIP 文件，里面包含邮箱账号、密码/授权码和全部分组。</p>
            <div class="mail-import-warning">请妥善保存导出密码。导入或解压这个 ZIP 文件时必须输入同一个密码。</div>
            <label class="mt-5 block">
              <span class="input-label">ZIP 密码 *</span>
              <input v-model="exportPassword" class="input" type="password" autocomplete="new-password" placeholder="请输入导出 ZIP 密码" @keyup.enter="exportMailDataFile" />
            </label>
          </div>
          <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="exportModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="exportingMailData || !exportPassword.trim()" @click="exportMailDataFile">{{ exportingMailData ? '导出中...' : '开始导出' }}</button>
          </div>
        </div>
      </div>

      <div v-if="importModalOpen" class="mail-modal-mask center-mail-modal">
        <div class="mail-import-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold text-gray-900 dark:text-white">导入数据</h3>
            <button class="modal-close-button" type="button" @click="importModalOpen = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-modal-scroll-body p-6">
            <p class="text-sm text-gray-600 dark:text-dark-300">上传导出的加密 ZIP 文件以批量导入账号与分组。</p>
            <div class="mail-import-warning">导入文件包含邮箱密码/授权码；同一分组内重复邮箱会覆盖，不同分组的同邮箱会保留为独立记录。</div>
            <label class="mt-5 block">
              <span class="input-label">数据文件</span>
              <div class="mail-import-file-box">
                <div class="min-w-0">
                  <div class="truncate text-sm font-bold text-gray-800 dark:text-dark-100">{{ importFileName || '请选择数据文件' }}</div>
                  <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">ZIP (.zip)</div>
                </div>
                <button class="mail-import-file-button" type="button" @click="chooseImportFile">选择文件</button>
                <input ref="importFileInputRef" class="hidden" type="file" accept=".zip,application/zip,application/x-zip-compressed" @change="handleImportFileChange" />
              </div>
            </label>
            <label class="mt-5 block">
              <span class="input-label">ZIP 密码 *</span>
              <input v-model="importPassword" class="input" type="password" autocomplete="current-password" placeholder="请输入导出时设置的 ZIP 密码" @keyup.enter="startImportMailData" />
            </label>
          </div>
          <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="importModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="importingMailData || !importFile || !importPassword.trim()" @click="startImportMailData">{{ importingMailData ? '导入中...' : '开始导入' }}</button>
          </div>
        </div>
      </div>

      <div v-if="showServerModal" class="mail-modal-mask server-modal-mask">
        <div class="mail-form-modal simple-server-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold text-gray-900 dark:text-white">服务器地址管理</h3>
            <button class="modal-close-button" type="button" @click="showServerModal = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-modal-scroll-body server-modal-body p-5">
            <div class="grid gap-4 md:grid-cols-3">
              <label>
                <span class="input-label">服务器名称 *</span>
                <input v-model.trim="serverForm.name" class="input" placeholder="例: QQ邮箱" />
              </label>
              <label>
                <span class="input-label">收件服务器地址 *</span>
                <input v-model.trim="serverForm.imap_host" class="input" placeholder="例: imap.qq.com" />
              </label>
              <label>
                <span class="input-label">发件服务器地址 *</span>
                <input v-model.trim="serverForm.smtp_host" class="input" placeholder="例: smtp.qq.com" />
              </label>
            </div>
            <div class="server-search-action-row">
              <div class="server-search-tools">
                <label class="server-search-field">
                  <span class="input-label">搜索服务器名称</span>
                  <div class="search-clear-field">
                    <input v-model.trim="serverSearchQuery" class="input search-clear-input" placeholder="快速搜索已添加的服务器" />
                    <button v-if="serverSearchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="serverSearchQuery = ''">
                      <X class="h-3.5 w-3.5" />
                    </button>
                  </div>
                </label>
                <button class="server-refresh-button" type="button" title="刷新服务器列表" :disabled="serverRefreshing" @click="refreshServers">
                  <RefreshCw class="h-4 w-4" :class="{ 'mail-refresh-icon-spinning': serverRefreshing }" />
                  刷新
                </button>
              </div>
              <button class="btn btn-primary" type="button" :disabled="serverSaving" @click="saveServer">{{ editingServerID ? '保存服务器' : '添加服务器' }}</button>
            </div>
            <div class="server-list-section">
              <div class="mb-3 flex items-center gap-3">
                <h4 class="text-base font-semibold text-gray-900 dark:text-white">已添加的服务器</h4>
                <button
                  v-if="selectedServerIDs.length > 0"
                  class="server-batch-delete-button"
                  type="button"
                  @click="removeSelectedServers"
                >
                  批量删除
                </button>
              </div>
              <div class="server-list rounded-xl border border-gray-200 dark:border-dark-700">
                <div v-if="servers.length === 0" class="p-5 text-center text-sm text-gray-500 dark:text-dark-400">暂无服务器</div>
                <div v-else-if="filteredServers.length === 0" class="p-5 text-center text-sm text-gray-500 dark:text-dark-400">没有匹配的服务器</div>
                <div v-for="server in filteredServers" :key="server.id" class="server-list-row">
                  <input v-model="selectedServerIDs" :value="server.id" type="checkbox" />
                  <div class="server-list-cell min-w-0 flex-1">
                    <span>服务器名称</span>
                    <strong class="truncate">{{ server.name }}</strong>
                  </div>
                  <div class="server-list-cell min-w-0 flex-1">
                    <span>收件</span>
                    <strong class="truncate">{{ server.imap_host }}</strong>
                  </div>
                  <div class="server-list-cell min-w-0 flex-1">
                    <span>发件</span>
                    <strong class="truncate">{{ server.smtp_host }}</strong>
                  </div>
                  <div class="flex gap-2">
                    <button class="server-edit-button" type="button" @click="editServer(server)">编辑</button>
                    <button class="rounded-lg bg-red-500 px-3 py-1.5 text-xs font-semibold text-white" type="button" @click="removeServer(server)">删除</button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div
        v-if="activeMailMenuID && activeMailMenuItem"
        data-mail-row-menu
        class="mail-row-menu fixed w-36 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 text-left shadow-xl dark:border-dark-600 dark:bg-dark-800"
        :style="{ left: `${mailMenuX}px`, top: `${mailMenuY}px` }"
        @click.stop
      >
        <button class="mail-row-menu-item" type="button" @click="openMailReceiveModal(activeMailMenuItem)">
          <Inbox class="h-4 w-4" />
          <span>收件</span>
        </button>
        <button class="mail-row-menu-item" type="button" @click="openMailSendEntry(activeMailMenuItem)">
          <Send class="h-4 w-4" />
          <span>发件</span>
        </button>
        <button class="mail-row-menu-item" type="button" @click="openMailRemarkModal(activeMailMenuItem)">
          <StickyNote class="h-4 w-4" />
          <span>备注</span>
        </button>
      </div>

      <div v-if="receiveModalOpen && receiveTarget" class="mail-modal-mask center-mail-modal">
        <div class="mail-receive-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-base font-bold text-gray-900 dark:text-white">收件</h3>
            <label v-if="!selectedReceiveMessage" class="mail-receive-search search-clear-field">
              <Search class="h-4 w-4 text-gray-400 dark:text-dark-400" />
              <input v-model.trim="receiveSearchQuery" class="search-clear-input" type="search" placeholder="搜索标题 / 收件人 / 发件人" />
              <button v-if="receiveSearchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="receiveSearchQuery = ''">
                <X class="h-3.5 w-3.5" />
              </button>
            </label>
            <button class="modal-close-button" type="button" @click="receiveModalOpen = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-receive-body">
            <aside class="mail-receive-sidebar">
              <div class="text-xs text-gray-500 dark:text-dark-400">当前邮箱</div>
              <div class="mt-2 break-all text-sm font-bold leading-6 text-gray-900 dark:text-white">{{ receiveTarget.email }}</div>
              <label class="mt-5 block">
                <span class="input-label">收取封数</span>
                <input v-model.number="receiveLimit" class="input" min="1" max="100" type="number" />
              </label>
              <button class="mt-3 w-full rounded-lg bg-primary-600 px-4 py-2.5 text-sm font-bold text-white hover:bg-primary-500 disabled:cursor-not-allowed disabled:opacity-60" type="button" :disabled="receiveLoading" @click="fetchReceiveMessages">{{ receiveLoading ? '收取中...' : '收取邮件' }}</button>
              <div class="mt-3 grid gap-2">
                <button class="mail-folder-button" :class="{ active: receiveFolder === 'inbox' }" type="button" @click="setReceiveFolder('inbox')">
                  <Inbox class="h-4 w-4" />
                  <span>收件箱</span>
                </button>
                <button class="mail-folder-button" :class="{ active: receiveFolder === 'trash' }" type="button" @click="setReceiveFolder('trash')">
                  <Trash2 class="h-4 w-4" />
                  <span>垃圾箱</span>
                </button>
              </div>
            </aside>
            <section v-if="!selectedReceiveMessage" class="mail-receive-list">
              <table class="w-full table-fixed text-left text-sm">
                <thead class="bg-gray-50 text-gray-700 dark:bg-dark-800 dark:text-dark-200">
                  <tr>
                    <th class="mail-receive-col-subject px-3 py-3 font-bold">标题</th>
                    <th class="mail-receive-col-address px-3 py-3 font-bold">收件人</th>
                    <th class="mail-receive-col-address px-3 py-3 font-bold">发件人</th>
                    <th class="mail-receive-col-time px-3 py-3 font-bold">时间</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="message in receiveVisibleMessages" :key="`${message.folder}-${message.uid}-${message.timestamp}`" class="mail-message-row" @click="openReceiveDetail(message)">
                    <td class="truncate px-3 py-4 text-gray-700 dark:text-dark-200" :title="message.subject">{{ message.subject || '无标题' }}</td>
                    <td class="truncate px-3 py-4 text-gray-500 dark:text-dark-400" :title="mailContactEmails(message.to)">{{ mailContactEmails(message.to) || '-' }}</td>
                    <td class="truncate px-3 py-4 text-gray-500 dark:text-dark-400" :title="mailContactEmails(message.from)">{{ mailContactEmails(message.from) || '-' }}</td>
                    <td class="whitespace-nowrap px-3 py-4 text-gray-500 dark:text-dark-400" :title="message.time">{{ message.time || '-' }}</td>
                  </tr>
                  <tr v-if="receiveVisibleMessages.length === 0">
                    <td class="px-3 py-4 text-gray-400 dark:text-dark-400" colspan="4">暂无邮件</td>
                  </tr>
                </tbody>
              </table>
            </section>
            <section v-else class="mail-detail-panel">
              <div class="mail-detail-sticky">
                <button class="mail-detail-back" type="button" @click="closeReceiveDetail">返回列表</button>
                <h4 class="mt-3 text-base font-bold text-gray-900 dark:text-white">{{ selectedReceiveMessage.subject || '无标题' }}</h4>
                <div class="mt-4 grid gap-2 text-sm text-gray-500 dark:text-dark-300">
                  <div>发件人：{{ mailContactDetail(selectedReceiveMessage.from) || '-' }}</div>
                  <div>收件人：{{ mailContactDetail(selectedReceiveMessage.to) || '-' }}</div>
                  <div>时间：{{ selectedReceiveMessage.time || '-' }}</div>
                  <div>所属：{{ selectedReceiveMessage.folder === 'trash' ? '垃圾箱' : '收件箱' }}</div>
                </div>
              </div>
              <div v-if="receiveDetailLoading" class="mt-5 text-sm text-gray-400 dark:text-dark-400">读取中...</div>
              <SafeMailFrame
                v-else
                class="mail-detail-content"
                :html="selectedReceiveMessage.html"
                :text="selectedReceiveMessage.body || selectedReceiveMessage.body_preview"
                :title="selectedReceiveMessage.subject || '邮件正文'"
              />
            </section>
          </div>
          <div v-if="!selectedReceiveMessage" class="mail-receive-footer">
            <PaginationBar
              :page="receivePage"
              :pages="receiveTotalPages"
              :page-size="receivePageSize"
              :page-size-options="receivePageSizeOptions"
              :total="receiveTotal"
              @page-change="setReceivePage"
              @page-size-change="selectReceivePageSize"
            />
            <div v-if="false" class="mail-receive-footer-left">
              <span>显示 {{ receivePageStart }} 至 {{ receivePageEnd }} 共 {{ receiveTotal }} 条结果</span>
              <span>每页:</span>
              <div class="page-size-select relative w-20" data-receive-page-size-select>
                <button class="page-size-trigger" type="button" @click.stop="receivePageSizeDropdownOpen = !receivePageSizeDropdownOpen">
                  <span>{{ receivePageSize }}</span>
                  <ChevronDown class="h-4 w-4 transition-transform" :class="{ 'rotate-180': receivePageSizeDropdownOpen }" />
                </button>
                <div v-if="receivePageSizeDropdownOpen" class="page-size-menu">
                  <button v-for="size in receivePageSizeOptions" :key="size" class="page-size-option" :class="{ 'page-size-option-active': size === receivePageSize }" type="button" @click="selectReceivePageSize(size)">
                    <span>{{ size }}</span>
                    <Check v-if="size === receivePageSize" class="h-4 w-4" />
                  </button>
                </div>
              </div>
            </div>
            <div v-if="false" class="receive-page-numbers">
              <button class="receive-page-square receive-page-arrow receive-page-prev" type="button" :disabled="receivePage <= 1" @click="changeReceivePage(-1)">‹</button>
              <template v-for="item in receivePaginationItems" :key="item.key">
                <span v-if="item.type === 'ellipsis'" class="receive-page-ellipsis">...</span>
                <button v-else class="receive-page-square receive-page-number" :class="{ active: item.page === receivePage }" type="button" @click="setReceivePage(item.page)">{{ item.page }}</button>
              </template>
              <button class="receive-page-square receive-page-arrow receive-page-next" type="button" :disabled="receivePage >= receiveTotalPages" @click="changeReceivePage(1)">›</button>
              <form class="page-jump-form" @submit.prevent="jumpToReceivePage">
                <input
                  v-model.trim="receivePageJump"
                  class="page-jump-input"
                  type="text"
                  inputmode="numeric"
                  pattern="[0-9]*"
                  min="1"
                  :max="receiveTotalPages"
                  :placeholder="String(receivePage)"
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

      <div v-if="sendModalOpen && sendTarget" class="mail-modal-mask center-mail-modal">
        <div class="mail-send-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <h3 class="text-base font-bold text-gray-900 dark:text-white">发送邮件</h3>
            <button class="modal-close-button" type="button" @click="sendModalOpen = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-modal-scroll-body grid gap-4 p-6">
            <label>
              <span class="input-label">昵称</span>
              <input v-model.trim="sendForm.nickname" class="input" placeholder="收件人看到的发件人昵称（可选）" />
            </label>
            <label>
              <span class="input-label">收件人 *</span>
              <input v-model.trim="sendForm.recipient" class="input" placeholder="例: target@example.com" />
            </label>
            <label>
              <span class="input-label">主题</span>
              <input v-model.trim="sendForm.subject" class="input" placeholder="邮件主题" />
            </label>
            <label>
              <span class="input-label">正文</span>
              <textarea v-model="sendForm.body" class="input mail-send-body-textarea" placeholder="邮件正文"></textarea>
            </label>
          </div>
          <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="sendModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="sendSending" @click="sendMailMessage">
              {{ sendSending ? '发送中...' : '发送' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="remarkModalOpen" class="mail-modal-mask center-mail-modal">
        <div class="remark-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-700">
            <h3 class="text-lg font-bold text-gray-900 dark:text-white">编辑备注</h3>
            <button class="modal-close-button" type="button" @click="remarkModalOpen = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-modal-scroll-body p-5">
            <label class="block">
              <span class="input-label">备注</span>
              <textarea v-model.trim="remarkText" class="input mail-remark-edit-textarea" placeholder="请输入备注"></textarea>
            </label>
          </div>
          <div class="shrink-0 flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" @click="remarkModalOpen = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="remarkSaving" @click="saveMailRemark">
              {{ remarkSaving ? '保存中...' : '保存' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.mail-page-layout {
  display: flex;
  align-items: stretch;
  min-width: 0;
  width: 100%;
}

.mail-group-panel {
  display: flex;
  flex-direction: column;
  width: 224px;
  min-height: calc(100vh - 8rem);
  max-height: calc(100vh - 8rem);
  overflow: hidden;
}

.mail-group-panel > div:first-child {
  padding: 0.8rem 1rem;
}

.mail-group-panel h2 {
  font-size: 0.95rem;
}

.mail-group-list-wrap {
  min-height: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.mail-group-list {
  min-height: 0;
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
}

.mail-group-list::-webkit-scrollbar {
  width: 0.55rem;
  height: 0.55rem;
}

.mail-group-list::-webkit-scrollbar-track {
  background: transparent;
}

.mail-group-list::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.55);
}

.dark .mail-group-list::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.75);
}

.mail-group-item {
  -webkit-tap-highlight-color: transparent;
  min-height: 2.15rem;
  padding-top: 0.45rem !important;
  padding-bottom: 0.45rem !important;
  font-size: 0.8125rem;
}

.mail-group-name-viewport {
  display: block;
  min-width: 0;
  flex: 1;
  overflow: hidden;
}

.mail-group-name-inner {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 0.5rem;
  transition: transform 0.06s linear;
  will-change: transform;
}

.mail-group-name {
  display: block;
  flex: 0 0 auto;
  min-width: 0;
  overflow: visible;
  white-space: nowrap;
  word-break: keep-all;
  line-height: 1.15;
}

.mail-group-count {
  flex-shrink: 0;
  min-width: 1.35rem;
  margin-left: 0.75rem;
  text-align: center;
}

.mail-group-count-placeholder {
  flex-shrink: 0;
  width: 1.35rem;
  margin-left: 0.75rem;
}

.mail-group-horizontal-scroll {
  flex-shrink: 0;
  padding: 0 0.75rem 0.35rem;
}

.mail-group-horizontal-scroll-body {
  width: 100%;
  height: 0.75rem;
  overflow-x: auto;
  overflow-y: hidden;
}

.mail-group-horizontal-scroll-body > div {
  height: 1px;
}

.mail-group-horizontal-scroll-body::-webkit-scrollbar {
  height: 0.55rem;
}

.mail-group-horizontal-scroll-body::-webkit-scrollbar-track {
  background: transparent;
}

.mail-group-horizontal-scroll-body::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.55);
}

.dark .mail-group-horizontal-scroll-body::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.75);
}

.mail-group-item:focus {
  outline: none;
}

.mail-group-item:active {
  transform: none;
}

.dark .mail-group-item:hover,
.dark .mail-group-item:focus-visible {
  background: rgb(51 65 85 / 0.7) !important;
  color: rgb(255 255 255) !important;
}

.dark .mail-group-item.bg-primary-50,
.dark .mail-group-item.dark\:bg-dark-700 {
  background: rgb(51 65 85) !important;
}

.mail-account-panel {
  display: flex;
  min-width: 0;
  max-width: 100%;
  flex: 1 1 0;
  flex-direction: column;
  min-height: calc(100vh - 8rem);
}

.mail-account-toolbar {
  padding: 0.75rem 1rem;
}

.mail-account-body {
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.mail-table-area {
  --mail-col-select: 3.1rem;
  --mail-col-group: 14rem;
  --mail-col-email: 19rem;
  --mail-col-server: 12rem;
  --mail-col-created: 10rem;
  --mail-col-status: 5.2rem;
  --mail-col-remark: 15.5rem;
  --mail-col-actions: 170px;
  --mail-table-min-width: calc(
    var(--mail-col-select) +
    var(--mail-col-group) +
    var(--mail-col-email) +
    var(--mail-col-server) +
    var(--mail-col-created) +
    var(--mail-col-status) +
    var(--mail-col-remark) +
    var(--mail-col-actions)
  );
  --mail-table-divider: rgb(148 163 184 / 0.08);
  flex: 1;
  min-height: 100%;
  min-width: 0;
  width: 100%;
  max-width: 100%;
  max-height: min(62vh, 720px);
  overflow-y: auto;
}

.mail-virtual-row {
  height: 74px;
}

.dark .mail-table-area {
  --mail-table-divider: rgb(148 163 184 / 0.12);
}

.mail-empty-state {
  pointer-events: none;
}

.mail-action-primary,
.mail-action-secondary,
.mail-action-tertiary,
.mail-action-more,
.mail-action-refresh {
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

.mail-action-primary {
  background: linear-gradient(135deg, rgb(20 184 166), rgb(13 148 136));
  color: white;
  box-shadow: 0 12px 22px rgb(20 184 166 / 0.22);
}

.mail-action-secondary {
  border: 1px solid rgb(20 184 166 / 0.35);
  background: rgb(240 253 250);
  color: rgb(15 118 110);
}

.mail-action-tertiary {
  border: 1px solid rgb(148 163 184 / 0.45);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
}

.mail-action-more {
  border: 1px solid rgb(148 163 184 / 0.55);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
  min-width: 8.5rem;
}

.mail-action-refresh {
  border: 1px solid rgb(148 163 184 / 0.45);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
}

.mail-action-primary:hover,
.mail-action-secondary:hover,
.mail-action-tertiary:hover,
.mail-action-more:hover,
.mail-action-refresh:hover {
  transform: translateY(-1px);
}

.mail-action-refresh:disabled {
  cursor: wait;
  opacity: 0.72;
}

.mail-action-refresh:disabled:hover {
  transform: none;
}

.mail-refresh-icon-spinning {
  animation: mail-refresh-spin 0.8s linear infinite;
}

@keyframes mail-refresh-spin {
  to {
    transform: rotate(360deg);
  }
}

.dark .mail-action-secondary {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: rgb(94 234 212);
}

.dark .mail-action-tertiary {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

html.dark .mail-action-more {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.dark .mail-action-refresh {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.mail-more-menu {
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

.mail-more-menu-label {
  margin-bottom: 0.35rem;
  padding: 0 0.25rem;
  font-size: 0.75rem;
  color: rgb(100 116 139);
}

.mail-more-menu-item {
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

.mail-more-menu-item:hover {
  background: rgb(241 245 249);
  color: rgb(15 23 42);
}

.mail-more-menu-icon {
  display: inline-flex;
  height: 2rem;
  width: 2rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.45rem;
}

.mail-more-menu-icon.import {
  background: rgb(204 251 241);
  color: rgb(15 118 110);
}

.mail-more-menu-icon.export {
  background: rgb(237 233 254);
  color: rgb(109 40 217);
}

.mail-more-selected-badge {
  margin-left: auto;
  border-radius: 999px;
  background: rgb(204 251 241);
  padding: 0.15rem 0.45rem;
  font-size: 0.7rem;
  font-weight: 800;
  color: rgb(15 118 110);
}

html.dark .mail-more-menu {
  border-color: rgb(71 85 105 / 0.55);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
  box-shadow: 0 18px 38px rgb(2 6 23 / 0.3);
}

html.dark .mail-more-menu-label {
  color: rgb(148 163 184);
}

html.dark .mail-more-menu-item {
  color: rgb(226 232 240);
}

html.dark .mail-more-menu-item:hover {
  background: rgb(51 65 85 / 0.72);
  color: white;
}

html.dark .mail-more-menu-icon.import {
  background: rgb(20 184 166 / 0.18);
  color: rgb(94 234 212);
}

html.dark .mail-more-menu-icon.export {
  background: rgb(124 58 237 / 0.2);
  color: rgb(167 139 250);
}

html.dark .mail-more-selected-badge {
  background: rgb(20 184 166 / 0.18);
  color: rgb(94 234 212);
}

.mail-account-table {
  table-layout: fixed;
  width: max(100%, var(--mail-table-min-width));
  min-width: var(--mail-table-min-width);
  border-collapse: separate;
  border-spacing: 0;
  font-size: 0.8125rem;
}

.mail-account-table th {
  border-right: 1px solid var(--mail-table-divider);
}

.mail-account-table th,
.mail-account-table td {
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.65rem 0.8rem !important;
}

.dark .mail-account-table th,
.dark .mail-account-table td {
  border-bottom-color: rgb(51 65 85);
}

.mail-col-select {
  width: var(--mail-col-select);
}

.mail-col-group {
  width: var(--mail-col-group);
}

.mail-col-email {
  width: var(--mail-col-email);
}

.mail-col-server {
  width: var(--mail-col-server);
}

.mail-col-created {
  width: var(--mail-col-created);
}

.mail-col-status {
  width: var(--mail-col-status);
}

.mail-col-remark {
  width: var(--mail-col-remark);
}

.mail-col-actions {
  width: var(--mail-col-actions);
}

.mail-column-divider-layer {
  display: none;
}

.mail-column-divider {
  position: absolute;
  top: 0;
  bottom: 0;
  width: 1px;
  background: var(--mail-table-divider);
}

.mail-account-table th:last-child,
.mail-account-table td:last-child {
  border-right: 0;
}

.mail-select-col {
  width: var(--mail-col-select) !important;
  text-align: center;
}

.mail-select-col input {
  height: 0.95rem;
  width: 0.95rem;
  accent-color: rgb(20 184 166);
}

.mail-email-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.35rem;
}

.mail-email-link {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
  color: inherit;
  font: inherit;
  transition: color 0.15s ease;
}

.mail-email-link:hover {
  color: rgb(20 184 166);
  text-decoration: underline;
  text-underline-offset: 3px;
}

.mail-email-copy-button {
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

.mail-email-copy-button:hover {
  background: rgb(20 184 166 / 0.12);
  color: rgb(20 184 166);
}

.mail-account-table th:nth-child(2),
.mail-account-table td:nth-child(2) {
  width: var(--mail-col-group);
}

.mail-account-table th:nth-child(3),
.mail-account-table td:nth-child(3) {
  width: var(--mail-col-email);
}

.mail-account-table th:nth-child(4),
.mail-account-table td:nth-child(4) {
  width: var(--mail-col-server);
}

.mail-account-table th:nth-child(5),
.mail-account-table td:nth-child(5) {
  width: var(--mail-col-created);
}

.mail-account-table th:nth-child(6),
.mail-account-table td:nth-child(6) {
  width: var(--mail-col-status);
  padding-left: 0.35rem !important;
  padding-right: 0.35rem !important;
  text-align: center;
}

.mail-status-cell {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  position: relative;
  width: 100%;
}

.mail-account-table td:nth-child(6) .badge {
  white-space: nowrap;
}

.mail-status-reason {
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

.mail-status-reason:hover,
.mail-status-reason:focus-visible {
  color: rgb(239 68 68);
}

.mail-status-tooltip {
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

.mail-status-tooltip::before {
  content: '';
  position: absolute;
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 0.35rem solid transparent;
  border-bottom-color: rgb(15 23 42);
}

.mail-status-reason:hover .mail-status-tooltip,
.mail-status-reason:focus-visible .mail-status-tooltip {
  opacity: 1;
  transform: translateX(-50%) translateY(0.1rem);
}

.mail-account-table th:nth-child(7),
.mail-account-table td:nth-child(7) {
  width: var(--mail-col-remark);
}

.mail-account-table td:nth-child(2),
.mail-account-table td:nth-child(3),
.mail-account-table td:nth-child(4),
.mail-account-table td:nth-child(5),
.mail-account-table td:nth-child(7) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mail-server-lines {
  display: grid;
  gap: 0.2rem;
  min-width: 0;
  line-height: 1.45;
}

.mail-server-lines > div {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mail-account-table thead th {
  position: sticky;
  top: 0;
  z-index: 15;
  background: rgb(249 250 251);
  white-space: nowrap;
}

.dark .mail-account-table thead th {
  background: rgb(30 41 59);
}

.mail-account-table thead .sticky-col-right {
  z-index: 25;
}

.mail-sort-button {
  display: inline-flex;
  width: 100%;
  min-width: max-content;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
  color: inherit;
  transition: color 0.15s ease;
}

.mail-sort-button::before {
  content: '';
  width: 0.875rem;
  flex: 0 0 0.875rem;
}

.mail-sort-button > svg {
  flex: 0 0 0.875rem;
}

.mail-account-table th:nth-child(6) .mail-sort-button {
  min-width: 0;
  gap: 0.15rem;
}

.mail-account-table th:nth-child(6) .mail-sort-button::before {
  display: none;
}

.mail-account-table th:nth-child(6) .mail-sort-button > svg {
  margin-right: -0.25rem;
}

.mail-sort-label {
  flex: 0 0 auto;
  white-space: nowrap;
}

.mail-sort-button:hover {
  color: rgb(20 184 166);
}

.mail-toolbar-batch-button,
.mail-toolbar-batch-danger {
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
  box-shadow: 0 8px 18px rgb(15 23 42 / 0.12);
  transition: transform 0.15s ease, background-color 0.15s ease;
}

.mail-toolbar-batch-button {
  background: rgb(37 99 235);
  box-shadow: 0 10px 20px rgb(37 99 235 / 0.18);
}

.mail-toolbar-batch-button:hover {
  background: rgb(29 78 216);
}

.mail-toolbar-batch-danger {
  background: rgb(239 68 68);
}

.mail-toolbar-batch-danger:hover {
  background: rgb(220 38 38);
}

.mail-toolbar-batch-button:hover,
.mail-toolbar-batch-danger:hover {
  transform: translateY(-1px);
}

:global(.mail-modal-mask) {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
  overflow: auto;
  background: rgb(0 0 0 / 0.45);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
}

:global(.server-modal-mask) {
  align-items: center;
}

:global(.center-mail-modal) {
  overflow: hidden;
}

:global(.mail-form-modal) {
  width: min(34rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
  overflow: hidden;
}

:global(.mail-account-form-modal) {
  width: min(42rem, calc(100vw - 2rem));
}

:global(.mail-receive-modal) {
  width: min(100rem, calc(100vw - 1rem));
  height: min(48rem, calc(100vh - 1rem));
  max-height: calc(100vh - 1rem);
}

:global(.mail-send-modal) {
  width: min(37.5rem, calc(100vw - 1rem));
  max-height: calc(100vh - 1rem);
}

:global(.mail-import-modal) {
  width: min(32rem, calc(100vw - 1rem));
  max-height: calc(100vh - 1rem);
}

:global(.scrollable-mail-modal) {
  display: flex;
  flex-direction: column;
}

:global(.mail-modal-scroll-body) {
  min-height: 0;
  overflow-y: auto;
}

:global(.mail-modal-scroll-body::-webkit-scrollbar) {
  width: 0.55rem;
}

:global(.mail-modal-scroll-body::-webkit-scrollbar-track) {
  border-radius: 999px;
  background: rgb(226 232 240 / 0.8);
}

:global(.mail-modal-scroll-body::-webkit-scrollbar-thumb) {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.85);
}

:global(.dark .mail-modal-scroll-body::-webkit-scrollbar-track) {
  background: rgb(15 23 42 / 0.75);
}

:global(.dark .mail-modal-scroll-body::-webkit-scrollbar-thumb) {
  background: rgb(71 85 105 / 0.95);
}

:global(.simple-server-modal) {
  width: min(64rem, calc(100vw - 2rem));
  height: min(44rem, calc(100vh - 2rem));
  max-height: calc(100vh - 2rem);
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

:global(.dark .modal-close-button) {
  background: transparent;
  color: rgb(203 213 225);
}

:global(.dark .modal-close-button:hover) {
  background: rgb(51 65 85 / 0.9);
  color: white;
}

:global(.mail-modal-body) {
  font-size: 0.8125rem;
}

:global(.mail-modal-body .input-label) {
  margin-bottom: 0.35rem;
  font-size: 0.8125rem;
}

:global(.mail-modal-body .input) {
  min-height: 2.25rem;
  border-radius: 0.625rem;
  padding: 0.5rem 0.75rem;
  font-size: 0.8125rem;
}

:global(.mail-modal-body textarea.input) {
  line-height: 1.45;
}

:global(.mail-remark-textarea) {
  min-height: 5rem !important;
}

:global(.batch-mail-content-textarea) {
  min-height: 11rem !important;
}

.mail-import-warning {
  margin-top: 1.25rem;
  border: 1px solid rgb(249 115 22 / 0.85);
  border-radius: 0.55rem;
  background: rgb(249 115 22 / 0.08);
  padding: 0.75rem 0.85rem;
  font-size: 0.8rem;
  font-weight: 800;
  color: rgb(234 88 12);
}

.dark .mail-import-warning {
  background: rgb(249 115 22 / 0.12);
  color: rgb(251 191 36);
}

.mail-import-file-box {
  margin-top: 0.45rem;
  display: flex;
  min-height: 4.25rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border: 1px dashed rgb(148 163 184 / 0.75);
  border-radius: 0.65rem;
  padding: 0.95rem;
}

.dark .mail-import-file-box {
  border-color: rgb(71 85 105);
}

.mail-import-file-button {
  flex-shrink: 0;
  border: 1px solid rgb(148 163 184 / 0.6);
  border-radius: 0.75rem;
  padding: 0.65rem 1rem;
  font-size: 0.8rem;
  font-weight: 800;
  color: rgb(51 65 85);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.mail-import-file-button:hover {
  background: rgb(241 245 249);
}

.dark .mail-import-file-button {
  border-color: rgb(71 85 105);
  color: rgb(226 232 240);
}

.dark .mail-import-file-button:hover {
  background: rgb(51 65 85);
}

:global(.mail-remark-edit-textarea) {
  min-height: 8rem !important;
  resize: vertical;
}

:global(.mail-send-body-textarea) {
  min-height: 7.25rem;
  resize: vertical;
}

.mail-receive-body {
  display: grid;
  grid-template-columns: 8.75rem minmax(0, 1fr);
  gap: 1.9rem;
  min-height: 0;
  flex: 1;
  padding: 1.6rem 2.4rem 1rem 1.6rem;
}

.mail-receive-search {
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

.mail-receive-search input {
  min-width: 0;
  flex: 1;
  border: 0;
  background: transparent;
  color: rgb(15 23 42);
  font-size: 0.875rem;
  outline: none;
}

.mail-receive-search input::placeholder {
  color: rgb(148 163 184);
}

html.dark .mail-receive-search {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59 / 0.58);
  box-shadow: none;
}

html.dark .mail-receive-search input {
  color: rgb(226 232 240);
}

html.dark .mail-receive-search input::placeholder {
  color: rgb(148 163 184);
}

.mail-receive-sidebar {
  align-self: start;
  border-radius: 0.7rem;
  background: rgb(248 250 252);
  padding: 1rem 0.9rem;
}

.dark .mail-receive-sidebar {
  background: rgb(15 23 42 / 0.72);
}

.mail-folder-button {
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

.mail-folder-button.active {
  border-color: rgb(99 102 241);
  background: rgb(238 242 255);
  color: rgb(79 70 229);
}

.dark .mail-folder-button {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42);
  color: rgb(203 213 225);
}

.dark .mail-folder-button.active {
  border-color: rgb(45 212 191);
  background: rgb(20 184 166 / 0.14);
  color: rgb(94 234 212);
}

.mail-receive-list {
  min-width: 0;
  overflow: auto;
}

.mail-receive-list table {
  min-width: 58rem;
  border-collapse: collapse;
}

.mail-receive-col-subject {
  width: 42%;
}

.mail-receive-col-address {
  width: 22%;
}

.mail-receive-col-time {
  width: 14%;
  min-width: 11.5rem;
}

.mail-receive-list th,
.mail-receive-list td {
  border-bottom: 1px solid rgb(226 232 240);
}

.mail-message-row {
  cursor: pointer;
  transition: background-color 0.15s ease;
}

.mail-message-row:hover {
  background: rgb(241 245 249 / 0.8);
}

.dark .mail-message-row:hover {
  background: rgb(30 41 59 / 0.74);
}

.dark .mail-receive-list th,
.dark .mail-receive-list td {
  border-bottom-color: rgb(51 65 85);
}

.mail-detail-panel {
  min-width: 0;
  overflow: auto;
  padding-right: 0.75rem;
}

.mail-detail-sticky {
  position: sticky;
  top: 0;
  z-index: 5;
  margin-right: -0.75rem;
  padding: 0 0.75rem 0.85rem 0;
  background: white;
}

.dark .mail-detail-sticky {
  background: rgb(15 23 42);
}

.mail-detail-back {
  border-radius: 0.45rem;
  background: rgb(99 102 241);
  padding: 0.35rem 0.65rem;
  color: white;
  font-size: 0.75rem;
  font-weight: 700;
}

.mail-detail-content {
  margin-top: 1rem;
  min-height: 0;
}

.mail-detail-content :deep(a),
.mail-detail-plain :deep(a) {
  color: rgb(37 99 235);
  text-decoration: underline;
  text-underline-offset: 2px;
  overflow-wrap: anywhere;
}

.dark .mail-detail-content {
  color: inherit;
}

.dark .mail-detail-content :deep(a),
.dark .mail-detail-plain :deep(a) {
  color: rgb(94 234 212);
}

.mail-detail-plain {
  font-family: inherit;
  line-height: 1.65;
}

.mail-receive-footer {
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

.mail-receive-footer-left {
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

.dark .mail-receive-footer {
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

.mail-page-size-select {
  width: 5.5rem;
  height: 2.25rem;
  padding-top: 0;
  padding-bottom: 0;
}

:global(.remark-modal) {
  width: min(30rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
}

.server-list {
  min-height: clamp(10rem, 28vh, 16rem);
  flex: 1;
  overflow-y: scroll;
  scrollbar-gutter: stable;
}

.server-modal-body {
  box-sizing: border-box;
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 1.15rem;
  padding-bottom: 1.35rem !important;
}

.server-search-action-row {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  align-items: flex-end;
  gap: 1rem;
}

.server-search-tools {
  display: contents;
}

.server-search-field {
  width: 100%;
}

.server-refresh-button {
  display: inline-flex;
  height: 2.35rem;
  width: fit-content;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  border-radius: 0.65rem;
  border: 1px solid rgb(148 163 184 / 0.45);
  background: rgb(248 250 252);
  padding: 0 0.75rem;
  font-size: 0.8125rem;
  font-weight: 600;
  color: rgb(51 65 85);
  transition: transform 0.15s ease, background-color 0.15s ease, color 0.15s ease;
}

.server-refresh-button:hover {
  transform: translateY(-1px);
}

.server-refresh-button:disabled {
  cursor: wait;
  opacity: 0.72;
}

.server-refresh-button:disabled:hover {
  transform: none;
}

.dark .server-refresh-button {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.server-search-action-row > .btn {
  justify-self: end;
}

.server-refresh-button {
  justify-self: start;
}

.server-list-section {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  padding-top: 0.6rem;
}

.server-list::-webkit-scrollbar {
  width: 0.55rem;
}

.server-list::-webkit-scrollbar-track {
  border-radius: 999px;
  background: rgb(226 232 240 / 0.8);
}

.server-list::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.85);
}

.dark .server-list::-webkit-scrollbar-track {
  background: rgb(15 23 42 / 0.75);
}

.dark .server-list::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.95);
}

.server-edit-button {
  border-radius: 0.5rem;
  background: rgb(16 185 129);
  padding: 0.375rem 0.75rem;
  font-size: 0.75rem;
  font-weight: 700;
  color: white;
  box-shadow: 0 8px 18px rgb(16 185 129 / 0.22);
  transition: background-color 0.15s ease, box-shadow 0.15s ease;
}

.server-edit-button:hover {
  background: rgb(5 150 105);
  box-shadow: 0 10px 22px rgb(16 185 129 / 0.32);
}

.server-batch-delete-button {
  border-radius: 0.5rem;
  background: rgb(239 68 68);
  padding: 0.35rem 0.75rem;
  font-size: 0.75rem;
  font-weight: 700;
  color: white;
  box-shadow: 0 8px 18px rgb(239 68 68 / 0.2);
  transition: background-color 0.15s ease, box-shadow 0.15s ease;
}

.server-batch-delete-button:hover {
  background: rgb(220 38 38);
  box-shadow: 0 10px 22px rgb(239 68 68 / 0.3);
}

.mail-row-menu {
  z-index: 2147483647;
  pointer-events: auto;
  box-shadow: 0 18px 40px rgb(15 23 42 / 0.2);
}

.mail-row-actions {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
}

.mail-row-action-button {
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

.mail-row-action-button span {
  display: block;
  white-space: nowrap;
  word-break: keep-all;
  writing-mode: horizontal-tb;
  font-size: 0.6875rem;
  line-height: 0.9rem;
}

.mail-row-menu-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.65rem;
  padding: 0.65rem 0.85rem;
  font-size: 0.8125rem;
  color: rgb(55 65 81);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.mail-row-menu-item:hover {
  background: rgb(243 244 246);
  color: rgb(17 24 39);
}

.dark .mail-row-menu-item {
  color: rgb(226 232 240);
}

.dark .mail-row-menu-item:hover {
  background: rgb(51 65 85);
  color: white;
}

.server-list-row {
  display: flex;
  align-items: center;
  gap: 1rem;
  border-bottom: 1px solid rgb(243 244 246);
  padding: 0.85rem 1rem;
}

.server-list-row:last-child {
  border-bottom: 0;
}

.dark .server-list-row {
  border-color: rgb(51 65 85);
}

.server-list-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.45rem;
  font-size: 0.8125rem;
}

.server-list-cell span {
  flex-shrink: 0;
  color: rgb(148 163 184);
  font-size: 0.75rem;
}

.server-list-cell strong {
  min-width: 0;
  color: rgb(17 24 39);
  font-weight: 600;
}

.dark .server-list-cell strong {
  color: rgb(255 255 255);
}

.sticky-col-right {
  position: sticky;
  right: 0;
  z-index: 10;
  width: var(--mail-col-actions);
  min-width: var(--mail-col-actions);
  overflow: visible;
  border-left: 0;
  background: rgb(255 255 255);
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
  background: var(--mail-table-divider);
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

.group-context-menu {
  position: fixed;
  z-index: 2147483647;
  pointer-events: auto;
  background: rgb(255 255 255);
  box-shadow: 0 18px 40px rgb(15 23 42 / 0.2), 0 0 0 1px rgb(148 163 184 / 0.24);
}

.dark .group-context-menu {
  background: rgb(30 41 59);
  box-shadow: 0 18px 40px rgb(0 0 0 / 0.35), 0 0 0 1px rgb(71 85 105 / 0.7);
}

.context-menu-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.75rem;
  padding: 0.65rem 0.9rem;
  text-align: left;
  font-size: 0.875rem;
  color: rgb(55 65 81);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.context-menu-item:hover {
  background: rgb(243 244 246);
  color: rgb(17 24 39);
}

.context-menu-item:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.context-menu-item:disabled:hover {
  background: transparent;
  color: rgb(55 65 81);
}

.dark .context-menu-item {
  color: rgb(226 232 240);
}

.dark .context-menu-item:hover {
  background: rgb(51 65 85);
  color: rgb(255 255 255);
}

.dark .context-menu-item:disabled:hover {
  background: transparent;
  color: rgb(226 232 240);
}

.context-menu-item.text-red-600:hover {
  color: rgb(220 38 38);
}

.dark .context-menu-item.text-red-600:hover {
  color: rgb(248 113 113);
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

@media (max-width: 1023px) {
  .mail-page-layout {
    flex-direction: column;
    min-height: 0;
  }

  .mail-group-panel {
    width: 100%;
    min-height: 12rem;
    max-height: 16rem;
  }

  .mail-account-panel {
    flex: 0 0 auto;
    min-height: calc(100vh - 24rem);
  }
}

@media (max-width: 767px) {
  .mail-page-layout {
    gap: 0.75rem;
  }

  .mail-group-panel {
    min-height: 10rem;
    max-height: 13rem;
    border-radius: 0.875rem;
  }

  .mail-account-panel {
    min-height: calc(100vh - 18rem);
    border-radius: 0.875rem;
  }

  .mail-account-panel > div:first-child {
    align-items: stretch;
    padding: 0.75rem;
  }

  .mail-account-panel > div:first-child > div:first-child {
    width: 100%;
  }

  .mail-action-primary,
  .mail-action-secondary,
  .mail-action-tertiary {
    flex: 1 1 9rem;
    padding: 0 0.75rem;
    font-size: 0.8125rem;
  }

  .mail-account-panel .input {
    font-size: 0.8125rem;
  }

  .mail-account-panel > div:last-child {
    flex-wrap: wrap;
    gap: 0.75rem;
    padding: 0.75rem;
  }

  .mail-account-panel > div:last-child > div:first-child {
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  :global(.mail-modal-mask) {
    padding: 0.75rem;
  }

  :global(.mail-modal-body) {
    grid-template-columns: minmax(0, 1fr) !important;
  }

  :global(.mail-modal-body > .md\:col-span-2) {
    grid-column: auto !important;
  }

  :global(.simple-server-modal) {
    width: calc(100vw - 1.5rem);
    height: min(40rem, calc(100vh - 1.5rem));
  }

  :global(.mail-receive-modal) {
    width: calc(100vw - 1.5rem);
    height: min(42rem, calc(100vh - 1.5rem));
  }

  :global(.mail-send-modal) {
    width: calc(100vw - 1.5rem);
  }

  .mail-receive-body {
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
    padding: 1rem;
  }

  .mail-receive-footer {
    align-items: flex-start;
    flex-direction: column;
    gap: 0.75rem;
    margin: 0 1rem;
    padding: 0.9rem 0;
  }

  .mail-receive-footer-left {
    flex-wrap: wrap;
  }

  .server-search-action-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .server-search-field {
    width: 100%;
  }

  .server-search-action-row > .btn,
  .server-refresh-button {
    width: 100%;
  }

  .server-list-section {
    flex: 0 0 auto;
  }

  .server-list {
    flex: 0 0 14rem;
    height: 14rem;
    margin-bottom: 1rem;
    min-height: 0;
  }

  .server-list-row {
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 0.65rem;
  }

  .server-list-cell {
    flex: 1 1 100%;
  }
}

@media (min-width: 1600px) {
  .mail-table-area {
    max-height: min(68vh, 900px);
  }
}

@media (max-width: 1279px) {
  .mail-table-area {
    max-height: min(58vh, 640px);
  }
}

@media (max-width: 640px) {
  .mail-group-panel {
    min-height: 9rem;
    max-height: 12rem;
  }

  .mail-account-panel {
    flex: 0 0 auto;
    min-height: calc(100svh - 15rem);
  }

  .mail-table-area {
    flex: 0 0 auto;
    min-height: 18rem;
    max-height: 60svh;
  }

  .mail-account-body {
    flex: 0 0 auto;
  }

  .mail-account-toolbar {
    gap: 0.65rem;
  }

  .mail-account-toolbar .search-clear-field {
    width: 100% !important;
    flex: 1 1 100% !important;
  }

  .mail-account-panel > div:last-child {
    align-items: stretch;
    flex-direction: column;
  }

  .mail-account-panel > div:last-child .pagination-bar {
    width: 100%;
  }

  .mail-action-primary,
  .mail-action-secondary,
  .mail-action-tertiary,
  .mail-action-more,
  .mail-toolbar-batch-button,
  .mail-toolbar-batch-danger {
    min-width: 0;
  }

  :global(.mail-modal-mask) {
    align-items: stretch;
    padding: 0.5rem;
  }

  :global(.mail-form-modal),
  :global(.mail-account-form-modal),
  :global(.mail-import-modal),
  :global(.simple-server-modal),
  :global(.mail-receive-modal),
  :global(.mail-send-modal),
  :global(.remark-modal) {
    width: calc(100vw - 1rem);
    max-height: calc(100svh - 1rem);
    border-radius: 0.875rem;
  }

  :global(.mail-receive-modal) {
    height: calc(100svh - 1rem);
  }

  :global(.mail-modal-scroll-body) {
    overscroll-behavior: contain;
  }
}

@media (max-width: 420px) {
  .mail-action-primary,
  .mail-action-secondary,
  .mail-action-tertiary,
  .mail-action-more,
  .mail-toolbar-batch-button,
  .mail-toolbar-batch-danger {
    flex-basis: 100%;
    width: 100%;
  }
}
</style>
