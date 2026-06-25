<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { Check, ChevronDown, ChevronLeft, ChevronRight, Copy, Folder, Link, Link2, Pencil, Plus, RefreshCw, Search, Table2, Trash2, Unlink, Upload, X } from 'lucide-vue-next'
import PaginationBar from '../components/PaginationBar.vue'
import { useAppStore } from '../stores/app'
import { listMailGroups, type MailGroup } from '../api/mailGroups'
import { listMailAccounts, type MailAccount, type MailAccountListParams, type MailAccountListResponse } from '../api/mailAccounts'
import { listOutlookAccounts, listOutlookGroups, type OutlookAccount, type OutlookAccountListParams, type OutlookGroup } from '../api/outlookAccounts'
import {
  batchCardKeyAction,
  batchCreateCardKeys,
  createCardKey,
  createCardKeyGroup,
  deleteCardKey,
  deleteCardKeyGroup,
  listCardKeyGroups,
  listCardKeys,
  updateCardKey,
  updateCardKeyGroup,
  type CardKey,
  type CardKeyGroup,
  type CardKeyListResponse,
  type CardKeyListParams,
  type CardKeyStatus,
  type SaveCardKeyPayload,
} from '../api/cardKeys'
import { copyToClipboard } from '../utils/clipboard'

const appStore = useAppStore()
const queryClient = useQueryClient()
const fallbackTablePageSize = 20
const fallbackTablePageSizeOptions = [10, 20, 50, 100]
const pageSizeStorageKey = 'card_keys_page_size'
const activeGroupStorageKey = 'card_keys_active_group_id'
const cardKeySortStorageKey = 'card_keys_sort'
const cardKeyManagementCacheKey = 'card_key_management_cache_v1'
const emailPickerSourceStorageKey = 'card_key_email_picker_source'
const emailPickerExpandedStorageKey = 'card_key_email_picker_expanded_group_ids'
const outlookEmailPickerExpandedStorageKey = 'card_key_outlook_email_picker_expanded_group_ids'
const emailPickerActiveGroupStorageKey = 'card_key_email_picker_active_group_id'
const outlookEmailPickerActiveGroupStorageKey = 'card_key_outlook_email_picker_active_group_id'
const emailPickerAvailableCacheStorageKey = 'card_key_email_picker_available_cache_v1'
const mailAccountPageSizeStorageKey = 'mail_accounts_page_size'

type PaginationItem = { key: string; type: 'page'; page: number } | { key: string; type: 'ellipsis' }
type CardKeySortKey = NonNullable<CardKeyListParams['sort_by']>
type EmailPickerSource = 'imap' | 'outlook'
type EmailPickerMode = 'edit' | 'quickBind' | 'batchBind'
type EmailPickerGroup = MailGroup | OutlookGroup
type EmailPickerAccount = Pick<MailAccount | OutlookAccount, 'id' | 'group_id' | 'email' | 'remark'>
type SelectedEmailAccount = EmailPickerAccount & { source: EmailPickerSource; selectionKey: string }
type EmailPickerListParams = MailAccountListParams | OutlookAccountListParams
type EmailPickerListResponse = Omit<MailAccountListResponse, 'items'> & { items: EmailPickerAccount[] }
type EmailPickerPageCacheEntry = EmailPickerListResponse & {
  source: EmailPickerSource
  query: {
    group_id: number
    search: string
    page: number
    page_size: number
    sort_by: string
    sort_order: 'asc' | 'desc'
  }
  updated_at: number
}
type EmailPickerAvailableCache = {
  groups?: Partial<Record<EmailPickerSource, EmailPickerGroup[]>>
  pages?: Record<string, EmailPickerPageCacheEntry>
  updated_at?: number
}
const cardKeySortKeys: CardKeySortKey[] = ['key', 'usage_limit', 'used_at', 'bound_email', 'mail_filter', 'created_at', 'remark']

const groups = ref<CardKeyGroup[]>([])
const activeGroupID = ref(readPersistedActiveGroupID())
const contextGroup = ref<CardKeyGroup | null>(null)
const groupMenuOpen = ref(false)
const groupMenuX = ref(0)
const groupMenuY = ref(0)
const groupNameScrollX = ref(0)
const groupNameScrollMax = ref(0)
const mailGroupListRef = ref<HTMLElement | null>(null)
const showGroupModal = ref(false)
const groupModalMode = ref<'create' | 'edit'>('create')
const groupName = ref('')
const groupSortOrder = ref(1)
const groupSaving = ref(false)

const cardKeys = ref<CardKey[]>([])
const total = ref(0)
const pages = ref(0)
const loading = ref(false)
const saving = ref(false)
const batchSaving = ref(false)
const refreshing = ref(false)
const searchQuery = ref('')
const selectedIDs = ref<number[]>([])
const currentPage = ref(1)
const pageJump = ref('')
const pageSize = ref(readPersistedPageSize() || fallbackTablePageSize)
const pageSizeOptions = ref<number[]>(fallbackTablePageSizeOptions)
const pageSizeDropdownOpen = ref(false)
const persistedCardKeySort = readPersistedCardKeySort()
const sortKey = ref<CardKeySortKey>(persistedCardKeySort.key)
const sortOrder = ref<'asc' | 'desc'>(persistedCardKeySort.order)
const showCardModal = ref(false)
const showBatchModal = ref(false)
const showEmailPicker = ref(false)
const editingID = ref<number | null>(null)
const emailPickerMode = ref<EmailPickerMode>('edit')
const emailPickerSource = ref<EmailPickerSource>('imap')
const emailPickerGroups = ref<EmailPickerGroup[]>([])
const emailPickerAccounts = ref<EmailPickerAccount[]>([])
const emailPickerActiveGroupID = ref(0)
const emailPickerExpandedGroupIDs = ref<number[]>(readPersistedEmailPickerExpandedGroupIDs())
const emailPickerSearch = ref('')
const emailPickerPage = ref(1)
const emailPickerPageSize = ref(readPersistedMailAccountPageSize() || fallbackTablePageSize)
const emailPickerPageSizeOptions = ref<number[]>(fallbackTablePageSizeOptions)
const emailPickerPages = ref(0)
const emailPickerTotal = ref(0)
const emailPickerGroupsLoading = ref(false)
const emailPickerAccountsLoading = ref(false)
const quickBindSaving = ref(false)
const bulkBindSaving = ref(false)
const bulkEmailSelections = ref<SelectedEmailAccount[]>([])
const emailPickerGroupNameScrollX = ref(0)
const emailPickerGroupNameScrollMax = ref(0)
const emailPickerGroupListRef = ref<HTMLElement | null>(null)
let searchTimer: number | undefined
let emailPickerSearchTimer: number | undefined
let cardKeyRequestID = 0
let emailPickerRequestID = 0
let cardKeyAutoRefreshEnabled = false

const form = reactive({
  group_id: 0,
  key: '',
  amount: 0,
  status: 'unused' as CardKeyStatus,
  used_by: '',
  usage_limit: 1,
  mail_days: 1 as number | '',
  mail_keyword: '',
  bound_email: '',
  remark: '',
})

const batchForm = reactive({
  group_id: 0,
  count: 10,
  amount: 0,
  status: 'unused' as CardKeyStatus,
  usage_limit: 1,
  mail_days: 1 as number | '',
  mail_keyword: '',
  bound_email: '',
  remark: '',
})

const currentGroup = computed(() => groups.value.find((item) => item.id === activeGroupID.value) || null)
const emailPickerCurrentGroup = computed(() => emailPickerGroups.value.find((item) => item.id === emailPickerActiveGroupID.value) || emailPickerGroups.value[0] || null)
const emailPickerAllMailGroupCount = computed(() => emailPickerGroups.value.reduce((total, group) => {
  if (group.id === 1) return total
  return total + (Number(group.count) || 0)
}, 0))
const emailPickerGroupChildrenMap = computed(() => {
  const map = new Map<number, EmailPickerGroup[]>()
  for (const group of emailPickerGroups.value) {
    const list = map.get(group.parent_id) || []
    list.push(group)
    map.set(group.parent_id, list)
  }
  for (const list of map.values()) {
    list.sort((a, b) => Number(b.system) - Number(a.system) || emailPickerGroupSortOrder(a) - emailPickerGroupSortOrder(b) || a.id - b.id)
  }
  return map
})
const emailPickerVisibleGroups = computed(() => {
  const result: Array<EmailPickerGroup & { level: number; hasChildren: boolean }> = []
  const visit = (parentID: number, level: number) => {
    const children = emailPickerGroupChildrenMap.value.get(parentID) || []
    for (const group of children) {
      const hasChildren = (emailPickerGroupChildrenMap.value.get(group.id) || []).length > 0
      result.push({ ...group, level, hasChildren })
      if (hasChildren && emailPickerExpandedGroupIDs.value.includes(group.id)) {
        visit(group.id, level + 1)
      }
    }
  }
  visit(0, 0)
  return result
})
const emailPickerTotalPages = computed(() => Math.max(emailPickerPages.value, 1))
const showEmailPickerAccountsLoading = computed(() => emailPickerAccountsLoading.value && emailPickerAccounts.value.length === 0)
const groupModalTitle = computed(() => (groupModalMode.value === 'edit' ? '编辑分组' : '添加分组'))
const groupSortOrderMax = computed(() => Math.max(1, groups.value.length))
const totalPages = computed(() => Math.max(pages.value, 1))
const pageStart = computed(() => (total.value === 0 ? 0 : (currentPage.value - 1) * pageSize.value + 1))
const pageEnd = computed(() => Math.min(currentPage.value * pageSize.value, total.value))
const paginationItems = computed(() => buildPaginationItems(currentPage.value, totalPages.value))
const pageIDs = computed(() => cardKeys.value.map((item) => item.id))
const allPageSelected = computed(() => pageIDs.value.length > 0 && pageIDs.value.every((id) => selectedIDs.value.includes(id)))
const selectedIDSet = computed(() => new Set(selectedIDs.value))
const selectedCardKeys = computed(() => cardKeys.value.filter((item) => selectedIDSet.value.has(item.id)))
const selectedCount = computed(() => selectedCardKeys.value.length)
const showListLoading = computed(() => loading.value && !refreshing.value && cardKeys.value.length === 0)
const isBatchEmailPicker = computed(() => emailPickerMode.value === 'batchBind')
const emailPickerTitle = computed(() => (isBatchEmailPicker.value ? '批量绑定邮箱' : '选择绑定邮箱'))
const bulkEmailSelectionKeySet = computed(() => new Set(bulkEmailSelections.value.map((item) => item.selectionKey)))
const allEmailPickerPageSelected = computed(() => emailPickerAccounts.value.length > 0 && emailPickerAccounts.value.every((account) => bulkEmailSelectionKeySet.value.has(emailAccountSelectionKey(account))))
const someEmailPickerPageSelected = computed(() => emailPickerAccounts.value.some((account) => bulkEmailSelectionKeySet.value.has(emailAccountSelectionKey(account))))
const bulkBindPairCount = computed(() => Math.min(selectedCardKeys.value.length, bulkEmailSelections.value.length))
const cardKeyQueryKey = computed(() => [
  'card-keys',
  activeGroupID.value || 0,
  searchQuery.value.trim(),
  currentPage.value,
  pageSize.value,
  sortKey.value,
  sortOrder.value,
])

onMounted(() => {
  restoreCardKeyManagementCache()
  cardKeyAutoRefreshEnabled = true
  document.addEventListener('click', handleDocumentClick)
  window.addEventListener('resize', updateGroupNameScrollMax)
  window.addEventListener('resize', updateEmailPickerGroupNameScrollMax)
  void refreshAll()
})

onBeforeUnmount(() => {
  window.clearTimeout(searchTimer)
  window.clearTimeout(emailPickerSearchTimer)
  document.removeEventListener('click', handleDocumentClick)
  window.removeEventListener('resize', updateGroupNameScrollMax)
  window.removeEventListener('resize', updateEmailPickerGroupNameScrollMax)
})

watch(groupNameScrollMax, (max) => {
  if (groupNameScrollX.value > max) {
    groupNameScrollX.value = max
  }
})

watch(emailPickerGroupNameScrollMax, (max) => {
  if (emailPickerGroupNameScrollX.value > max) {
    emailPickerGroupNameScrollX.value = max
  }
})

watch(groups, () => {
  void updateGroupNameScrollMax()
}, { flush: 'post' })

watch(emailPickerVisibleGroups, () => {
  void updateEmailPickerGroupNameScrollMax()
}, { flush: 'post' })

watch(showEmailPicker, (visible) => {
  if (visible) {
    void updateEmailPickerGroupNameScrollMax()
  } else {
    emailPickerGroupNameScrollX.value = 0
  }
}, { flush: 'post' })

watch(searchQuery, () => {
  if (!cardKeyAutoRefreshEnabled) return
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    resetToFirstPageOrLoad()
  }, 300)
})

watch(emailPickerSearch, () => {
  if (!showEmailPicker.value) return
  window.clearTimeout(emailPickerSearchTimer)
  emailPickerSearchTimer = window.setTimeout(() => {
    emailPickerPage.value = 1
    void loadEmailPickerAccounts()
  }, 300)
})

