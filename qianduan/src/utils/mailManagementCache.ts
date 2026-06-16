import type { MailAccount, MailAccountListParams, MailAccountListResponse, MailServer } from '../api/mailAccounts'
import type { MailGroup } from '../api/mailGroups'

export const mailManagementCacheKey = 'mail_management_cache_v1'

const mailAccountPageCacheLimit = 80
const fallbackPageSize = 20

export type MailAccountPageCacheEntry = MailAccountListResponse & {
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

export type MailManagementCache = {
  groups?: MailGroup[]
  accounts?: MailAccount[]
  accountPages?: Record<string, MailAccountPageCacheEntry>
  servers?: MailServer[]
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

type MailAccountPageCacheParams = Omit<MailAccountListParams, 'sort_by'> & {
  sort_by?: string
}

function normalizeSortOrder(value: unknown): 'asc' | 'desc' {
  return value === 'desc' ? 'desc' : 'asc'
}

function normalizePageSize(value: unknown) {
  const size = Number(value)
  return Number.isFinite(size) && size > 0 ? size : fallbackPageSize
}

export function mailAccountPageCacheKey(params: MailAccountPageCacheParams = {}) {
  return [
    Number(params.group_id) || 0,
    params.search || '',
    Number(params.page) || 1,
    normalizePageSize(params.page_size),
    params.sort_by || 'created_at',
    normalizeSortOrder(params.sort_order),
  ].map((part) => encodeURIComponent(String(part))).join('|')
}

export function readMailManagementCache(): MailManagementCache | null {
  try {
    const value = JSON.parse(localStorage.getItem(mailManagementCacheKey) || 'null')
    return value && typeof value === 'object' ? value as MailManagementCache : null
  } catch {
    return null
  }
}

export function writeMailManagementCache(cache: MailManagementCache) {
  try {
    localStorage.setItem(mailManagementCacheKey, JSON.stringify(cache))
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

function responseFromAccounts(accounts: MailAccount[], pagination?: MailManagementCache['pagination']): MailAccountListResponse {
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

function normalizePageCacheEntry(value: unknown): MailAccountPageCacheEntry | null {
  if (!value || typeof value !== 'object') return null
  const entry = value as Partial<MailAccountPageCacheEntry>
  if (!Array.isArray(entry.items)) return null
  const query = entry.query && typeof entry.query === 'object' ? entry.query : {} as MailAccountPageCacheEntry['query']
  const page = Number(entry.page || query.page) || 1
  const pageSize = normalizePageSize(entry.page_size || query.page_size)
  const total = Number(entry.total) || entry.items.length
  return {
    items: entry.items as MailAccount[],
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

export function normalizeMailAccountPageCache(value: unknown) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const cache: Record<string, MailAccountPageCacheEntry> = {}
  for (const [key, rawEntry] of Object.entries(value as Record<string, unknown>)) {
    const entry = normalizePageCacheEntry(rawEntry)
    if (entry) cache[key] = entry
  }
  return pruneMailAccountPageCache(cache)
}

export function pruneMailAccountPageCache(cache: Record<string, MailAccountPageCacheEntry>) {
  const entries = Object.entries(cache)
    .filter(([, entry]) => Array.isArray(entry.items))
    .sort((a, b) => (Number(b[1].updated_at) || 0) - (Number(a[1].updated_at) || 0))
    .slice(0, mailAccountPageCacheLimit)
  return Object.fromEntries(entries)
}

export function buildMailAccountPageCacheEntry(response: MailAccountListResponse, params: MailAccountPageCacheParams): MailAccountPageCacheEntry {
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

export function rememberMailAccountPage(cache: Record<string, MailAccountPageCacheEntry>, response: MailAccountListResponse, params: MailAccountPageCacheParams) {
  const entry = buildMailAccountPageCacheEntry(response, params)
  return pruneMailAccountPageCache({
    ...cache,
    [mailAccountPageCacheKey(entry.query)]: entry,
  })
}

function findLoosePage(cache: Record<string, MailAccountPageCacheEntry>, params: MailAccountPageCacheParams) {
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

export function findMailAccountPage(cache: MailManagementCache | null, params: MailAccountPageCacheParams): MailAccountListResponse | null {
  if (!cache) return null
  const pages = normalizeMailAccountPageCache(cache.accountPages)
  const exact = pages[mailAccountPageCacheKey(params)]
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

export function updateMailManagementCacheGroups(groups: MailGroup[]) {
  const cache = readMailManagementCache() || {}
  writeMailManagementCache({
    ...cache,
    groups,
    updated_at: Date.now(),
  })
}

export function updateMailManagementCachePage(response: MailAccountListResponse, params: MailAccountPageCacheParams) {
  const cache = readMailManagementCache() || {}
  const pageCache = rememberMailAccountPage(normalizeMailAccountPageCache(cache.accountPages), response, params)
  writeMailManagementCache({
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
