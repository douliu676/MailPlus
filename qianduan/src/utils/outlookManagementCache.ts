import type { OutlookAccount, OutlookAccountListParams, OutlookAccountListResponse, OutlookGroup } from '../api/outlookAccounts'

export const outlookManagementCacheKey = 'outlook_management_cache_v1'

const outlookAccountPageCacheLimit = 80
const fallbackPageSize = 20

export type OutlookAccountPageCacheEntry = OutlookAccountListResponse & {
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

export type OutlookManagementCache = {
  groups?: OutlookGroup[]
  accounts?: OutlookAccount[]
  accountPages?: Record<string, OutlookAccountPageCacheEntry>
  pagination?: {
    page?: number
    page_size?: number
    total?: number
    pages?: number
  }
  query?: {
    group_id?: number
    search?: string
    sort_by?: string
    sort_order?: string
  }
  updated_at?: number
}

type OutlookAccountPageCacheParams = Omit<OutlookAccountListParams, 'sort_by'> & {
  sort_by?: string
}

function normalizeSortOrder(value: unknown): 'asc' | 'desc' {
  return value === 'desc' ? 'desc' : 'asc'
}

function normalizePageSize(value: unknown) {
  const size = Number(value)
  return Number.isFinite(size) && size > 0 ? size : fallbackPageSize
}

export function outlookAccountPageCacheKey(params: OutlookAccountPageCacheParams = {}) {
  return [
    Number(params.group_id) || 0,
    params.search || '',
    Number(params.page) || 1,
    normalizePageSize(params.page_size),
    params.sort_by || 'created_at',
    normalizeSortOrder(params.sort_order),
  ].map((part) => encodeURIComponent(String(part))).join('|')
}

export function readOutlookManagementCache(): OutlookManagementCache | null {
  try {
    const value = JSON.parse(localStorage.getItem(outlookManagementCacheKey) || 'null')
    return value && typeof value === 'object' ? value as OutlookManagementCache : null
  } catch {
    return null
  }
}

export function writeOutlookManagementCache(cache: OutlookManagementCache) {
  try {
    localStorage.setItem(outlookManagementCacheKey, JSON.stringify(cache))
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

function responseFromAccounts(accounts: OutlookAccount[], pagination?: OutlookManagementCache['pagination']): OutlookAccountListResponse {
  const page = Number(pagination?.page) || 1
  const pageSize = normalizePageSize(pagination?.page_size)
  const pageItems = accounts.length > pageSize ? accounts.slice((page - 1) * pageSize, page * pageSize) : accounts
  const normal = pageItems.filter((item) => ['active', 'normal', 'ok', 'success'].includes(String(item.status || '').toLowerCase())).length
  const total = Number.isFinite(Number(pagination?.total)) ? Number(pagination?.total) : accounts.length
  return {
    items: pageItems,
    total,
    page,
    page_size: pageSize,
    pages: Number(pagination?.pages) || Math.ceil(total / Math.max(1, pageSize)),
    normal,
    error: Math.max(0, total - normal),
  }
}

function normalizePageCacheEntry(value: unknown): OutlookAccountPageCacheEntry | null {
  if (!value || typeof value !== 'object') return null
  const entry = value as Partial<OutlookAccountPageCacheEntry>
  if (!Array.isArray(entry.items)) return null
  const query = entry.query && typeof entry.query === 'object' ? entry.query : {} as OutlookAccountPageCacheEntry['query']
  const page = Number(entry.page || query.page) || 1
  const pageSize = normalizePageSize(entry.page_size || query.page_size)
  const total = Number(entry.total) || entry.items.length
  return {
    items: entry.items as OutlookAccount[],
    total,
    page,
    page_size: pageSize,
    pages: Number(entry.pages) || Math.ceil(total / Math.max(1, pageSize)),
    normal: Number(entry.normal) || 0,
    error: Number(entry.error) || 0,
    query: {
      group_id: Number(query.group_id) || 0,
      search: String(query.search || ''),
      page,
      page_size: pageSize,
      sort_by: String(query.sort_by || 'created_at'),
      sort_order: normalizeSortOrder(query.sort_order),
    },
    updated_at: Number(entry.updated_at) || Date.now(),
  }
}

export function normalizeOutlookAccountPageCache(value: unknown) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const cache: Record<string, OutlookAccountPageCacheEntry> = {}
  for (const [key, rawEntry] of Object.entries(value as Record<string, unknown>)) {
    const entry = normalizePageCacheEntry(rawEntry)
    if (entry) cache[key] = entry
  }
  return pruneOutlookAccountPageCache(cache)
}

export function pruneOutlookAccountPageCache(cache: Record<string, OutlookAccountPageCacheEntry>) {
  const entries = Object.entries(cache)
    .filter(([, entry]) => Array.isArray(entry.items))
    .sort((a, b) => (Number(b[1].updated_at) || 0) - (Number(a[1].updated_at) || 0))
    .slice(0, outlookAccountPageCacheLimit)
  return Object.fromEntries(entries)
}

export function buildOutlookAccountPageCacheEntry(response: OutlookAccountListResponse, params: OutlookAccountPageCacheParams): OutlookAccountPageCacheEntry {
  const groupID = Number(params.group_id) || 0
  const page = Number(response.page || params.page) || 1
  const pageSize = normalizePageSize(response.page_size || params.page_size)
  return {
    ...response,
    page,
    page_size: pageSize,
    query: {
      group_id: groupID,
      search: params.search || '',
      page,
      page_size: pageSize,
      sort_by: params.sort_by || 'created_at',
      sort_order: normalizeSortOrder(params.sort_order),
    },
    updated_at: Date.now(),
  }
}

export function rememberOutlookAccountPage(cache: Record<string, OutlookAccountPageCacheEntry>, response: OutlookAccountListResponse, params: OutlookAccountPageCacheParams) {
  const entry = buildOutlookAccountPageCacheEntry(response, params)
  return pruneOutlookAccountPageCache({
    ...cache,
    [outlookAccountPageCacheKey(entry.query)]: entry,
  })
}

function findLoosePage(cache: Record<string, OutlookAccountPageCacheEntry>, params: OutlookAccountPageCacheParams) {
  const targetGroupID = Number(params.group_id) || 0
  const targetSearch = params.search || ''
  const targetPage = Number(params.page) || 1
  const targetPageSize = normalizePageSize(params.page_size)
  return Object.values(cache)
    .filter((entry) => (
      Number(entry.query?.group_id) === targetGroupID
      && String(entry.query?.search || '') === targetSearch
      && Number(entry.query?.page || entry.page) === targetPage
      && normalizePageSize(entry.query?.page_size || entry.page_size) === targetPageSize
    ))
    .sort((a, b) => (Number(b.updated_at) || 0) - (Number(a.updated_at) || 0))[0] || null
}

export function findOutlookAccountPage(cache: OutlookManagementCache | null, params: OutlookAccountPageCacheParams): OutlookAccountListResponse | null {
  if (!cache) return null
  const pages = normalizeOutlookAccountPageCache(cache.accountPages)
  const exact = pages[outlookAccountPageCacheKey(params)]
  if (exact) return exact

  const loose = findLoosePage(pages, params)
  if (loose) return loose

  if (!Array.isArray(cache.accounts)) return null
  const cachedGroupID = Number(cache.query?.group_id) || 0
  const cachedSearch = String(cache.query?.search || '')
  const cachedPage = Number(cache.pagination?.page) || 1
  const cachedPageSize = normalizePageSize(cache.pagination?.page_size)
  if (
    cachedGroupID === (Number(params.group_id) || 0)
    && cachedSearch === (params.search || '')
    && cachedPage === (Number(params.page) || 1)
    && cachedPageSize === normalizePageSize(params.page_size)
  ) {
    return responseFromAccounts(cache.accounts, cache.pagination)
  }
  return null
}

export function updateOutlookManagementCacheGroups(groups: OutlookGroup[]) {
  const cache = readOutlookManagementCache() || {}
  writeOutlookManagementCache({
    ...cache,
    groups,
    updated_at: Date.now(),
  })
}

export function updateOutlookManagementCachePage(response: OutlookAccountListResponse, params: OutlookAccountPageCacheParams) {
  const cache = readOutlookManagementCache() || {}
  const pageCache = rememberOutlookAccountPage(normalizeOutlookAccountPageCache(cache.accountPages), response, params)
  writeOutlookManagementCache({
    ...cache,
    accounts: response.items,
    accountPages: pageCache,
    pagination: {
      page: response.page,
      page_size: response.page_size,
      total: response.total,
      pages: response.pages,
    },
    query: {
      group_id: Number(params.group_id) || 0,
      search: params.search || '',
      sort_by: params.sort_by || 'created_at',
      sort_order: normalizeSortOrder(params.sort_order),
    },
    updated_at: Date.now(),
  })
}