watch(activeGroupID, () => {
  localStorage.setItem(activeGroupStorageKey, String(activeGroupID.value))
  selectedIDs.value = []
  if (!cardKeyAutoRefreshEnabled) return
  resetToFirstPageOrLoad()
})

watch(pageSize, () => {
  localStorage.setItem(pageSizeStorageKey, String(pageSize.value))
  if (!cardKeyAutoRefreshEnabled) return
  resetToFirstPageOrLoad()
})

watch(currentPage, () => {
  if (!cardKeyAutoRefreshEnabled) return
  void loadCardKeys()
})

watch([sortKey, sortOrder], () => {
  localStorage.setItem(cardKeySortStorageKey, JSON.stringify({ key: sortKey.value, order: sortOrder.value }))
  if (!cardKeyAutoRefreshEnabled) return
  resetToFirstPageOrLoad()
})

watch(cardKeys, () => {
  const validIDs = new Set(cardKeys.value.map((item) => item.id))
  selectedIDs.value = selectedIDs.value.filter((id) => validIDs.has(id))
})

function readPersistedPageSize() {
  const value = Number(localStorage.getItem(pageSizeStorageKey))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function readPersistedMailAccountPageSize() {
  const value = Number(localStorage.getItem(mailAccountPageSizeStorageKey))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function readPersistedActiveGroupID() {
  const value = Number(localStorage.getItem(activeGroupStorageKey))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function readPersistedCardKeySort() {
  try {
    const value = JSON.parse(localStorage.getItem(cardKeySortStorageKey) || '{}')
    const key = cardKeySortKeys.includes(value.key) ? value.key as CardKeySortKey : 'created_at'
    const order = value.order === 'desc' ? 'desc' : 'asc'
    return { key, order }
  } catch {
    return { key: 'created_at' as CardKeySortKey, order: 'asc' as const }
  }
}

function emailPickerExpandedStorageKeyForSource(source = emailPickerSource.value) {
  return source === 'imap' ? emailPickerExpandedStorageKey : outlookEmailPickerExpandedStorageKey
}

function emailPickerActiveGroupStorageKeyForSource(source = emailPickerSource.value) {
  return source === 'imap' ? emailPickerActiveGroupStorageKey : outlookEmailPickerActiveGroupStorageKey
}

function readPersistedEmailPickerSource(): EmailPickerSource {
  return localStorage.getItem(emailPickerSourceStorageKey) === 'outlook' ? 'outlook' : 'imap'
}

function readPersistedEmailPickerExpandedGroupIDs(source: EmailPickerSource = 'imap') {
  try {
    const value = JSON.parse(localStorage.getItem(emailPickerExpandedStorageKeyForSource(source)) || '[]')
    return Array.isArray(value) ? value.map((item) => Number(item)).filter((item) => Number.isFinite(item) && item > 0) : []
  } catch {
    return []
  }
}

function readPersistedEmailPickerActiveGroupID(source: EmailPickerSource = 'imap') {
  const value = Number(localStorage.getItem(emailPickerActiveGroupStorageKeyForSource(source)))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function defaultEmailPickerGroupID(groups: EmailPickerGroup[]) {
  return groups.find((item) => item.id === 1)?.id || groups[0]?.id || 0
}

function restoreEmailPickerActiveGroupID(groups: EmailPickerGroup[], source = emailPickerSource.value) {
  const persistedGroupID = readPersistedEmailPickerActiveGroupID(source)
  if (persistedGroupID && groups.some((item) => item.id === persistedGroupID)) {
    emailPickerActiveGroupID.value = persistedGroupID
    return
  }
  emailPickerActiveGroupID.value = defaultEmailPickerGroupID(groups)
}

function readEmailPickerAvailableCache(): EmailPickerAvailableCache | null {
  try {
    const value = JSON.parse(localStorage.getItem(emailPickerAvailableCacheStorageKey) || 'null')
    return value && typeof value === 'object' ? value as EmailPickerAvailableCache : null
  } catch {
    return null
  }
}

function writeEmailPickerAvailableCache(cache: EmailPickerAvailableCache) {
  try {
    localStorage.setItem(emailPickerAvailableCacheStorageKey, JSON.stringify({ ...cache, updated_at: Date.now() }))
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

function clearEmailPickerAvailableCache() {
  localStorage.removeItem(emailPickerAvailableCacheStorageKey)
}

function emailPickerAvailablePageCacheKey(source: EmailPickerSource, params: EmailPickerListParams) {
  return [
    source,
    Number(params.group_id) || 0,
    params.search || '',
    Number(params.page) || 1,
    normalizePositiveInteger(params.page_size || emailPickerPageSize.value, fallbackTablePageSize),
    params.sort_by || 'created_at',
    params.sort_order || 'asc',
  ].map((part) => encodeURIComponent(String(part))).join('|')
}

function restoreEmailPickerFromAvailableCache() {
  const cache = readEmailPickerAvailableCache()
  if (!cache) return false

  let restored = false
  const cachedGroups = cache.groups?.[emailPickerSource.value] || []
  if (Array.isArray(cachedGroups) && cachedGroups.length > 0) {
    emailPickerGroups.value = cachedGroups
    restoreEmailPickerActiveGroupID(cachedGroups)
    restored = true
  }

  const cachedAccounts = findEmailPickerAvailableCachedAccounts(emailPickerListParams())
  if (cachedAccounts) {
    applyEmailPickerAccountsResponse(cachedAccounts)
    restored = true
  }

  if (restored) {
    void updateEmailPickerGroupNameScrollMax()
  }
  return restored
}

function findEmailPickerAvailableCachedAccounts(params: EmailPickerListParams): EmailPickerListResponse | null {
  const cache = readEmailPickerAvailableCache()
  const entry = cache?.pages?.[emailPickerAvailablePageCacheKey(emailPickerSource.value, params)]
  if (!entry || !Array.isArray(entry.items)) return null
  return entry
}

function rememberEmailPickerAvailableGroups(source: EmailPickerSource, groups: EmailPickerGroup[]) {
  const cache = readEmailPickerAvailableCache() || {}
  writeEmailPickerAvailableCache({
    ...cache,
    groups: {
      ...(cache.groups || {}),
      [source]: groups,
    },
  })
}

function rememberEmailPickerAvailablePage(source: EmailPickerSource, response: EmailPickerListResponse, params: EmailPickerListParams) {
  const cache = readEmailPickerAvailableCache() || {}
  const pageSize = normalizePositiveInteger(response.page_size || params.page_size || emailPickerPageSize.value, fallbackTablePageSize)
  const page = Number(response.page || params.page) || 1
  const entry: EmailPickerPageCacheEntry = {
    ...response,
    source,
    page,
    page_size: pageSize,
    query: {
      group_id: Number(params.group_id) || 0,
      search: params.search || '',
      page,
      page_size: pageSize,
      sort_by: params.sort_by || 'created_at',
      sort_order: params.sort_order === 'desc' ? 'desc' : 'asc',
    },
    updated_at: Date.now(),
  }
  writeEmailPickerAvailableCache({
    ...cache,
    pages: {
      ...(cache.pages || {}),
      [emailPickerAvailablePageCacheKey(source, params)]: entry,
    },
  })
}

function decreaseEmailPickerGroupCounts(groups: EmailPickerGroup[], groupIDs: number[]) {
  const counts = new Map<number, number>()
  for (const groupID of groupIDs) {
    if (groupID > 0) counts.set(groupID, (counts.get(groupID) || 0) + 1)
  }
  if (counts.size === 0) return groups
  return groups.map((group) => {
    const count = counts.get(group.id) || 0
    return count > 0 ? { ...group, count: Math.max(0, (Number(group.count) || 0) - count) } : group
  })
}

function removeBoundEmailsFromAvailableCache(accounts: SelectedEmailAccount[]) {
  if (accounts.length === 0) return
  const cache = readEmailPickerAvailableCache() || {}
  const pages = { ...(cache.pages || {}) }
  const groups = { ...(cache.groups || {}) }
  const emailsBySource = new Map<EmailPickerSource, Set<string>>()
  const groupIDsBySource = new Map<EmailPickerSource, number[]>()

  for (const account of accounts) {
    const email = account.email.trim().toLowerCase()
    if (!email) continue
    const sourceEmails = emailsBySource.get(account.source) || new Set<string>()
    sourceEmails.add(email)
    emailsBySource.set(account.source, sourceEmails)
    const sourceGroupIDs = groupIDsBySource.get(account.source) || []
    sourceGroupIDs.push(Number(account.group_id) || 0)
    groupIDsBySource.set(account.source, sourceGroupIDs)
  }

  for (const [key, entry] of Object.entries(pages)) {
    const emails = emailsBySource.get(entry.source)
    if (!emails || emails.size === 0) continue
    const nextItems = entry.items.filter((item) => !emails.has(item.email.trim().toLowerCase()))
    const removedCount = entry.items.length - nextItems.length
    if (removedCount > 0) {
      pages[key] = {
        ...entry,
        items: nextItems,
        total: Math.max(0, Number(entry.total) - removedCount),
        pages: calculatePages(Math.max(0, Number(entry.total) - removedCount), entry.page_size),
        updated_at: Date.now(),
      }
    }
  }

  for (const [source, groupIDs] of groupIDsBySource.entries()) {
    const sourceGroups = groups[source] || []
    groups[source] = decreaseEmailPickerGroupCounts(sourceGroups, groupIDs)
  }

  writeEmailPickerAvailableCache({ ...cache, groups, pages })

  const currentEmails = emailsBySource.get(emailPickerSource.value)
  if (currentEmails && currentEmails.size > 0) {
    const removedCurrent = emailPickerAccounts.value.filter((item) => currentEmails.has(item.email.trim().toLowerCase()))
    if (removedCurrent.length > 0) {
      emailPickerAccounts.value = emailPickerAccounts.value.filter((item) => !currentEmails.has(item.email.trim().toLowerCase()))
      emailPickerTotal.value = Math.max(0, emailPickerTotal.value - removedCurrent.length)
      emailPickerPages.value = calculatePages(emailPickerTotal.value, emailPickerPageSize.value)
    }
    emailPickerGroups.value = decreaseEmailPickerGroupCounts(emailPickerGroups.value, groupIDsBySource.get(emailPickerSource.value) || [])
  }
  bulkEmailSelections.value = bulkEmailSelections.value.filter((item) => {
    const sourceEmails = emailsBySource.get(item.source)
    return !sourceEmails?.has(item.email.trim().toLowerCase())
  })
}

function buildPaginationItems(currentPage: number, totalPages: number): PaginationItem[] {
  const normalizedPages = Math.max(1, Math.floor(Number(totalPages) || 1))
  const current = Math.max(1, Math.min(Math.floor(Number(currentPage) || 1), normalizedPages))
  const items: PaginationItem[] = []
  const addPage = (page: number) => items.push({ key: `page-${page}`, type: 'page', page })
  const addEllipsis = (key: string) => items.push({ key, type: 'ellipsis' })

  if (normalizedPages <= 7) {
    for (let page = 1; page <= normalizedPages; page += 1) addPage(page)
    return items
  }
  if (current <= 4) {
    for (let page = 1; page <= 4; page += 1) addPage(page)
    addEllipsis('ellipsis-end')
    addPage(normalizedPages)
    return items
  }
  if (current >= normalizedPages - 3) {
    addPage(1)
    addEllipsis('ellipsis-start')
    for (let page = normalizedPages - 3; page <= normalizedPages; page += 1) addPage(page)
    return items
  }

  addPage(1)
  addEllipsis('ellipsis-start')
  for (let page = current - 1; page <= current + 1; page += 1) addPage(page)
  addEllipsis('ellipsis-end')
  addPage(normalizedPages)
  return items
}

function restoreCardKeyManagementCache() {
  try {
    const value = JSON.parse(localStorage.getItem(cardKeyManagementCacheKey) || 'null')
    if (!value || typeof value !== 'object') return
    if (Array.isArray(value.groups)) {
      groups.value = value.groups
    }
    if (Array.isArray(value.cardKeys)) {
      cardKeys.value = value.cardKeys
    }
    if (value.pagination && typeof value.pagination === 'object') {
      currentPage.value = Number(value.pagination.page) || currentPage.value
      total.value = Number(value.pagination.total) || 0
      pages.value = Number(value.pagination.pages) || 0
      pageSize.value = Number(value.pagination.page_size) || pageSize.value
    }
    if (value.query && typeof value.query === 'object') {
      activeGroupID.value = Number(value.query.group_id) || activeGroupID.value
      searchQuery.value = String(value.query.search || '')
      if (cardKeySortKeys.includes(value.query.sort_by)) {
        sortKey.value = value.query.sort_by
      }
      sortOrder.value = value.query.sort_order === 'desc' ? 'desc' : 'asc'
    }
    if (groups.value.length > 0 && !groups.value.some((item) => item.id === activeGroupID.value)) {
      activeGroupID.value = groups.value[0]?.id || 0
    }
    saveCardKeyManagementCache()
  } catch {
    // Ignore stale cache.
  }
}

function saveCardKeyManagementCache() {
  try {
    localStorage.setItem(
      cardKeyManagementCacheKey,
      JSON.stringify({
        groups: groups.value,
        cardKeys: cardKeys.value,
        pagination: {
          page: currentPage.value,
          page_size: pageSize.value,
          total: total.value,
          pages: pages.value,
        },
        query: {
          group_id: activeGroupID.value,
          search: searchQuery.value,
          sort_by: sortKey.value,
          sort_order: sortOrder.value,
        },
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

function applyCardKeyListResponse(response: CardKeyListResponse) {
  cardKeys.value = response.items
  total.value = response.total
  pages.value = response.pages
  currentPage.value = response.page || currentPage.value
  if (response.page_size && response.page_size !== pageSize.value) {
    pageSize.value = response.page_size
  }
  saveCardKeyManagementCache()
}

async function refreshAll() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    await loadGroups()
    await loadCardKeys()
    saveCardKeyManagementCache()
  } finally {
    refreshing.value = false
  }
}

async function loadGroups() {
  try {
    const response = await listCardKeyGroups()
    groups.value = response
    if (response.length === 0) {
      activeGroupID.value = 0
      clearCardKeyList()
      return
    }
    if (!response.some((item) => item.id === activeGroupID.value)) {
      activeGroupID.value = response[0].id
    }
    saveCardKeyManagementCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '读取卡密分组失败')
  }
}

function currentListParams(): CardKeyListParams {
  return {
    group_id: activeGroupID.value || undefined,
    search: searchQuery.value.trim() || undefined,
    page: currentPage.value,
    page_size: pageSize.value,
    sort_by: sortKey.value,
    sort_order: sortOrder.value,
  }
}

async function loadCardKeys() {
  if (!activeGroupID.value) {
    clearCardKeyList()
    return
  }
  loading.value = true
  const requestID = ++cardKeyRequestID
  try {
    const queryKey = cardKeyQueryKey.value
    const params = currentListParams()
    const cached = queryClient.getQueryData<CardKeyListResponse>(queryKey)
    if (cached) {
      applyCardKeyListResponse(cached)
    }
    const response = await queryClient.fetchQuery({
      queryKey,
      queryFn: () => listCardKeys(params),
      staleTime: 0,
    })
    if (requestID !== cardKeyRequestID) return
    if (response.items.length === 0 && response.total > 0 && response.pages > 0 && currentPage.value > response.pages) {
      currentPage.value = response.pages
      return
    }
    applyCardKeyListResponse(response)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '读取卡密失败')
  } finally {
    loading.value = false
  }
}

function clearCardKeyList() {
  cardKeys.value = []
  total.value = 0
  pages.value = 0
  selectedIDs.value = []
  saveCardKeyManagementCache()
}

function resetToFirstPageOrLoad() {
  if (currentPage.value !== 1) {
    currentPage.value = 1
    return
  }
  void loadCardKeys()
}

function selectGroup(group: CardKeyGroup) {
  activeGroupID.value = group.id
}

function openGroupContextMenu(event: MouseEvent, group?: CardKeyGroup) {
  event.preventDefault()
  contextGroup.value = group || null
  groupMenuX.value = event.clientX
  groupMenuY.value = event.clientY
  groupMenuOpen.value = true
}

function closeGroupMenu() {
  groupMenuOpen.value = false
}

function openGroupModal(mode: 'create' | 'edit') {
  if (mode === 'edit' && !contextGroup.value) return
  groupModalMode.value = mode
  if (mode === 'edit' && contextGroup.value) {
    groupName.value = contextGroup.value.name
    groupSortOrder.value = contextGroup.value.sort_order || 1
  } else {
    groupName.value = ''
    groupSortOrder.value = groups.value.length + 1
  }
  showGroupModal.value = true
  closeGroupMenu()
}

async function openDeleteGroupDialog() {
  if (!contextGroup.value) return
  const group = contextGroup.value
  closeGroupMenu()
  const confirmed = await appStore.showConfirm({
    title: '删除分组',
    message: `确定删除分组 ${group.name} 吗？`,
    description: '如果分组下有卡密，将无法删除。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteCardKeyGroup(group.id)
    appStore.showSuccess('分组已删除')
    await refreshAll()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除分组失败')
  }
}

async function saveGroup() {
  groupSaving.value = true
  try {
    if (groupModalMode.value === 'edit' && contextGroup.value) {
      await updateCardKeyGroup(contextGroup.value.id, {
        name: groupName.value.trim(),
        sort_order: Number(groupSortOrder.value) || 1,
      })
      appStore.showSuccess('分组已更新')
    } else {
      const group = await createCardKeyGroup({ name: groupName.value.trim() })
      activeGroupID.value = group.id
      appStore.showSuccess('分组已添加')
    }
    showGroupModal.value = false
    await refreshAll()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存分组失败')
  } finally {
    groupSaving.value = false
  }
}

function selectPageSize(size: number) {
  pageSize.value = size
  pageSizeDropdownOpen.value = false
}

function changePage(page: number) {
  const nextPage = Math.max(1, Math.min(page, totalPages.value))
  if (nextPage !== currentPage.value) {
    currentPage.value = nextPage
  }
}

function jumpToPage() {
  const value = Number(pageJump.value)
  if (!Number.isFinite(value) || value <= 0) {
    pageJump.value = ''
    return
  }
  changePage(Math.trunc(value))
  pageJump.value = ''
}

function toggleSort(key: CardKeySortKey) {
  if (sortKey.value === key) {
    sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
    return
  }
  sortKey.value = key
  sortOrder.value = 'asc'
}

function toggleAllPage(event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  if (checked) {
    selectedIDs.value = Array.from(new Set([...selectedIDs.value, ...pageIDs.value]))
    return
  }
  const pageIDSet = new Set(pageIDs.value)
  selectedIDs.value = selectedIDs.value.filter((id) => !pageIDSet.has(id))
}

function resetForm() {
  editingID.value = null
  form.group_id = activeGroupID.value
  form.key = ''
  form.amount = 0
  form.status = 'unused'
  form.used_by = ''
  form.usage_limit = 1
  form.mail_days = 1
  form.mail_keyword = ''
  form.bound_email = ''
  form.remark = ''
}

function openAddModal() {
  if (!activeGroupID.value) {
    appStore.showWarning('请先添加卡密分组')
    return
  }
  resetForm()
  showCardModal.value = true
}

function fillCardKeyForm(item: CardKey) {
  editingID.value = item.id
  form.group_id = item.group_id
  form.key = item.key
  form.amount = Number(item.amount) || 0
  form.status = item.status
  form.used_by = item.used_by || ''
  form.usage_limit = normalizePositiveInteger(item.usage_limit, 1)
  form.mail_days = item.mail_days_blank && Number(item.mail_days) === 0 ? '' : normalizeMailDaysForForm(item.mail_days)
  form.mail_keyword = item.mail_keyword || ''
  form.bound_email = item.bound_email || ''
  form.remark = item.remark || ''
}

function openEditModal(item: CardKey) {
  fillCardKeyForm(item)
  showCardModal.value = true
}

async function openBindEmailPicker(item: CardKey) {
  fillCardKeyForm(item)
  showCardModal.value = false
  await nextTick()
  await openEmailPicker('quickBind')
}

async function openBatchBindEmailPicker() {
  if (selectedCardKeys.value.length === 0) {
    appStore.showWarning('请先选择卡密')
    return
  }
  editingID.value = null
  showCardModal.value = false
  await nextTick()
  await openEmailPicker('batchBind')
}

function emailPickerGroupSortOrder(group: EmailPickerGroup) {
  const value = Number(group.sort_order)
  return Number.isFinite(value) && value > 0 ? value : group.id
}

function emailPickerGroupCount(group: EmailPickerGroup) {
  if (group.id === 1) return emailPickerAllMailGroupCount.value
  return Number(group.count) || 0
}

function emailPickerAccountListGroupID() {
  const group = emailPickerCurrentGroup.value
  return group?.id && group.id !== 1 ? group.id : undefined
}

function emailPickerListParams(): EmailPickerListParams {
  return {
    group_id: emailPickerAccountListGroupID(),
    search: emailPickerSearch.value.trim() || undefined,
    page: emailPickerPage.value,
    page_size: emailPickerPageSize.value,
    sort_by: 'created_at',
    sort_order: 'asc',
    exclude_card_key_bound: true,
  }
}

function emailPickerMailAccountQueryKey(params = emailPickerListParams()) {
  return [
    emailPickerSource.value === 'imap' ? 'mail-accounts' : 'outlook-accounts',
    params.group_id || 0,
    params.search || '',
    params.page || 1,
    params.page_size || emailPickerPageSize.value,
    params.sort_by || 'created_at',
    params.sort_order || 'asc',
    params.exclude_card_key_bound ? 1 : 0,
  ]
}

function applyEmailPickerAccountsResponse(response: EmailPickerListResponse) {
  emailPickerAccounts.value = response.items
  emailPickerTotal.value = response.total
  emailPickerPages.value = response.pages
  emailPickerPage.value = response.page || emailPickerPage.value
  if (response.page_size && response.page_size !== emailPickerPageSize.value) {
    emailPickerPageSize.value = response.page_size
  }
}

function findCachedEmailPickerAccounts(params = emailPickerListParams()) {
  const availableCache = findEmailPickerAvailableCachedAccounts(params)
  if (availableCache) return availableCache

  const exact = queryClient.getQueryData<EmailPickerListResponse>(emailPickerMailAccountQueryKey(params))
  if (exact) return exact

  const targetGroupID = params.group_id || 0
  const targetSearch = params.search || ''
  const targetPage = params.page || 1
  const targetPageSize = params.page_size || emailPickerPageSize.value
  const queryPrefix = emailPickerSource.value === 'imap' ? 'mail-accounts' : 'outlook-accounts'
  const queries = queryClient.getQueryCache().findAll({ queryKey: [queryPrefix] })
  for (const query of queries) {
    const key = query.queryKey as readonly unknown[]
    if (key[1] === targetGroupID && key[2] === targetSearch && key[3] === targetPage && key[4] === targetPageSize && key[7] === 1) {
      const data = query.state.data as EmailPickerListResponse | undefined
      if (data) return data
    }
  }
  return null
}

function persistEmailPickerExpandedGroupIDs() {
  localStorage.setItem(emailPickerExpandedStorageKeyForSource(), JSON.stringify(emailPickerExpandedGroupIDs.value))
}

function persistEmailPickerActiveGroupID() {
  if (emailPickerActiveGroupID.value > 0) {
    localStorage.setItem(emailPickerActiveGroupStorageKeyForSource(), String(emailPickerActiveGroupID.value))
  }
}

async function openEmailPicker(mode: EmailPickerMode = 'edit') {
  if (mode !== 'batchBind' && !editingID.value) return
  emailPickerMode.value = mode
  if (mode === 'batchBind') {
    bulkEmailSelections.value = []
  }
  emailPickerSource.value = readPersistedEmailPickerSource()
  emailPickerExpandedGroupIDs.value = readPersistedEmailPickerExpandedGroupIDs(emailPickerSource.value)
  emailPickerActiveGroupID.value = readPersistedEmailPickerActiveGroupID(emailPickerSource.value)
  showEmailPicker.value = true
  emailPickerSearch.value = ''
  emailPickerPage.value = 1
  const restored = restoreEmailPickerFromAvailableCache()
  if (!restored || emailPickerGroups.value.length === 0) {
    await loadEmailPickerGroups()
  } else {
    void loadEmailPickerGroups(true)
  }
  await loadEmailPickerAccounts()
}

function closeEmailPicker() {
  showEmailPicker.value = false
  emailPickerMode.value = 'edit'
  bulkEmailSelections.value = []
  window.clearTimeout(emailPickerSearchTimer)
}

async function switchEmailPickerSource(source: EmailPickerSource) {
  if (emailPickerSource.value === source) return
  emailPickerSource.value = source
  localStorage.setItem(emailPickerSourceStorageKey, source)
  emailPickerExpandedGroupIDs.value = readPersistedEmailPickerExpandedGroupIDs(source)
  emailPickerGroups.value = []
  emailPickerAccounts.value = []
  emailPickerActiveGroupID.value = readPersistedEmailPickerActiveGroupID(source)
  emailPickerPage.value = 1
  emailPickerTotal.value = 0
  emailPickerPages.value = 0
  const restored = restoreEmailPickerFromAvailableCache()
  if (!restored || emailPickerGroups.value.length === 0) {
    await loadEmailPickerGroups()
  } else {
    void loadEmailPickerGroups(true)
  }
  await loadEmailPickerAccounts()
}

async function loadEmailPickerGroups(silent = false) {
  if (!silent) {
    emailPickerGroupsLoading.value = true
  }
  try {
    const response = emailPickerSource.value === 'imap'
      ? await listMailGroups({ exclude_card_key_bound: true })
      : await listOutlookGroups({ exclude_card_key_bound: true })
    emailPickerGroups.value = response
    rememberEmailPickerAvailableGroups(emailPickerSource.value, response)
    restoreEmailPickerActiveGroupID(response)
    void updateEmailPickerGroupNameScrollMax()
  } catch (error) {
    if (!silent) {
      appStore.showError(error instanceof Error ? error.message : `读取 ${emailPickerSource.value === 'imap' ? 'IMAP' : '微软'} 邮箱分组失败`)
    }
  } finally {
    if (!silent) {
      emailPickerGroupsLoading.value = false
    }
  }
}

async function loadEmailPickerAccounts() {
  const requestID = ++emailPickerRequestID
  const source = emailPickerSource.value
  const params = emailPickerListParams()
  const queryKey = emailPickerMailAccountQueryKey(params)
  const cached = findCachedEmailPickerAccounts(params)
  const hadCached = Boolean(cached)
  if (cached) {
    applyEmailPickerAccountsResponse(cached)
    emailPickerAccountsLoading.value = false
  } else {
    emailPickerAccounts.value = []
    emailPickerTotal.value = 0
    emailPickerPages.value = 0
    emailPickerAccountsLoading.value = true
  }
  try {
    const response = await queryClient.fetchQuery<EmailPickerListResponse>({
      queryKey,
      queryFn: async () => {
        if (source === 'imap') {
          return await listMailAccounts(params as MailAccountListParams) as EmailPickerListResponse
        }
        return await listOutlookAccounts(params as OutlookAccountListParams) as EmailPickerListResponse
      },
      staleTime: 0,
    })
    if (requestID !== emailPickerRequestID) return
    if (response.items.length === 0 && response.total > 0 && response.pages > 0 && emailPickerPage.value > response.pages) {
      emailPickerPage.value = response.pages
      await loadEmailPickerAccounts()
      return
    }
    applyEmailPickerAccountsResponse(response)
    rememberEmailPickerAvailablePage(source, response, params)
  } catch (error) {
    if (requestID === emailPickerRequestID && !hadCached) {
      appStore.showError(error instanceof Error ? error.message : `读取 ${source === 'imap' ? 'IMAP' : '微软'} 邮箱失败`)
    }
  } finally {
    if (requestID === emailPickerRequestID) {
      emailPickerAccountsLoading.value = false
    }
  }
}

function toggleEmailPickerGroupExpanded(group: EmailPickerGroup & { hasChildren?: boolean }) {
  if (!group.hasChildren) return
  if (emailPickerExpandedGroupIDs.value.includes(group.id)) {
    emailPickerExpandedGroupIDs.value = emailPickerExpandedGroupIDs.value.filter((id) => id !== group.id)
    persistEmailPickerExpandedGroupIDs()
    return
  }
  emailPickerExpandedGroupIDs.value = [...emailPickerExpandedGroupIDs.value, group.id]
  persistEmailPickerExpandedGroupIDs()
}

function selectEmailPickerGroup(group: EmailPickerGroup & { hasChildren?: boolean }) {
  emailPickerActiveGroupID.value = group.id
  persistEmailPickerActiveGroupID()
  emailPickerPage.value = 1
  void loadEmailPickerAccounts()
  if (group.hasChildren) {
    toggleEmailPickerGroupExpanded(group)
  }
}

function changeEmailPickerPage(page: number) {
  const nextPage = Math.max(1, Math.min(page, emailPickerTotalPages.value))
  if (nextPage === emailPickerPage.value) return
  emailPickerPage.value = nextPage
  void loadEmailPickerAccounts()
}

function selectEmailPickerPageSize(size: number) {
  const nextSize = normalizePositiveInteger(size, fallbackTablePageSize)
  if (nextSize === emailPickerPageSize.value) return
  emailPickerPageSize.value = nextSize
  localStorage.setItem(mailAccountPageSizeStorageKey, String(nextSize))
  emailPickerPage.value = 1
  void loadEmailPickerAccounts()
}

function emailAccountSelectionKey(account: EmailPickerAccount, source = emailPickerSource.value) {
  return `${source}:${account.id}`
}

function toSelectedEmailAccount(account: EmailPickerAccount, source = emailPickerSource.value): SelectedEmailAccount {
  return {
    ...account,
    source,
    selectionKey: emailAccountSelectionKey(account, source),
  }
}

function toggleEmailAccountSelection(account: EmailPickerAccount, event?: Event) {
  if (!isBatchEmailPicker.value || bulkBindSaving.value) return
  const key = emailAccountSelectionKey(account)
  const checked = event ? (event.target as HTMLInputElement).checked : !bulkEmailSelectionKeySet.value.has(key)
  if (checked) {
    if (!bulkEmailSelectionKeySet.value.has(key)) {
      bulkEmailSelections.value = [...bulkEmailSelections.value, toSelectedEmailAccount(account)]
    }
    return
  }
  bulkEmailSelections.value = bulkEmailSelections.value.filter((item) => item.selectionKey !== key)
}

function toggleAllEmailPickerPage(event: Event) {
  if (!isBatchEmailPicker.value || bulkBindSaving.value) return
  const checked = (event.target as HTMLInputElement).checked
  const pageKeys = new Set(emailPickerAccounts.value.map((account) => emailAccountSelectionKey(account)))
  if (checked) {
    const existingKeys = bulkEmailSelectionKeySet.value
    const additions = emailPickerAccounts.value
      .filter((account) => !existingKeys.has(emailAccountSelectionKey(account)))
      .map((account) => toSelectedEmailAccount(account))
    bulkEmailSelections.value = [...bulkEmailSelections.value, ...additions]
    return
  }
  bulkEmailSelections.value = bulkEmailSelections.value.filter((item) => !pageKeys.has(item.selectionKey))
}

function buildCardKeyPayload(): SaveCardKeyPayload {
  return {
    group_id: Number(form.group_id) || 0,
    key: editingID.value ? form.key.trim() : '',
    amount: Number(form.amount) || 0,
    status: form.status,
    used_by: form.status === 'used' ? form.used_by.trim() : '',
    usage_limit: normalizePositiveInteger(form.usage_limit, 1),
    mail_days: normalizeMailDaysForPayload(form.mail_days),
    mail_days_blank: isMailDaysBlank(form.mail_days),
    mail_keyword: form.mail_keyword.trim(),
    bound_email: form.bound_email.trim(),
    remark: form.remark.trim(),
  }
}

function buildCardKeyPayloadFromItem(item: CardKey, boundEmail = item.bound_email): SaveCardKeyPayload {
  return {
    group_id: Number(item.group_id) || 0,
    key: item.key,
    amount: Number(item.amount) || 0,
    status: item.status,
    used_by: item.status === 'used' ? item.used_by || '' : '',
    usage_limit: normalizePositiveInteger(item.usage_limit, 1),
    mail_days: normalizeMailDaysForPayload(item.mail_days),
    mail_days_blank: Boolean(item.mail_days_blank),
    mail_keyword: item.mail_keyword || '',
    bound_email: boundEmail,
    remark: item.remark || '',
  }
}

async function selectEmailAccount(account: EmailPickerAccount) {
  if (emailPickerMode.value === 'batchBind') {
    toggleEmailAccountSelection(account)
    return
  }
  form.bound_email = account.email
  if (emailPickerMode.value === 'quickBind') {
    if (!editingID.value || quickBindSaving.value) return
    quickBindSaving.value = true
    try {
      await updateCardKey(editingID.value, buildCardKeyPayload())
      removeBoundEmailsFromAvailableCache([toSelectedEmailAccount(account)])
      appStore.showSuccess('邮箱绑定成功')
      closeEmailPicker()
      showCardModal.value = false
      await refreshAll()
    } catch (error) {
      appStore.showError(error instanceof Error ? error.message : '绑定邮箱失败')
    } finally {
      quickBindSaving.value = false
    }
    return
  }
  closeEmailPicker()
}

async function saveBatchBoundEmails() {
  const cards = selectedCardKeys.value
  if (cards.length === 0) {
    appStore.showWarning('请先选择卡密')
    return
  }
  if (bulkEmailSelections.value.length === 0) {
    appStore.showWarning('请选择邮箱')
    return
  }
  const assignments = cards.slice(0, bulkEmailSelections.value.length).map((item, index) => ({
    item,
    account: bulkEmailSelections.value[index],
  }))
  if (assignments.length === 0) {
    appStore.showWarning('没有可绑定的邮箱')
    return
  }

  bulkBindSaving.value = true
  try {
    const results = await Promise.allSettled(
      assignments.map(({ item, account }) => updateCardKey(item.id, buildCardKeyPayloadFromItem(item, account.email)))
    )
    const successCount = results.filter((result) => result.status === 'fulfilled').length
    const failedCount = results.length - successCount
    if (successCount > 0) {
      const boundAccounts = assignments
        .filter((_, index) => results[index].status === 'fulfilled')
        .map(({ account }) => account)
      removeBoundEmailsFromAvailableCache(boundAccounts)
      appStore.showSuccess(`已绑定 ${successCount} 个邮箱`)
      closeEmailPicker()
      await refreshAll()
    }
    if (failedCount > 0) {
      appStore.showError(`批量绑定完成，${failedCount} 个失败`)
    }
    if (successCount === 0) {
      appStore.showError('批量绑定邮箱失败')
    }
  } finally {
    bulkBindSaving.value = false
  }
}

function clearBoundEmail() {
  form.bound_email = ''
}

async function saveCardKeyForm() {
  saving.value = true
  try {
    const payload = buildCardKeyPayload()
    if (editingID.value) {
      await updateCardKey(editingID.value, payload)
      appStore.showSuccess('卡密已更新')
    } else {
      await createCardKey(payload)
      appStore.showSuccess('卡密已生成')
    }
    clearEmailPickerAvailableCache()
    showCardModal.value = false
    await refreshAll()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : (editingID.value ? '保存卡密失败' : '生成卡密失败'))
  } finally {
    saving.value = false
  }
}

function openBatchModal() {
  if (!activeGroupID.value) {
    appStore.showWarning('请先添加卡密分组')
    return
  }
  batchForm.group_id = activeGroupID.value
  batchForm.count = 10
  batchForm.amount = 0
  batchForm.status = 'unused'
  batchForm.usage_limit = 1
  batchForm.mail_days = 1
  batchForm.mail_keyword = ''
  batchForm.bound_email = ''
  batchForm.remark = ''
  showBatchModal.value = true
}

async function saveBatchCardKeys() {
  batchSaving.value = true
  try {
    const items = await batchCreateCardKeys({
      group_id: Number(batchForm.group_id) || 0,
      count: normalizePositiveInteger(batchForm.count, 1),
      amount: Number(batchForm.amount) || 0,
      status: batchForm.status,
      usage_limit: normalizePositiveInteger(batchForm.usage_limit, 1),
      mail_days: normalizeMailDaysForPayload(batchForm.mail_days),
      mail_days_blank: isMailDaysBlank(batchForm.mail_days),
      mail_keyword: batchForm.mail_keyword.trim(),
      bound_email: batchForm.bound_email.trim(),
      remark: batchForm.remark.trim(),
    })
    appStore.showSuccess(`已生成 ${items.length} 个卡密`)
    if (batchForm.bound_email.trim()) {
      clearEmailPickerAvailableCache()
    }
    showBatchModal.value = false
    await refreshAll()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '批量生成卡密失败')
  } finally {
    batchSaving.value = false
  }
}

async function removeCardKey(item: CardKey) {
  const confirmed = await appStore.showConfirm({
    title: '删除卡密',
    message: `确定删除卡密 ${item.key} 吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteCardKey(item.id)
    clearEmailPickerAvailableCache()
    appStore.showSuccess('卡密已删除')
    await refreshAll()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除卡密失败')
  }
}

async function removeSelectedCardKeys() {
  if (selectedIDs.value.length === 0) return
  const confirmed = await appStore.showConfirm({
    title: '批量删除卡密',
    message: `确定删除选中的 ${selectedIDs.value.length} 个卡密吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    const result = await batchCardKeyAction({ action: 'delete', ids: selectedIDs.value })
    clearEmailPickerAvailableCache()
    appStore.showSuccess(`已删除 ${result.count} 个卡密`)
    selectedIDs.value = []
    await refreshAll()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '批量删除卡密失败')
  }
}

async function unbindSelectedCardKeys() {
  if (selectedIDs.value.length === 0) return
  const confirmed = await appStore.showConfirm({
    title: '批量解绑邮箱',
    message: `确定解绑选中的 ${selectedIDs.value.length} 个卡密吗？`,
    description: '未绑定邮箱的卡密会自动跳过。',
    confirmText: '确认解绑',
    tone: 'warning',
  })
  if (!confirmed) return

  try {
    const result = await batchCardKeyAction({ action: 'unbind_email', ids: selectedIDs.value })
    clearEmailPickerAvailableCache()
    const skipped = result.skipped || 0
    appStore.showSuccess(skipped > 0 ? `已解绑 ${result.count} 个卡密，跳过 ${skipped} 个未绑定` : `已解绑 ${result.count} 个卡密`)
    selectedIDs.value = []
    await refreshAll()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '批量解绑邮箱失败')
  }
}

async function copyText(value: string, successMessage: string) {
  try {
    await copyToClipboard(value)
    appStore.showSuccess(successMessage)
  } catch {
    appStore.showError('复制失败')
  }
}

async function copyCardKey(value: string) {
  await copyText(value, '卡密已复制')
}

async function copyBoundEmail(value: string) {
  await copyText(value, '邮箱已复制')
}

function cardKeyPublicLink(value: string) {
  return `${window.location.origin}/mail/keys=${encodeURIComponent(value)}/`
}

async function copyCardKeyPublicLink(item: CardKey) {
  await copyText(cardKeyPublicLink(item.key), 'API链接已复制')
}

async function copyCardKeyExcelRow(item: CardKey) {
  await copyText(`${item.bound_email || ''}\t${cardKeyPublicLink(item.key)}`, '邮箱和API链接已复制，可直接粘贴到Excel')
}

async function copySelectedCardKeyPublicLinks() {
  const items = selectedCardKeys.value
  if (items.length === 0) {
    appStore.showWarning('请先选择卡密')
    return
  }
  await copyText(items.map((item) => cardKeyPublicLink(item.key)).join('\n'), `已复制 ${items.length} 个API链接`)
}

async function copySelectedCardKeyExcelRows() {
  const items = selectedCardKeys.value
  if (items.length === 0) {
    appStore.showWarning('请先选择卡密')
    return
  }
  const rows = items.map((item) => `${item.bound_email || ''}\t${cardKeyPublicLink(item.key)}`)
  await copyText(rows.join('\n'), `已复制 ${items.length} 行邮箱和API链接，可直接粘贴到Excel`)
}

function normalizePositiveInteger(value: number | string, fallback: number) {
  const next = Math.trunc(Number(value))
  return Number.isFinite(next) && next > 0 ? next : fallback
}

function calculatePages(total: number, size: number) {
  const pageSize = normalizePositiveInteger(size, fallbackTablePageSize)
  return total <= 0 ? 0 : Math.ceil(total / pageSize)
}

function normalizeMailDaysForForm(value: number | string) {
  const next = Math.trunc(Number(value))
  return Number.isFinite(next) && next >= 0 ? next : ''
}

function isMailDaysBlank(value: number | string) {
  return String(value).trim() === ''
}

function normalizeMailDaysForPayload(value: number | string) {
  if (isMailDaysBlank(value)) return 0
  const next = Math.trunc(Number(value))
  return Number.isFinite(next) && next > 0 ? next : 0
}

function formatMailFilter(item: CardKey) {
  const keyword = mailFilterKeyword(item)
  return `${mailFilterDays(item)} ${keyword}`
}

function mailFilterDays(item: CardKey) {
  const days = Math.trunc(Number(item.mail_days))
  return Number.isFinite(days) && days > 0 ? `收件：${days}天` : '收件：不限制'
}

function mailFilterKeyword(item: CardKey) {
  return `关键词：${item.mail_keyword.trim() || '-'}`
}

function cardKeyUsageText(item: CardKey) {
  const used = Math.max(0, Number(item.used_count) || 0)
  const limit = normalizePositiveInteger(item.usage_limit, 1)
  return `${used}/${limit}`
}

function syncGroupNameScroll(event: Event) {
  groupNameScrollX.value = (event.currentTarget as HTMLElement).scrollLeft
}

function syncEmailPickerGroupNameScroll(event: Event) {
  emailPickerGroupNameScrollX.value = (event.currentTarget as HTMLElement).scrollLeft
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

async function updateEmailPickerGroupNameScrollMax() {
  await nextTick()
  const list = emailPickerGroupListRef.value
  if (!list) {
    emailPickerGroupNameScrollMax.value = 0
    emailPickerGroupNameScrollX.value = 0
    return
  }

  const max = Array.from(list.querySelectorAll<HTMLElement>('.card-key-email-picker-group-name-viewport')).reduce((currentMax, viewport) => {
    const inner = viewport.firstElementChild as HTMLElement | null
    if (!inner) return currentMax
    return Math.max(currentMax, Math.ceil(inner.scrollWidth - viewport.clientWidth))
  }, 0)

  emailPickerGroupNameScrollMax.value = Math.max(0, max)
  if (emailPickerGroupNameScrollX.value > emailPickerGroupNameScrollMax.value) {
    emailPickerGroupNameScrollX.value = emailPickerGroupNameScrollMax.value
  }
}

function handleDocumentClick(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (!target.closest('[data-page-size-select]')) {
    pageSizeDropdownOpen.value = false
  }
  if (!target.closest('[data-card-key-group-menu]')) {
    groupMenuOpen.value = false
  }
}
</script>

<template>
  <div class="mail-page-layout card-key-page-layout min-h-[calc(100vh-8rem)] gap-3">
    <aside class="mail-group-panel card-key-group-panel shrink-0 rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/50" @contextmenu="openGroupContextMenu">
      <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div>
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">卡密分组</h2>
        </div>
      </div>

      <div class="mail-group-list-wrap">
        <div ref="mailGroupListRef" class="mail-group-list space-y-1 p-3">
        <button
          v-for="group in groups"
          :key="group.id"
          class="mail-group-item flex w-full select-none items-center justify-between rounded-xl px-3 py-2.5 text-left text-sm transition-colors"
          :class="activeGroupID === group.id
            ? 'bg-primary-50 text-primary-700 dark:bg-dark-700 dark:text-primary-300'
            : 'text-gray-600 hover:bg-gray-50 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-700/70 dark:hover:text-white'"
          type="button"
          @click="selectGroup(group)"
          @contextmenu.stop="openGroupContextMenu($event, group)"
        >
          <span class="mail-group-name-viewport">
            <span class="mail-group-name-inner" :style="{ transform: `translateX(-${groupNameScrollX}px)` }">
              <span class="flex h-4 w-4 shrink-0 items-center justify-center">
                <Folder class="h-4 w-4" />
              </span>
              <span class="mail-group-name" :title="group.name">{{ group.name }}</span>
            </span>
          </span>
          <span class="mail-group-count rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-900 dark:text-dark-400">{{ group.count }}</span>
        </button>
        <div v-if="groups.length === 0" class="px-3 py-8 text-center text-sm font-semibold text-gray-400 dark:text-dark-400">
          暂无分组
        </div>
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
          data-card-key-group-menu
          class="group-context-menu w-44 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 shadow-xl shadow-black/10 dark:border-dark-600 dark:bg-dark-800 dark:shadow-black/30"
          :style="{ left: `${groupMenuX}px`, top: `${groupMenuY}px` }"
          @click.stop
          @contextmenu.prevent.stop
        >
          <button class="context-menu-item" type="button" @click.stop="openGroupModal('create')">
            <Plus class="h-4 w-4" />
            <span>添加分组</span>
          </button>
          <button class="context-menu-item" type="button" :disabled="!contextGroup" @click.stop="openGroupModal('edit')">
            <Pencil class="h-4 w-4" />
            <span>编辑分组</span>
          </button>
          <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
          <button class="context-menu-item text-red-600 dark:text-red-400" type="button" :disabled="!contextGroup" @click.stop="openDeleteGroupDialog">
            <Trash2 class="h-4 w-4" />
            <span>删除分组</span>
          </button>
        </div>
      </Teleport>
    </aside>

    <section class="mail-account-panel card-key-panel min-w-0 flex-1 overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/50">
      <div class="mail-account-toolbar flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div class="card-key-actions flex flex-wrap items-center gap-2">
          <button class="mail-action-primary" type="button" :disabled="!activeGroupID" @click="openAddModal">
            <Plus class="h-4 w-4" />
            生成卡密
          </button>
          <button class="mail-action-secondary" type="button" :disabled="!activeGroupID" @click="openBatchModal">
            <Upload class="h-4 w-4" />
            批量生成卡密
          </button>
          <button class="mail-action-refresh" type="button" :disabled="refreshing || loading" @click="refreshAll">
            <RefreshCw class="h-4 w-4" :class="{ 'mail-refresh-icon-spinning': refreshing }" />
            刷新
          </button>
          <button v-if="selectedCount > 0" class="mail-toolbar-batch-button" type="button" @click="openBatchBindEmailPicker">
            <Link class="h-4 w-4" />
            批量绑定({{ selectedCount }})
          </button>
          <button v-if="selectedCount > 0" class="mail-toolbar-batch-button" type="button" @click="unbindSelectedCardKeys">
            <Unlink class="h-4 w-4" />
            批量解绑({{ selectedCount }})
          </button>
          <button v-if="selectedCount > 0" class="mail-toolbar-batch-button" type="button" @click="copySelectedCardKeyPublicLinks">
            <Link2 class="h-4 w-4" />
            批量复制链接({{ selectedCount }})
          </button>
          <button v-if="selectedCount > 0" class="mail-toolbar-batch-button" type="button" @click="copySelectedCardKeyExcelRows">
            <Table2 class="h-4 w-4" />
            批量复制表格({{ selectedCount }})
          </button>
          <button v-if="selectedCount > 0" class="mail-toolbar-batch-danger" type="button" @click="removeSelectedCardKeys">
            <Trash2 class="h-4 w-4" />
            批量删除({{ selectedCount }})
          </button>
        </div>
        <div class="search-clear-field relative max-w-full" style="width: min(350px, 100%); flex: 0 0 min(350px, 100%);">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
          <input v-model.trim="searchQuery" class="input search-clear-input h-9 pl-10 text-sm" type="text" placeholder="搜索卡密、绑定邮箱、备注" />
          <button v-if="searchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="searchQuery = ''">
            <X class="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <div class="mail-account-body flex-1">
        <div class="mail-table-area card-key-table-wrap relative overflow-x-auto">
          <table class="mail-account-table card-key-table text-sm">
            <colgroup>
              <col class="card-key-col-select" />
              <col class="card-key-col-key" />
              <col class="card-key-col-usage" />
              <col class="card-key-col-used-at" />
              <col class="card-key-col-bound-email" />
              <col class="card-key-col-mail-filter" />
              <col class="card-key-col-created" />
              <col class="card-key-col-remark" />
              <col class="card-key-col-actions" />
            </colgroup>
            <thead class="bg-gray-50 text-center text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
              <tr>
                <th class="card-key-select-col px-5 py-3 font-medium">
                  <input :checked="allPageSelected" type="checkbox" @change="toggleAllPage" />
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleSort('key')">
                    <span class="mail-sort-label">卡密</span>
                    <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': sortKey === 'key' && sortOrder === 'asc' }" />
                  </button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleSort('usage_limit')">
                    <span class="mail-sort-label">使用次数</span>
                    <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': sortKey === 'usage_limit' && sortOrder === 'asc' }" />
                  </button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleSort('used_at')">
                    <span class="mail-sort-label">使用时间</span>
                    <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': sortKey === 'used_at' && sortOrder === 'asc' }" />
                  </button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleSort('bound_email')">
                    <span class="mail-sort-label">绑定邮箱</span>
                    <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': sortKey === 'bound_email' && sortOrder === 'asc' }" />
                  </button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleSort('mail_filter')">
                    <span class="mail-sort-label">邮件过滤</span>
                    <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': sortKey === 'mail_filter' && sortOrder === 'asc' }" />
                  </button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleSort('created_at')">
                    <span class="mail-sort-label">生成时间</span>
                    <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': sortKey === 'created_at' && sortOrder === 'asc' }" />
                  </button>
                </th>
                <th class="px-5 py-3 font-medium">
                  <button class="mail-sort-button" type="button" @click="toggleSort('remark')">
                    <span class="mail-sort-label">备注</span>
                    <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': sortKey === 'remark' && sortOrder === 'asc' }" />
                  </button>
                </th>
                <th class="sticky-col sticky-col-right px-5 py-3 text-center font-medium">操作</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
              <tr v-for="item in cardKeys" :key="item.id" class="hover:bg-gray-50 dark:hover:bg-dark-800">
                <td class="card-key-select-col px-5 py-4">
                  <input v-model="selectedIDs" :value="item.id" type="checkbox" />
                </td>
                <td class="px-5 py-4 font-medium text-gray-900 dark:text-white" :title="item.key">
                  <div class="card-key-value-cell">
                    <span>{{ item.key }}</span>
                    <button class="card-key-copy-button" type="button" title="复制卡密" @click.stop="copyCardKey(item.key)">
                      <Copy class="h-3.5 w-3.5" />
                    </button>
                  </div>
                </td>
                <td class="px-5 py-4 text-gray-700 dark:text-gray-200">{{ cardKeyUsageText(item) }}</td>
                <td class="px-5 py-4 text-gray-500 dark:text-dark-400">{{ item.used_at || '-' }}</td>
                <td class="px-5 py-4 font-medium text-gray-900 dark:text-white" :title="item.bound_email">
                  <div v-if="item.bound_email" class="card-key-bound-email-cell">
                    <button class="card-key-bound-email-link" type="button" @click.stop="openEditModal(item)">{{ item.bound_email }}</button>
                    <button class="card-key-bound-email-copy-button" type="button" title="复制邮箱" @click.stop="copyBoundEmail(item.bound_email)">
                      <Copy class="h-3.5 w-3.5" />
                    </button>
                  </div>
                  <span v-else>-</span>
                </td>
                <td class="px-5 py-4 text-gray-500 dark:text-dark-400" :title="formatMailFilter(item)">
                  <div class="card-key-mail-filter-cell">
                    <span class="card-key-mail-filter-days">{{ mailFilterDays(item) }}</span>
                    <span class="card-key-mail-filter-keyword">{{ mailFilterKeyword(item) }}</span>
                  </div>
                </td>
                <td class="px-5 py-4 text-gray-500 dark:text-dark-400">{{ item.created_at }}</td>
                <td class="px-5 py-4 text-gray-500 dark:text-dark-400" :title="item.remark">{{ item.remark || '' }}</td>
                <td class="sticky-col sticky-col-right card-key-actions-cell py-4 text-center">
                  <div class="mail-row-actions text-gray-500 dark:text-dark-400">
                    <button class="mail-row-action-button hover:text-primary-600 dark:hover:text-primary-300" type="button" title="编辑卡密" @click="openEditModal(item)">
                      <Pencil class="h-4 w-4" />
                      <span>编辑</span>
                    </button>
                    <button class="mail-row-action-button hover:text-primary-600 dark:hover:text-primary-300" type="button" title="绑定邮箱" @click="openBindEmailPicker(item)">
                      <Link class="h-4 w-4" />
                      <span>绑定</span>
                    </button>
                    <button class="mail-row-action-button hover:text-primary-600 dark:hover:text-primary-300" type="button" title="复制API链接" @click="copyCardKeyPublicLink(item)">
                      <Link2 class="h-4 w-4" />
                      <span>链接</span>
                    </button>
                    <button class="mail-row-action-button hover:text-primary-600 dark:hover:text-primary-300" type="button" title="复制邮箱和API链接，可粘贴到Excel" @click="copyCardKeyExcelRow(item)">
                      <Table2 class="h-4 w-4" />
                      <span>表格</span>
                    </button>
                    <button class="mail-row-action-button hover:text-red-600 dark:hover:text-red-400" type="button" title="删除卡密" @click="removeCardKey(item)">
                      <Trash2 class="h-4 w-4" />
                      <span>删除</span>
                    </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
          <div v-if="!showListLoading && activeGroupID && cardKeys.length === 0" class="mail-empty-state p-8 text-center text-sm font-semibold text-gray-500 dark:text-dark-400">
            暂无卡密
          </div>
          <div v-if="!showListLoading && !activeGroupID" class="mail-empty-state p-8 text-center text-sm font-semibold text-gray-500 dark:text-dark-400">
            暂无分组
          </div>
          <div v-if="showListLoading" class="mail-empty-state p-8 text-center text-sm font-semibold text-gray-500 dark:text-dark-400">
            加载中...
          </div>
        </div>
      </div>

      <div class="card-key-footer flex items-center justify-between border-t border-gray-200 bg-gray-50 px-5 py-3 dark:border-dark-700 dark:bg-dark-800">
        <PaginationBar
          :page="currentPage"
          :pages="totalPages"
          :page-size="pageSize"
          :page-size-options="pageSizeOptions"
          :total="total"
          @page-change="changePage"
          @page-size-change="selectPageSize"
        />
        <div v-if="false" class="flex items-center gap-3">
          <p class="text-sm text-gray-700 dark:text-gray-300">
            显示 {{ pageStart }} 至 {{ pageEnd }} 共 {{ total }} 条结果
          </p>
          <div class="flex items-center gap-2">
            <span class="text-sm text-gray-700 dark:text-gray-300">每页:</span>
            <div class="page-size-select relative w-20" data-page-size-select>
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
          <button class="pagination-arrow-button relative inline-flex items-center rounded-l-md border border-gray-300 bg-white px-2 py-2 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600" type="button" :disabled="currentPage <= 1" @click="changePage(currentPage - 1)">
            <ChevronLeft class="h-4 w-4" />
          </button>
          <template v-for="item in paginationItems" :key="item.key">
            <span v-if="item.type === 'ellipsis'" class="pagination-ellipsis relative inline-flex items-center border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400">...</span>
            <button
              v-else
              class="pagination-page-button relative inline-flex items-center border px-4 py-2 text-sm font-medium"
              :class="item.page === currentPage ? 'z-10 border-primary-500 bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400' : 'border-gray-300 bg-white text-gray-500 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600'"
              type="button"
              @click="changePage(item.page)"
            >
              {{ item.page }}
            </button>
          </template>
          <button class="pagination-arrow-button relative inline-flex items-center rounded-r-md border border-gray-300 bg-white px-2 py-2 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600" type="button" :disabled="currentPage >= totalPages" @click="changePage(currentPage + 1)">
            <ChevronRight class="h-4 w-4" />
          </button>
          <form class="page-jump-form" @submit.prevent="jumpToPage">
            <input
              v-model.trim="pageJump"
              class="page-jump-input"
              type="text"
              inputmode="numeric"
              pattern="[0-9]*"
              min="1"
              :max="totalPages"
              :placeholder="String(currentPage)"
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
        <label v-if="groupModalMode === 'edit'" class="mt-5 block">
          <span class="input-label">序号</span>
          <input v-model.number="groupSortOrder" class="input" type="number" min="1" :max="groupSortOrderMax" step="1" @keyup.enter="saveGroup" />
        </label>
        <label class="mt-5 block">
          <span class="input-label">分组名称</span>
          <input v-model.trim="groupName" class="input" type="text" placeholder="请输入分组名称" @keyup.enter="saveGroup" />
        </label>
        <div class="mt-6 flex justify-end gap-2">
          <button class="btn btn-secondary" type="button" :disabled="groupSaving" @click="showGroupModal = false">取消</button>
          <button class="btn btn-primary" type="button" :disabled="groupSaving" @click="saveGroup">
            {{ groupSaving ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>

    <Teleport to="body">
      <div v-if="showCardModal" class="mail-modal-mask center-mail-modal">
        <div class="mail-form-modal card-key-form-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-dark-700">
            <h3 class="text-base font-bold text-gray-900 dark:text-white">{{ editingID ? '编辑卡密' : '生成卡密' }}</h3>
            <button class="modal-close-button" type="button" :disabled="saving" @click="showCardModal = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-modal-body mail-modal-scroll-body grid gap-3 p-5">
            <label>
              <span class="input-label">分组归属 *</span>
              <select v-model.number="form.group_id" class="input">
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
            </label>
            <label v-if="editingID">
              <span class="input-label">卡密 *</span>
              <input v-model.trim="form.key" class="input card-key-readonly-input" disabled readonly placeholder="请输入卡密" />
            </label>
            <div v-if="editingID" class="card-key-bound-email-field">
              <span class="input-label">绑定邮箱</span>
              <div class="card-key-bound-email-row">
                <input v-model.trim="form.bound_email" class="input card-key-bound-email-input" readonly placeholder="未绑定邮箱" />
                <button class="card-key-email-action" type="button" @click="openEmailPicker">
                  <Search class="h-4 w-4" />
                  <span>选择邮箱</span>
                </button>
                <button class="card-key-email-clear" type="button" :disabled="!form.bound_email" @click="clearBoundEmail">
                  <X class="h-4 w-4" />
                  <span>清除</span>
                </button>
              </div>
            </div>
            <label>
              <span class="input-label">使用次数</span>
              <input v-model.number="form.usage_limit" class="input" type="number" min="1" step="1" />
            </label>
            <label>
              <span class="input-label">收取 X 天内的邮件</span>
              <input v-model.number="form.mail_days" class="input" type="number" min="0" step="1" placeholder="不填或 0 为不限制天数" />
            </label>
            <label>
              <span class="input-label">关键词邮件</span>
              <input v-model.trim="form.mail_keyword" class="input" placeholder="请输入关键词" />
            </label>
            <label>
              <span class="input-label">备注</span>
              <input v-model.trim="form.remark" class="input" placeholder="请输入备注" />
            </label>
          </div>
          <div class="flex shrink-0 justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" :disabled="saving" @click="showCardModal = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="saving" @click="saveCardKeyForm">
              {{ saving ? (editingID ? '保存中...' : '生成中...') : (editingID ? '保存' : '生成卡密') }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to="body">
      <div v-if="showEmailPicker" class="mail-modal-mask center-mail-modal card-key-email-picker-mask">
        <div class="card-key-email-picker-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900" @click.stop>
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-dark-700">
            <h3 class="text-base font-bold text-gray-900 dark:text-white">{{ emailPickerTitle }}</h3>
            <button class="modal-close-button" type="button" :disabled="quickBindSaving || bulkBindSaving" @click="closeEmailPicker">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="card-key-email-picker-body">
            <aside class="card-key-email-picker-groups">
              <div class="card-key-email-picker-source-switch">
                <button class="card-key-email-picker-source-button" :class="{ active: emailPickerSource === 'imap' }" type="button" @click="switchEmailPickerSource('imap')">IMAP邮箱分组</button>
                <button class="card-key-email-picker-source-button" :class="{ active: emailPickerSource === 'outlook' }" type="button" @click="switchEmailPickerSource('outlook')">微软邮箱分组</button>
              </div>
              <div ref="emailPickerGroupListRef" class="card-key-email-picker-group-list">
                <button
                  v-for="group in emailPickerVisibleGroups"
                  :key="`${emailPickerSource}-${group.id}`"
                  class="card-key-email-picker-group"
                  :class="emailPickerActiveGroupID === group.id ? 'card-key-email-picker-group-active' : ''"
                  :style="{ paddingLeft: `${12 + group.level * 18}px` }"
                  type="button"
                  @click="selectEmailPickerGroup(group)"
                >
                  <span class="card-key-email-picker-group-name-viewport">
                    <span class="card-key-email-picker-group-name-inner" :style="{ transform: `translateX(-${emailPickerGroupNameScrollX}px)` }">
                      <span class="flex h-4 w-4 shrink-0 items-center justify-center" @click.stop="toggleEmailPickerGroupExpanded(group)">
                        <ChevronDown v-if="group.hasChildren && emailPickerExpandedGroupIDs.includes(group.id)" class="h-4 w-4" />
                        <ChevronRight v-else-if="group.hasChildren" class="h-4 w-4" />
                        <Folder v-else class="h-4 w-4" />
                      </span>
                      <span class="card-key-email-picker-group-label" :title="group.name">{{ group.name }}</span>
                    </span>
                  </span>
                  <span v-if="!group.hasChildren" class="card-key-email-picker-count">{{ emailPickerGroupCount(group) }}</span>
                  <span v-else class="card-key-email-picker-count-placeholder"></span>
                </button>
                <div v-if="emailPickerGroupsLoading" class="card-key-email-picker-empty">加载中...</div>
                <div v-else-if="emailPickerGroups.length === 0" class="card-key-email-picker-empty">暂无分组</div>
              </div>
              <div v-if="emailPickerGroupNameScrollMax > 0" class="card-key-email-picker-group-horizontal-scroll">
                <div class="card-key-email-picker-group-horizontal-scroll-body" @scroll="syncEmailPickerGroupNameScroll">
                  <div :style="{ width: `calc(100% + ${emailPickerGroupNameScrollMax}px)` }"></div>
                </div>
              </div>
            </aside>
            <section class="card-key-email-picker-accounts">
              <div class="card-key-email-picker-search search-clear-field">
                <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
                <input v-model.trim="emailPickerSearch" class="input search-clear-input h-9 pl-10 text-sm" type="text" placeholder="搜索邮箱账号或备注" />
                <button v-if="emailPickerSearch" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="emailPickerSearch = ''">
                  <X class="h-3.5 w-3.5" />
                </button>
              </div>
              <div class="card-key-email-picker-table-wrap">
                <table class="card-key-email-picker-table" :class="{ 'card-key-email-picker-table-batch': isBatchEmailPicker }">
                  <colgroup>
                    <col v-if="isBatchEmailPicker" class="card-key-email-picker-col-select" />
                    <col class="card-key-email-picker-col-email" />
                    <col class="card-key-email-picker-col-remark" />
                    <col v-if="!isBatchEmailPicker" class="card-key-email-picker-col-action" />
                  </colgroup>
                  <thead>
                    <tr>
                      <th v-if="isBatchEmailPicker" class="card-key-email-picker-select-col">
                        <input
                          :checked="allEmailPickerPageSelected"
                          :indeterminate.prop="someEmailPickerPageSelected && !allEmailPickerPageSelected"
                          :disabled="bulkBindSaving || emailPickerAccounts.length === 0"
                          type="checkbox"
                          @change="toggleAllEmailPickerPage"
                        />
                      </th>
                      <th>邮箱账号</th>
                      <th>备注</th>
                      <th v-if="!isBatchEmailPicker">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="account in emailPickerAccounts" :key="`${emailPickerSource}-${account.id}`">
                      <td v-if="isBatchEmailPicker" class="card-key-email-picker-select-col">
                        <input
                          :checked="bulkEmailSelectionKeySet.has(emailAccountSelectionKey(account))"
                          :disabled="bulkBindSaving"
                          type="checkbox"
                          @change="toggleEmailAccountSelection(account, $event)"
                        />
                      </td>
                      <td :title="account.email">{{ account.email }}</td>
                      <td :title="account.remark">{{ account.remark || '-' }}</td>
                      <td v-if="!isBatchEmailPicker">
                        <button class="card-key-email-select-button" type="button" :disabled="quickBindSaving" @click="selectEmailAccount(account)">
                          <Check class="h-4 w-4" />
                          <span>{{ quickBindSaving && emailPickerMode === 'quickBind' ? '绑定中...' : '选择' }}</span>
                        </button>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div v-if="showEmailPickerAccountsLoading" class="card-key-email-picker-empty">加载中...</div>
                <div v-else-if="emailPickerAccounts.length === 0" class="card-key-email-picker-empty">暂无邮箱</div>
              </div>
              <div class="card-key-email-picker-footer">
                <div class="card-key-email-picker-pagination-row">
                  <PaginationBar
                    :page="emailPickerPage"
                    :pages="emailPickerTotalPages"
                    :page-size="emailPickerPageSize"
                    :page-size-options="emailPickerPageSizeOptions"
                    :total="emailPickerTotal"
                    :disabled="showEmailPickerAccountsLoading"
                    @page-change="changeEmailPickerPage"
                    @page-size-change="selectEmailPickerPageSize"
                  />
                </div>
                <div v-if="isBatchEmailPicker" class="card-key-email-picker-batch-row">
                  <div class="card-key-email-picker-batch-summary">
                    已选卡密 {{ selectedCount }} 个，已选邮箱 {{ bulkEmailSelections.length }} 个，将绑定 {{ bulkBindPairCount }} 个
                  </div>
                  <div class="card-key-email-picker-batch-actions">
                    <button class="btn btn-secondary" type="button" :disabled="bulkBindSaving" @click="closeEmailPicker">取消</button>
                    <button class="btn btn-primary" type="button" :disabled="bulkBindSaving || bulkBindPairCount === 0" @click="saveBatchBoundEmails">
                      {{ bulkBindSaving ? '绑定中...' : '绑定邮箱' }}
                    </button>
                  </div>
                </div>
              </div>
            </section>
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to="body">
      <div v-if="showBatchModal" class="mail-modal-mask center-mail-modal">
        <div class="mail-form-modal card-key-form-modal scrollable-mail-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900">
          <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-dark-700">
            <h3 class="text-base font-bold text-gray-900 dark:text-white">批量生成卡密</h3>
            <button class="modal-close-button" type="button" :disabled="batchSaving" @click="showBatchModal = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <div class="mail-modal-body mail-modal-scroll-body grid gap-3 p-5">
            <label>
              <span class="input-label">分组归属 *</span>
              <select v-model.number="batchForm.group_id" class="input">
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
            </label>
            <label>
              <span class="input-label">生成数量</span>
              <input v-model.number="batchForm.count" class="input" type="number" min="1" max="1000" step="1" />
            </label>
            <label>
              <span class="input-label">使用次数</span>
              <input v-model.number="batchForm.usage_limit" class="input" type="number" min="1" step="1" />
            </label>
            <label>
              <span class="input-label">收取 X 天内的邮件</span>
              <input v-model.number="batchForm.mail_days" class="input" type="number" min="0" step="1" placeholder="不填或 0 为不限制天数" />
            </label>
            <label>
              <span class="input-label">关键词邮件</span>
              <input v-model.trim="batchForm.mail_keyword" class="input" placeholder="请输入关键词" />
            </label>
            <label>
              <span class="input-label">备注</span>
              <input v-model.trim="batchForm.remark" class="input" placeholder="请输入备注" />
            </label>
          </div>
          <div class="flex shrink-0 justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700">
            <button class="btn btn-secondary" type="button" :disabled="batchSaving" @click="showBatchModal = false">取消</button>
            <button class="btn btn-primary" type="button" :disabled="batchSaving" @click="saveBatchCardKeys">
              {{ batchSaving ? '生成中...' : '批量生成卡密' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.card-key-page-layout {
  display: flex;
  height: calc(100vh - 8rem);
  min-height: 0;
  max-height: calc(100vh - 8rem);
  overflow: hidden;
}

.card-key-group-panel {
  display: flex;
  flex-direction: column;
  width: 224px;
  height: 100%;
  min-height: 0;
  max-height: 100%;
  overflow: hidden;
}

.card-key-group-panel > div:first-child {
  padding: 0.8rem 1rem;
}

.card-key-group-panel h2 {
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

.card-key-panel {
  display: flex;
  height: 100%;
  min-height: 0;
  max-height: 100%;
  flex-direction: column;
  overflow: hidden;
}

.mail-account-toolbar {
  flex-shrink: 0;
  padding: 0.75rem 1rem;
}

.mail-account-body {
  display: flex;
  min-height: 0;
  flex: 1;
}

.card-key-table-wrap {
  --card-key-col-select: 3.1rem;
  --card-key-col-key: 12rem;
  --card-key-col-usage: 5.4rem;
  --card-key-col-used-at: 10rem;
  --card-key-col-bound-email: 18rem;
  --card-key-col-mail-filter: 10rem;
  --card-key-col-created: 10rem;
  --card-key-col-remark: 9rem;
  --card-key-col-actions: 12.5rem;
  --card-key-table-min-width: calc(
    var(--card-key-col-select) +
    var(--card-key-col-key) +
    var(--card-key-col-usage) +
    var(--card-key-col-used-at) +
    var(--card-key-col-bound-email) +
    var(--card-key-col-mail-filter) +
    var(--card-key-col-created) +
    var(--card-key-col-remark) +
    var(--card-key-col-actions)
  );
  --card-key-table-divider: rgb(148 163 184 / 0.08);
  width: 100%;
  max-width: 100%;
  min-height: 0;
  flex: 1;
  height: 100%;
  overflow-x: auto;
  overflow-y: auto;
}

.card-key-footer {
  flex-shrink: 0;
}

.dark .card-key-table-wrap {
  --card-key-table-divider: rgb(148 163 184 / 0.12);
}

.card-key-table {
  width: max(100%, var(--card-key-table-min-width));
  min-width: var(--card-key-table-min-width);
  table-layout: fixed;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 0.8125rem;
}

.card-key-table th,
.card-key-table td {
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.65rem 0.8rem !important;
  text-align: left;
  vertical-align: middle;
}

.card-key-table td {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-key-table td.sticky-col {
  overflow: visible;
}

.dark .card-key-table th,
.dark .card-key-table td {
  border-bottom-color: rgb(51 65 85);
}

.card-key-table th {
  border-right: 1px solid var(--card-key-table-divider);
}

.card-key-table th:last-child {
  border-right: 0;
}

.card-key-table th {
  position: sticky;
  top: 0;
  z-index: 15;
  background: rgb(249 250 251);
  color: rgb(100 116 139);
  text-align: center;
  white-space: nowrap;
}

.dark .card-key-table th {
  background: rgb(30 41 59);
  color: rgb(203 213 225);
}

.card-key-col-select { width: var(--card-key-col-select); }
.card-key-col-key { width: var(--card-key-col-key); }
.card-key-col-usage { width: var(--card-key-col-usage); }
.card-key-col-used-at { width: var(--card-key-col-used-at); }
.card-key-col-bound-email { width: var(--card-key-col-bound-email); }
.card-key-col-mail-filter { width: var(--card-key-col-mail-filter); }
.card-key-col-remark { width: var(--card-key-col-remark); }
.card-key-col-created { width: var(--card-key-col-created); }
.card-key-col-actions { width: var(--card-key-col-actions); }

.card-key-select-col {
  width: var(--card-key-col-select) !important;
  text-align: center;
}

.card-key-select-col input {
  height: 0.95rem;
  width: 0.95rem;
  accent-color: rgb(20 184 166);
}

.mail-sort-button {
  display: inline-flex;
  width: 100%;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  color: inherit;
  font: inherit;
}

.mail-sort-button svg {
  transition: transform 0.18s ease;
}

.mail-sort-label {
  white-space: nowrap;
}

.card-key-value-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.45rem;
}

.card-key-value-cell span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-key-copy-button {
  display: inline-flex;
  height: 1.5rem;
  width: 1.5rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.35rem;
  color: rgb(100 116 139);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.card-key-copy-button:hover {
  background: rgb(14 165 233 / 0.1);
  color: rgb(2 132 199);
}

.card-key-bound-email-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.35rem;
}

.card-key-bound-email-link {
  min-width: 0;
  overflow: hidden;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: inherit;
  font: inherit;
  transition: color 0.15s ease;
}

.card-key-bound-email-link:hover {
  color: rgb(20 184 166);
  text-decoration: underline;
  text-underline-offset: 3px;
}

.card-key-bound-email-copy-button {
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

.card-key-bound-email-copy-button:hover {
  background: rgb(20 184 166 / 0.12);
  color: rgb(20 184 166);
}

.card-key-mail-filter-cell {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.12rem;
  line-height: 1.18;
}

.card-key-mail-filter-cell span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-key-mail-filter-days {
  font-size: 0.75rem;
  font-weight: 400;
  color: rgb(100 116 139);
}

.dark .card-key-mail-filter-days {
  color: rgb(148 163 184);
}

.card-key-mail-filter-keyword {
  font-size: 0.75rem;
  color: rgb(100 116 139);
}

.dark .card-key-mail-filter-keyword {
  color: rgb(148 163 184);
}

.mail-action-primary,
.mail-action-secondary,
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

.mail-action-refresh {
  border: 1px solid rgb(148 163 184 / 0.45);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
}

.mail-action-primary:disabled,
.mail-action-secondary:disabled,
.mail-action-refresh:disabled {
  cursor: not-allowed;
  opacity: 0.62;
}

.dark .mail-action-secondary {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: rgb(94 234 212);
}

.dark .mail-action-refresh {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
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
  color: white;
  font-weight: 700;
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

.card-key-disabled-button {
  background: rgb(100 116 139);
}

.mail-toolbar-batch-danger {
  background: rgb(239 68 68);
}

.mail-toolbar-batch-danger:hover {
  background: rgb(220 38 38);
}

.mail-action-primary:hover,
.mail-action-secondary:hover,
.mail-action-refresh:hover {
  transform: translateY(-1px);
}

.mail-toolbar-batch-button:hover,
.mail-toolbar-batch-danger:hover {
  transform: translateY(-1px);
}

.mail-action-primary:disabled:hover,
.mail-action-secondary:disabled:hover,
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

.mail-row-actions {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
}

.card-key-actions-cell {
  padding-left: 0.75rem !important;
  padding-right: 0.75rem !important;
}

.mail-row-action-button {
  display: inline-flex;
  width: 1.9rem;
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

.sticky-col-right {
  position: sticky;
  right: 0;
  z-index: 10;
  width: var(--card-key-col-actions);
  min-width: var(--card-key-col-actions);
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
  background: var(--card-key-table-divider);
  pointer-events: none;
}

thead .sticky-col-right {
  z-index: 25;
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
  color: white;
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
}

html.dark .page-size-trigger {
  border-color: rgb(20 184 166 / 0.75);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.page-size-menu {
  position: absolute;
  bottom: calc(100% + 0.5rem);
  left: 0;
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
}

html.dark .page-size-option {
  color: rgb(226 232 240);
}

html.dark .page-size-option:hover,
html.dark .page-size-option-active {
  background: rgb(51 65 85);
  color: rgb(45 212 191);
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

.page-jump-button {
  display: inline-flex;
  min-width: 2.25rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(203 213 225);
  border-radius: 0 0.5rem 0.5rem 0;
  background: rgb(248 250 252);
  color: rgb(71 85 105);
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

:global(.mail-modal-mask) {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  overflow: auto;
  background: rgb(0 0 0 / 0.45);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
  padding: 1rem;
}

:global(.center-mail-modal) {
  align-items: center;
  justify-content: center;
}

:global(.scrollable-mail-modal) {
  display: flex;
  flex-direction: column;
}

:global(.card-key-form-modal) {
  width: min(36rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
}

:global(.mail-modal-scroll-body) {
  min-height: 0;
  overflow-y: auto;
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
  transition: background-color 0.15s ease, color 0.15s ease;
}

:global(.modal-close-button:hover) {
  background: rgb(226 232 240 / 0.9);
  color: rgb(71 85 105);
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

.card-key-readonly-input:disabled {
  cursor: not-allowed;
  background: rgb(241 245 249);
  color: rgb(71 85 105);
  opacity: 1;
}

html.dark .card-key-readonly-input:disabled {
  background: rgb(15 23 42 / 0.72);
  color: rgb(203 213 225);
}

.card-key-bound-email-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: 0.5rem;
  align-items: center;
}

.card-key-bound-email-input {
  cursor: default;
}

.card-key-email-action,
.card-key-email-clear,
.card-key-email-select-button {
  display: inline-flex;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  border-radius: 0.625rem;
  padding: 0 0.75rem;
  font-size: 0.8125rem;
  font-weight: 700;
  white-space: nowrap;
  transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease, opacity 0.15s ease;
}

.card-key-email-action {
  border: 1px solid rgb(20 184 166 / 0.38);
  background: rgb(240 253 250);
  color: rgb(13 148 136);
}

.card-key-email-action:hover {
  border-color: rgb(20 184 166 / 0.65);
  background: rgb(204 251 241);
}

.card-key-email-clear {
  border: 1px solid rgb(220 38 38);
  background: rgb(220 38 38);
  color: white;
}

.card-key-email-clear:hover:not(:disabled) {
  border-color: rgb(185 28 28);
  background: rgb(185 28 28);
  color: white;
}

.card-key-email-clear:disabled {
  cursor: not-allowed;
  opacity: 0.52;
}

html.dark .card-key-email-action {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: rgb(94 234 212);
}

html.dark .card-key-email-action:hover {
  background: rgb(20 184 166 / 0.2);
}

html.dark .card-key-email-clear {
  border-color: rgb(248 113 113 / 0.78);
  background: rgb(185 28 28);
  color: white;
}

html.dark .card-key-email-clear:hover:not(:disabled) {
  border-color: rgb(252 165 165 / 0.85);
  background: rgb(153 27 27);
  color: white;
}

.card-key-email-picker-mask {
  z-index: 1100;
}

.card-key-email-picker-modal {
  display: flex;
  width: min(66rem, calc(100vw - 2rem));
  height: min(52rem, calc(100vh - 2rem));
  max-height: calc(100vh - 2rem);
  flex-direction: column;
}

.card-key-email-picker-body {
  display: grid;
  min-height: 0;
  flex: 1;
  overflow: hidden;
  grid-template-columns: 224px minmax(0, 1fr);
}

.card-key-email-picker-groups {
  display: flex;
  min-width: 0;
  flex-direction: column;
  border-right: 1px solid rgb(226 232 240);
  background: rgb(248 250 252);
}

html.dark .card-key-email-picker-groups {
  border-right-color: rgb(51 65 85);
  background: rgb(15 23 42 / 0.42);
}

.card-key-email-picker-section-title {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.65rem 0.9rem;
  font-size: 0.8125rem;
  font-weight: 700;
  color: rgb(71 85 105);
}

.card-key-email-picker-source-switch {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 0.4rem;
  flex-shrink: 0;
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.7rem;
}

.card-key-email-picker-source-button {
  display: inline-flex;
  min-width: 0;
  height: 2.35rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(20 184 166 / 0.42);
  border-radius: 0.65rem;
  background: rgb(255 255 255 / 0.82);
  padding: 0 0.3rem;
  font-size: 0.72rem;
  font-weight: 800;
  line-height: 1;
  white-space: nowrap;
  word-break: keep-all;
  color: rgb(100 116 139);
  transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
}

.card-key-email-picker-source-button:hover {
  border-color: rgb(20 184 166 / 0.75);
  background: rgb(204 251 241);
  color: rgb(15 118 110);
}

.card-key-email-picker-source-button.active {
  border-color: rgb(20 184 166);
  background: rgb(20 184 166);
  color: white;
  box-shadow: 0 0 0 1px rgb(94 234 212 / 0.35), 0 8px 18px rgb(20 184 166 / 0.22);
}

html.dark .card-key-email-picker-section-title {
  border-bottom-color: rgb(51 65 85);
  color: rgb(203 213 225);
}

html.dark .card-key-email-picker-source-switch {
  border-bottom-color: rgb(51 65 85);
}

html.dark .card-key-email-picker-source-button {
  border-color: rgb(94 234 212 / 0.4);
  background: rgb(15 23 42 / 0.85);
  color: rgb(226 232 240);
}

html.dark .card-key-email-picker-source-button:hover {
  border-color: rgb(45 212 191 / 0.8);
  background: rgb(20 184 166 / 0.2);
  color: rgb(153 246 228);
}

html.dark .card-key-email-picker-source-button.active {
  border-color: rgb(45 212 191);
  background: rgb(20 184 166);
  color: rgb(255 255 255);
  box-shadow: 0 0 0 1px rgb(94 234 212 / 0.6), 0 0 18px rgb(20 184 166 / 0.28);
}

.card-key-email-picker-group-list {
  min-height: 0;
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 0.65rem;
}

.card-key-email-picker-group-horizontal-scroll {
  flex-shrink: 0;
  padding: 0 0.75rem 0.35rem;
}

.card-key-email-picker-group-horizontal-scroll-body {
  width: 100%;
  height: 0.75rem;
  overflow-x: auto;
  overflow-y: hidden;
}

.card-key-email-picker-group-horizontal-scroll-body > div {
  height: 1px;
}

.card-key-email-picker-group-horizontal-scroll-body::-webkit-scrollbar {
  height: 0.55rem;
}

.card-key-email-picker-group-horizontal-scroll-body::-webkit-scrollbar-track {
  background: transparent;
}

.card-key-email-picker-group-horizontal-scroll-body::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.55);
}

.dark .card-key-email-picker-group-horizontal-scroll-body::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.75);
}

.card-key-email-picker-group {
  display: flex;
  width: 100%;
  min-height: 2.15rem;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  border-radius: 0.75rem;
  padding-bottom: 0.45rem;
  padding-right: 0.7rem;
  padding-top: 0.45rem;
  text-align: left;
  font-size: 0.8125rem;
  color: rgb(75 85 99);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.card-key-email-picker-group:hover,
.card-key-email-picker-group-active {
  background: rgb(240 253 250);
  color: rgb(13 148 136);
}

html.dark .card-key-email-picker-group {
  color: rgb(203 213 225);
}

html.dark .card-key-email-picker-group:hover,
html.dark .card-key-email-picker-group-active {
  background: rgb(51 65 85);
  color: rgb(94 234 212);
}

.card-key-email-picker-group-name-viewport {
  display: block;
  min-width: 0;
  flex: 1;
  overflow: hidden;
}

.card-key-email-picker-group-name-inner {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 0.5rem;
  transition: transform 0.06s linear;
  will-change: transform;
}

.card-key-email-picker-group-label {
  display: block;
  flex: 0 0 auto;
  min-width: 0;
  overflow: visible;
  white-space: nowrap;
  word-break: keep-all;
  line-height: 1.15;
}

.card-key-email-picker-count,
.card-key-email-picker-count-placeholder {
  flex-shrink: 0;
  min-width: 1.45rem;
}

.card-key-email-picker-count {
  border-radius: 999px;
  background: rgb(226 232 240);
  padding: 0.05rem 0.4rem;
  text-align: center;
  font-size: 0.75rem;
  color: rgb(71 85 105);
}

html.dark .card-key-email-picker-count {
  background: rgb(15 23 42);
  color: rgb(148 163 184);
}

.card-key-email-picker-accounts {
  display: flex;
  min-height: 0;
  min-width: 0;
  flex-direction: column;
  overflow: hidden;
}

.card-key-email-picker-search {
  position: relative;
  flex-shrink: 0;
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.75rem;
}

html.dark .card-key-email-picker-search {
  border-bottom-color: rgb(51 65 85);
}

.card-key-email-picker-table-wrap {
  position: relative;
  min-height: 0;
  flex: 1;
  overflow: auto;
}

.card-key-email-picker-table {
  width: 100%;
  min-width: 35rem;
  table-layout: fixed;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 0.8125rem;
}

.card-key-email-picker-table th,
.card-key-email-picker-table td {
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.7rem 0.8rem;
  text-align: left;
  vertical-align: middle;
}

html.dark .card-key-email-picker-table th,
html.dark .card-key-email-picker-table td {
  border-bottom-color: rgb(51 65 85);
}

.card-key-email-picker-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: rgb(249 250 251);
  color: rgb(100 116 139);
  font-weight: 700;
  text-align: center;
}

.card-key-email-picker-table th:not(:last-child) {
  border-right: 1px solid rgb(226 232 240);
}

html.dark .card-key-email-picker-table th {
  background: rgb(30 41 59);
  border-right-color: rgb(51 65 85);
  color: rgb(203 213 225);
}

.card-key-email-picker-table td {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: rgb(75 85 99);
}

html.dark .card-key-email-picker-table td {
  color: rgb(203 213 225);
}

.card-key-email-picker-col-select {
  width: 3.2rem;
}

.card-key-email-picker-select-col {
  width: 3.2rem;
  padding-left: 0.55rem !important;
  padding-right: 0.55rem !important;
  text-align: center !important;
}

.card-key-email-picker-select-col input {
  height: 0.95rem;
  width: 0.95rem;
  margin: 0 auto;
  accent-color: rgb(20 184 166);
}

.card-key-email-picker-col-email {
  width: 45%;
}

.card-key-email-picker-col-remark {
  width: 35%;
}

.card-key-email-picker-col-action {
  width: 7rem;
}

.card-key-email-picker-table-batch {
  min-width: 32rem;
}

.card-key-email-picker-table-batch .card-key-email-picker-col-email {
  width: 50%;
}

.card-key-email-picker-table-batch .card-key-email-picker-col-remark {
  width: auto;
}

.card-key-email-select-button {
  height: 2rem;
  border: 1px solid rgb(20 184 166 / 0.38);
  background: rgb(240 253 250);
  color: rgb(13 148 136);
}

.card-key-email-select-button:hover {
  border-color: rgb(20 184 166 / 0.68);
  background: rgb(204 251 241);
}

.card-key-email-select-button:disabled {
  cursor: not-allowed;
  opacity: 0.62;
}

html.dark .card-key-email-select-button {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: rgb(94 234 212);
}

.card-key-email-picker-empty {
  padding: 1.35rem;
  text-align: center;
  font-size: 0.8125rem;
  font-weight: 700;
  color: rgb(148 163 184);
}

.card-key-email-picker-footer {
  display: flex;
  flex-shrink: 0;
  align-items: stretch;
  flex-direction: column;
  gap: 0.65rem;
  border-top: 1px solid rgb(226 232 240);
  padding: 0.7rem 0.9rem;
  font-size: 0.8125rem;
  color: rgb(100 116 139);
}

.card-key-email-picker-pagination-row {
  min-width: 0;
  width: 100%;
}

.card-key-email-picker-pagination-row :deep(.pagination-bar) {
  width: 100%;
  min-width: 0;
}

.card-key-email-picker-batch-row {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.card-key-email-picker-batch-summary {
  min-width: 0;
  font-weight: 700;
  color: rgb(71 85 105);
  line-height: 1.35;
}

.card-key-email-picker-batch-actions {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
}

html.dark .card-key-email-picker-batch-summary {
  color: rgb(203 213 225);
}

html.dark .card-key-email-picker-footer {
  border-top-color: rgb(51 65 85);
  color: rgb(148 163 184);
}

@media (max-width: 1023px) {
  .card-key-page-layout {
    flex-direction: column;
    height: auto;
    min-height: 0;
    max-height: none;
    overflow: visible;
  }

  .card-key-group-panel {
    width: 100%;
    min-height: 0;
    max-height: 16rem;
  }

  .mail-group-list {
    max-height: 13rem;
  }

  .card-key-panel {
    height: calc(100vh - 24rem);
    min-height: 22rem;
    max-height: calc(100vh - 12rem);
  }
}

@media (max-width: 767px) {
  .mail-account-toolbar {
    align-items: stretch;
    padding: 0.75rem;
  }

  .card-key-actions {
    width: 100%;
  }

  .mail-action-primary,
  .mail-action-secondary,
  .mail-action-refresh,
  .mail-toolbar-batch-button,
  .mail-toolbar-batch-danger {
    flex: 1 1 9rem;
    padding: 0 0.75rem;
    font-size: 0.8125rem;
  }

  .card-key-footer {
    align-items: stretch;
    flex-direction: column;
    gap: 0.75rem;
  }

  .card-key-footer > div {
    flex-wrap: wrap;
  }

  :global(.mail-modal-mask) {
    padding: 0.75rem;
  }

  :global(.card-key-form-modal) {
    width: calc(100vw - 1.5rem);
    max-height: calc(100vh - 1.5rem);
  }

  :global(.mail-modal-body) {
    grid-template-columns: minmax(0, 1fr) !important;
  }

  :global(.mail-modal-body > .md\:col-span-2) {
    grid-column: auto !important;
  }

  .card-key-bound-email-row {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }

  .card-key-bound-email-input {
    grid-column: 1 / -1;
  }

  .card-key-email-picker-modal {
    width: calc(100vw - 1.5rem);
    max-height: calc(100vh - 1.5rem);
  }

  .card-key-email-picker-body {
    min-height: 0;
    grid-template-columns: minmax(0, 1fr);
  }

  .card-key-email-picker-groups {
    max-height: 13rem;
    border-right: 0;
    border-bottom: 1px solid rgb(226 232 240);
  }

  html.dark .card-key-email-picker-groups {
    border-bottom-color: rgb(51 65 85);
  }

  .card-key-email-picker-table-wrap {
    max-height: 18rem;
  }

  .card-key-email-picker-footer {
    align-items: stretch;
    flex-direction: column;
  }

  .card-key-email-picker-batch-summary {
    white-space: normal;
  }

  .card-key-email-picker-batch-actions {
    justify-content: flex-end;
  }

}

@media (min-width: 1600px) {
  .card-key-page-layout {
    max-height: calc(100vh - 8rem);
  }
}

@media (max-width: 640px) {
  .card-key-group-panel {
    max-height: 12rem;
    border-radius: 0.875rem;
  }

  .card-key-panel {
    height: auto;
    min-height: 0;
    max-height: none;
    border-radius: 0.875rem;
  }

  .card-key-table-wrap {
    min-height: 18rem;
    max-height: none;
  }

  .mail-account-toolbar .search-clear-field {
    width: 100% !important;
    flex: 1 1 100% !important;
  }

  .card-key-footer .pagination-bar {
    width: 100%;
  }

  :global(.mail-modal-mask) {
    align-items: stretch;
    padding: 0.5rem;
  }

  :global(.card-key-form-modal),
  .card-key-email-picker-modal {
    width: calc(100vw - 1rem);
    max-height: calc(100svh - 1rem);
    border-radius: 0.875rem;
  }

  .card-key-email-picker-modal {
    height: calc(100svh - 1rem);
  }

  .card-key-email-picker-groups {
    max-height: 11rem;
  }

  .card-key-email-picker-table-wrap {
    min-height: 14rem;
    max-height: none;
  }
}

@media (max-width: 420px) {
  .mail-action-primary,
  .mail-action-secondary,
  .mail-action-refresh,
  .mail-toolbar-batch-button,
  .mail-toolbar-batch-danger {
    flex-basis: 100%;
    width: 100%;
  }

  .card-key-bound-email-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .card-key-email-picker-batch-actions {
    width: 100%;
    justify-content: stretch;
  }

  .card-key-email-picker-batch-actions > button {
    flex: 1 1 0;
  }
}
</style>
