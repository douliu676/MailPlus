<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { Check, CheckCircle2, ChevronDown, ChevronLeft, ChevronRight, CircleHelp, CircleOff, Download, Globe2, MoreHorizontal, Pencil, Play, Plus, RefreshCw, Search, Trash2, Upload, X, XCircle } from 'lucide-vue-next'
import PaginationBar from '../components/PaginationBar.vue'
import { useAppStore } from '../stores/app'
import { getAdminSettings } from '../api/adminSettings'
import { createProxyNode, deleteProxyNode, getProxyRuntime, getProxySettings, importProxyNodes, listProxyNodes, testProxyNode, updateProxyNode, updateProxySettings, type ProxyNode, type ProxyNodeListParams, type ProxyNodeListResponse, type ProxyProtocol, type ProxyRuntime, type ProxySettings, type SaveProxyNodePayload } from '../api/proxySystem'

const appStore = useAppStore()
const queryClient = useQueryClient()
const fallbackTablePageSize = 10
const fallbackTablePageSizeOptions = [10, 20, 50, 100]
const pageSizeStorageKey = 'proxy_nodes_page_size'
const proxySortStorageKey = 'proxy_nodes_sort'
const proxySystemCacheKey = 'proxy_system_cache_v1'
const protocols: Array<{ label: string; value: ProxyProtocol }> = [
  { label: 'SOCKS5', value: 'socks5' },
  { label: 'HTTP', value: 'http' },
  { label: 'VMess', value: 'vmess' },
  { label: 'VLESS', value: 'vless' },
]
const allowedProxyProtocols = protocols.map((item) => item.value)

const nodes = ref<ProxyNode[]>([])
const nodeOptions = ref<ProxyNode[]>([])
const nodeTotal = ref(0)
const nodePages = ref(0)
const nodeNormal = ref(0)
const nodeError = ref(0)
const nodeStatsTotal = ref(0)
const nodeStatsNormal = ref(0)
const nodeStatsError = ref(0)
const settings = ref<ProxySettings | null>(null)
const runtime = ref<ProxyRuntime | null>(null)
const loading = ref(false)
const saving = ref(false)
const settingsSaving = ref(false)
const settingsControlsLocked = ref(false)
const testingID = ref<number | null>(null)
const editingID = ref<number | null>(null)
const searchQuery = ref('')
const showNodeModal = ref(false)
const moreActionsOpen = ref(false)
const importModalOpen = ref(false)
const exportModalOpen = ref(false)
const importFileName = ref('')
const importFile = ref<File | null>(null)
const importFileInputRef = ref<HTMLInputElement | null>(null)
const importingProxyData = ref(false)
const exportingProxyData = ref(false)
const batchTesting = ref(false)
const batchDeleting = ref(false)
const selectedNodeIDs = ref<number[]>([])
const pageSize = ref(readPersistedPageSize() || fallbackTablePageSize)
const pageSizeOptions = ref<number[]>(fallbackTablePageSizeOptions)
const pageSizeDropdownOpen = ref(false)
const pageJump = ref('')
const currentPage = ref(1)
const proxyTableWrapRef = ref<HTMLElement | null>(null)
let settingsSaveQueued = false
let proxyAutoRefreshEnabled = false
let proxySearchTimer: number | undefined
let nodeRequestID = 0

type PaginationItem = { key: string; type: 'page'; page: number } | { key: string; type: 'ellipsis' }
type ProxyNodeSortKey = NonNullable<ProxyNodeListParams['sort_by']>
const proxySortKeys: ProxyNodeSortKey[] = ['name', 'address', 'status', 'latency', 'created_at']
type ProxyScopeKey = 'imap' | 'outlook'
type ImportedProxyNode = {
  name: string
  protocol: ProxyProtocol
  address: string
  port: number
  username?: string
  password?: string
  uuid?: string
  alter_id?: number
  security?: string
  encryption?: string
  transport?: string
  tls?: string
  sni?: string
  path?: string
  host_header?: string
  flow?: string
  fingerprint?: string
  public_key?: string
  short_id?: string
  spider_x?: string
}
type ExportedProxyNode = SaveProxyNodePayload & Pick<ProxyNode, 'created_at'>

let lastAppliedImportURL = ''
let lastImportErrorURL = ''
const proxyScopeActionLabels: Record<ProxyScopeKey, string> = {
  imap: 'IMAP邮箱代理',
  outlook: '微软邮箱代理',
}

function restoreProxySystemCache() {
  try {
    const value = JSON.parse(localStorage.getItem(proxySystemCacheKey) || 'null')
    if (!value || typeof value !== 'object') return
    if (Array.isArray(value.nodes)) {
      nodes.value = value.nodes
    }
    if (Array.isArray(value.node_options)) {
      nodeOptions.value = value.node_options
    }
    if (value.list && typeof value.list === 'object') {
      nodeTotal.value = Number(value.list.total) || nodes.value.length
      nodePages.value = Number(value.list.pages) || Math.max(1, Math.ceil(nodeTotal.value / pageSize.value))
      nodeNormal.value = Number(value.list.normal) || 0
      nodeError.value = Number(value.list.error) || 0
      nodeStatsTotal.value = Number(value.stats?.total) || nodeTotal.value
      nodeStatsNormal.value = Number(value.stats?.normal) || nodeNormal.value
      nodeStatsError.value = Number(value.stats?.error) || nodeError.value
    } else {
      nodeTotal.value = nodes.value.length
      nodePages.value = Math.max(1, Math.ceil(nodeTotal.value / pageSize.value))
      nodeNormal.value = nodes.value.filter((node) => node.status === 'normal').length
      nodeError.value = nodes.value.filter((node) => node.status === 'error').length
      nodeStatsTotal.value = nodeTotal.value
      nodeStatsNormal.value = nodeNormal.value
      nodeStatsError.value = nodeError.value
    }
    if (value.settings && typeof value.settings === 'object') {
      settings.value = value.settings
      syncSettingsForm(value.settings)
    }
    if (value.runtime && typeof value.runtime === 'object') {
      runtime.value = value.runtime
    }
    if (value.pagination && typeof value.pagination === 'object') {
      currentPage.value = Number(value.pagination.page) || currentPage.value
      pageSize.value = Number(value.pagination.page_size) || pageSize.value
      if (Array.isArray(value.pagination.page_size_options)) {
        pageSizeOptions.value = normalizePageSizeOptions(value.pagination.page_size_options, pageSize.value)
      }
    }
    if (value.query && typeof value.query === 'object') {
      searchQuery.value = String(value.query.search || '')
    }
    const cachedResponse = currentProxyNodeListSnapshot()
    queryClient.setQueryData(proxyNodeQueryKey.value, cachedResponse)
  } catch {
    // Ignore stale cache; live requests will repopulate it.
  }
}

function saveProxySystemCache() {
  try {
    localStorage.setItem(
      proxySystemCacheKey,
      JSON.stringify({
        nodes: nodes.value,
        node_options: nodeOptions.value,
        settings: settings.value,
        runtime: runtime.value,
        list: {
          total: nodeTotal.value,
          pages: nodePages.value,
          normal: nodeNormal.value,
          error: nodeError.value,
        },
        stats: {
          total: nodeStatsTotal.value,
          normal: nodeStatsNormal.value,
          error: nodeStatsError.value,
        },
        pagination: {
          page: currentPage.value,
          page_size: pageSize.value,
          page_size_options: pageSizeOptions.value,
        },
        query: {
          search: searchQuery.value,
          sort_by: proxySortKey.value,
          sort_order: proxySortOrder.value,
        },
        updated_at: Date.now(),
      })
    )
  } catch {
    // Ignore storage quota errors; live data remains available.
  }
}

function readPersistedPageSize() {
  const value = Number(localStorage.getItem(pageSizeStorageKey))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function readPersistedProxySort() {
  try {
    const value = JSON.parse(localStorage.getItem(proxySortStorageKey) || '{}')
    const key = proxySortKeys.includes(value.key) ? value.key as ProxyNodeSortKey : 'created_at'
    const order = value.order === 'desc' ? 'desc' : 'asc'
    return { key, order }
  } catch {
    return { key: 'created_at' as ProxyNodeSortKey, order: 'asc' as const }
  }
}

function normalizePageSizeOptions(options: number[] | undefined, defaultPageSize: number) {
  const values = [...(options || []), defaultPageSize]
    .map((value) => Number(value))
    .filter((value) => Number.isFinite(value) && value > 0)
  const normalized = Array.from(new Set(values.length ? values : fallbackTablePageSizeOptions)).sort((a, b) => a - b)
  return normalized.length ? normalized : fallbackTablePageSizeOptions
}

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

const form = reactive({
  import_url: '',
  name: '',
  protocol: 'socks5' as ProxyProtocol,
  address: '',
  port: 1080,
  username: '',
  password: '',
  uuid: '',
  alter_id: 0,
  security: 'auto',
  encryption: 'none',
  transport: 'tcp',
  tls: '',
  sni: '',
  path: '',
  host_header: '',
  flow: '',
  fingerprint: '',
  public_key: '',
  short_id: '',
  spider_x: '',
  enabled: true,
  remark: '',
})

const settingsForm = reactive({
  imap: { enabled: false, proxy_node_id: 0 },
  outlook: { enabled: false, proxy_node_id: 0 },
})
const persistedProxySort = readPersistedProxySort()
const proxySortKey = ref<ProxyNodeSortKey>(persistedProxySort.key)
const proxySortOrder = ref<'asc' | 'desc'>(persistedProxySort.order)

const proxyNodeQueryKey = computed(() => [
  'proxy-nodes',
  searchQuery.value.trim(),
  currentPage.value,
  pageSize.value,
  proxySortKey.value,
  proxySortOrder.value,
])

function currentProxyNodeListParams(page = currentPage.value, size = pageSize.value): ProxyNodeListParams {
  return {
    search: searchQuery.value.trim() || undefined,
    page,
    page_size: size,
    sort_by: proxySortKey.value,
    sort_order: proxySortOrder.value,
  }
}

function currentProxyNodeListSnapshot(): ProxyNodeListResponse {
  return {
    items: nodes.value,
    total: nodeTotal.value,
    page: currentPage.value,
    page_size: pageSize.value,
    pages: nodePages.value,
    normal: nodeNormal.value,
    error: nodeError.value,
    stats_total: nodeStatsTotal.value,
    stats_normal: nodeStatsNormal.value,
    stats_error: nodeStatsError.value,
  }
}

function applyProxyNodeListResponse(response: ProxyNodeListResponse) {
  nodes.value = response.items
  nodeTotal.value = response.total
  nodePages.value = response.pages
  nodeNormal.value = response.normal
  nodeError.value = response.error
  nodeStatsTotal.value = Number.isFinite(Number(response.stats_total)) ? Number(response.stats_total) : response.total
  nodeStatsNormal.value = Number.isFinite(Number(response.stats_normal)) ? Number(response.stats_normal) : response.normal
  nodeStatsError.value = Number.isFinite(Number(response.stats_error)) ? Number(response.stats_error) : response.error
  currentPage.value = response.page || currentPage.value
  if (response.page_size && response.page_size !== pageSize.value) {
    pageSize.value = response.page_size
  }
}

function mergeProxyNodeLists(...lists: ProxyNode[][]) {
  const map = new Map<number, ProxyNode>()
  for (const list of lists) {
    for (const node of list) {
      map.set(node.id, node)
    }
  }
  return Array.from(map.values()).sort(compareProxyNodeCreatedAsc)
}

const nodeOptionList = computed(() => mergeProxyNodeLists(nodeOptions.value, nodes.value))

function proxyCreatedTimeValue(node: Pick<ProxyNode, 'created_at' | 'id'>) {
  const time = Date.parse(node.created_at || '')
  return Number.isFinite(time) ? time : 0
}

function compareProxyNodeCreatedAsc(a: ProxyNode, b: ProxyNode) {
  return proxyCreatedTimeValue(a) - proxyCreatedTimeValue(b) || a.id - b.id
}

function scrollProxyTableToTop() {
  if (proxyTableWrapRef.value) {
    proxyTableWrapRef.value.scrollTop = 0
  }
}

function isXrayNode(node: Pick<ProxyNode, 'protocol'> | null | undefined) {
  return node?.protocol === 'vmess' || node?.protocol === 'vless'
}

const isXrayProtocol = computed(() => form.protocol === 'vmess' || form.protocol === 'vless')
const normalCount = computed(() => nodeStatsNormal.value)
const errorCount = computed(() => nodeStatsError.value)
const xrayLoaded = computed(() => Boolean(runtime.value && !runtime.value.xray_error))
const xrayProcessRunning = computed(() => xrayLoaded.value && Boolean(runtime.value?.running))
const plainProxyRunning = computed(() => proxyScopePlainRunning(settingsForm.imap) || proxyScopePlainRunning(settingsForm.outlook))
const proxyCoreRunning = computed(() => xrayProcessRunning.value || plainProxyRunning.value)
const xrayLoadText = computed(() => {
  return xrayLoaded.value ? '已正常加载' : '未正常加载'
})
const xrayRunText = computed(() => {
  return proxyCoreRunning.value ? '已运行' : '未运行'
})
const xrayLoadClass = computed(() => {
  return xrayLoaded.value ? 'proxy-stat-success' : 'proxy-stat-danger'
})
const xrayRunClass = computed(() => {
  return proxyCoreRunning.value ? 'proxy-stat-success' : 'proxy-stat-danger'
})
const imapProxyRunning = computed(() => proxyScopeRunning(settingsForm.imap))
const outlookProxyRunning = computed(() => proxyScopeRunning(settingsForm.outlook))

const totalPages = computed(() => Math.max(nodePages.value, 1))
const paginationItems = computed(() => buildPaginationItems(currentPage.value, totalPages.value))
const pageStart = computed(() => (nodeTotal.value === 0 ? 0 : (currentPage.value - 1) * pageSize.value + 1))
const pageEnd = computed(() => Math.min(currentPage.value * pageSize.value, nodeTotal.value))
const pagedNodes = computed(() => nodes.value)
const normalizedPageSizeOptions = computed(() => normalizePageSizeOptions(pageSizeOptions.value, pageSize.value))
const pagedNodeIDs = computed(() => pagedNodes.value.map((node) => node.id))
const selectedNodes = computed(() => nodes.value.filter((node) => selectedNodeIDs.value.includes(node.id)))
const allPagedNodesSelected = computed(() => pagedNodeIDs.value.length > 0 && pagedNodeIDs.value.every((id) => selectedNodeIDs.value.includes(id)))
const exportTargetCount = computed(() => (selectedNodeIDs.value.length > 0 ? selectedNodes.value.length : nodeTotal.value))

function requestProxyNodes(resetPage = false) {
  if (!proxyAutoRefreshEnabled) {
    saveProxySystemCache()
    return
  }
  if (resetPage) {
    scrollProxyTableToTop()
  }
  if (resetPage && currentPage.value !== 1) {
    currentPage.value = 1
    return
  }
  void loadNodes()
}

watch(searchQuery, () => {
  if (!proxyAutoRefreshEnabled) {
    saveProxySystemCache()
    return
  }
  window.clearTimeout(proxySearchTimer)
  proxySearchTimer = window.setTimeout(() => requestProxyNodes(true), 300)
})

watch(pageSize, () => {
  saveProxySystemCache()
  requestProxyNodes(true)
})

watch(totalPages, (pages) => {
  if (currentPage.value > pages) {
    currentPage.value = pages
  }
})

watch(currentPage, () => {
  saveProxySystemCache()
  if (proxyAutoRefreshEnabled) {
    void loadNodes()
  }
})

watch(nodes, () => {
  const validIDs = new Set(nodes.value.map((node) => node.id))
  selectedNodeIDs.value = selectedNodeIDs.value.filter((id) => validIDs.has(id))
})

async function refreshAll() {
  loading.value = true
  try {
    await Promise.all([loadNodes(), loadSettings(), loadRuntime()])
    await loadNodeOptions()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '刷新代理数据失败')
  } finally {
    loading.value = false
  }
}

async function loadNodes() {
  const requestID = ++nodeRequestID
  const queryKey = proxyNodeQueryKey.value
  const cached = queryClient.getQueryData<ProxyNodeListResponse>(queryKey)
  if (cached) {
    applyProxyNodeListResponse(cached)
    saveProxySystemCache()
  }

  const response = await queryClient.fetchQuery({
    queryKey,
    queryFn: () => listProxyNodes(currentProxyNodeListParams()),
    staleTime: 0,
  })
  if (requestID !== nodeRequestID) return
  if (response.items.length === 0 && response.total > 0 && response.pages > 0 && currentPage.value > response.pages) {
    currentPage.value = response.pages
    return
  }
  applyProxyNodeListResponse(response)
  saveProxySystemCache()
}

async function loadSettings(syncForm = !settingsSaving.value) {
  settings.value = await getProxySettings()
  if (syncForm) syncSettingsForm(settings.value)
  saveProxySystemCache()
}

function selectedProxyNodeIDs() {
  return [settingsForm.imap.proxy_node_id, settingsForm.outlook.proxy_node_id]
    .map((id) => Number(id) || 0)
    .filter((id, index, ids) => id > 0 && ids.indexOf(id) === index)
}

async function loadNodeOptions() {
  const response = await listProxyNodes({ page: 1, page_size: 500, sort_by: 'created_at', sort_order: 'asc' })
  let options = response.items
  const optionIDs = new Set(options.map((node) => node.id))
  const missingSelectedIDs = selectedProxyNodeIDs().filter((id) => !optionIDs.has(id))
  if (missingSelectedIDs.length > 0) {
    const selectedResponse = await listProxyNodes({
      ids: missingSelectedIDs,
      page: 1,
      page_size: missingSelectedIDs.length,
      sort_by: 'created_at',
      sort_order: 'asc',
    })
    options = mergeProxyNodeLists(options, selectedResponse.items)
  }
  nodeOptions.value = options
  saveProxySystemCache()
}

async function loadRuntime() {
  runtime.value = await getProxyRuntime()
  saveProxySystemCache()
}

function syncSettingsForm(nextSettings: ProxySettings) {
  settingsForm.imap.enabled = Boolean(nextSettings.imap.enabled)
  settingsForm.imap.proxy_node_id = nextSettings.imap.proxy_node_id || 0
  settingsForm.outlook.enabled = Boolean(nextSettings.outlook.enabled)
  settingsForm.outlook.proxy_node_id = nextSettings.outlook.proxy_node_id || 0
}

function currentSettingsPayload() {
  return {
    imap: { enabled: Boolean(settingsForm.imap.enabled), proxy_node_id: Number(settingsForm.imap.proxy_node_id) || 0 },
    outlook: { enabled: Boolean(settingsForm.outlook.enabled), proxy_node_id: Number(settingsForm.outlook.proxy_node_id) || 0 },
  }
}

type ProxyScopeForm = { enabled: boolean; proxy_node_id: number }

function formatProxyTestLatency(value: number) {
  const latency = Math.max(0, Math.round(Number(value) || 0))
  return `${latency}ms`
}

async function refreshProxyStateAfterSettingsChange() {
  await Promise.all([loadNodes(), loadNodeOptions(), loadRuntime()])
}

async function persistCurrentProxySettings() {
  const savedSettings = await updateProxySettings(currentSettingsPayload())
  settings.value = savedSettings
  return savedSettings
}

function selectedProxyNode(scope: ProxyScopeForm) {
  return nodeOptionList.value.find((item) => item.id === Number(scope.proxy_node_id)) || null
}

function proxyNodeDisplayName(node: ProxyNode | null | undefined) {
  if (!node) return '未选择节点'
  return `${node.name} - ${node.protocol.toUpperCase()}`
}

function proxyScopeReady(scope: ProxyScopeForm) {
  if (!scope.enabled || !scope.proxy_node_id) return false
  const node = selectedProxyNode(scope)
  return Boolean(node?.enabled && node.status !== 'error')
}

function proxyScopeUsesPlainProxy(scope: ProxyScopeForm) {
  if (!scope.enabled || !scope.proxy_node_id) return false
  const node = selectedProxyNode(scope)
  return Boolean(node && !isXrayNode(node))
}

function proxyScopePlainRunning(scope: ProxyScopeForm) {
  return proxyScopeUsesPlainProxy(scope) && proxyScopeReady(scope)
}

function proxyScopeRunning(scope: ProxyScopeForm) {
  if (!proxyScopeReady(scope)) return false
  const node = selectedProxyNode(scope)
  if (isXrayNode(node)) return xrayProcessRunning.value
  return true
}

async function loadTablePageSettings() {
  try {
    const settings = await getAdminSettings()
    const defaultPageSize = Number(settings.table_default_page_size || fallbackTablePageSize)
    const nextDefaultPageSize = Number.isFinite(defaultPageSize) && defaultPageSize > 0 ? defaultPageSize : fallbackTablePageSize
    const nextPageSizeOptions = normalizePageSizeOptions(settings.table_page_size_options, nextDefaultPageSize)
    const persistedPageSize = readPersistedPageSize()

    pageSizeOptions.value = nextPageSizeOptions
    pageSize.value = persistedPageSize > 0 && nextPageSizeOptions.includes(persistedPageSize)
      ? persistedPageSize
      : nextDefaultPageSize
    saveProxySystemCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取表格分页设置失败')
    pageSizeOptions.value = normalizePageSizeOptions(fallbackTablePageSizeOptions, fallbackTablePageSize)
    pageSize.value = readPersistedPageSize() || fallbackTablePageSize
    saveProxySystemCache()
  }
}

function changePage(page: number) {
  const valid = Math.max(1, Math.min(page, totalPages.value))
  if (valid === currentPage.value) return
  currentPage.value = valid
  scrollProxyTableToTop()
}

function toggleProxySort(key: ProxyNodeSortKey) {
  if (proxySortKey.value === key) {
    proxySortOrder.value = proxySortOrder.value === 'asc' ? 'desc' : 'asc'
  } else {
    proxySortKey.value = key
    proxySortOrder.value = 'asc'
  }
  localStorage.setItem(proxySortStorageKey, JSON.stringify({ key: proxySortKey.value, order: proxySortOrder.value }))
  saveProxySystemCache()
  requestProxyNodes(true)
}

function jumpToPage() {
  const page = Number(pageJump.value)
  if (!Number.isFinite(page)) return
  changePage(page)
  pageJump.value = ''
}

function selectPageSize(size: number) {
  pageSize.value = size
  localStorage.setItem(pageSizeStorageKey, String(size))
  pageSizeDropdownOpen.value = false
}

function closePageSizeMenu(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (!target.closest('[data-page-size-select]')) {
    pageSizeDropdownOpen.value = false
  }
  if (!target.closest('[data-proxy-more-actions]')) {
    moreActionsOpen.value = false
  }
}

onMounted(async () => {
  restoreProxySystemCache()
  await loadTablePageSettings()
  proxyAutoRefreshEnabled = true
  void refreshAll()
  document.addEventListener('click', closePageSizeMenu)
})

onBeforeUnmount(() => {
  window.clearTimeout(proxySearchTimer)
  document.removeEventListener('click', closePageSizeMenu)
})

function resetForm() {
  editingID.value = null
  lastAppliedImportURL = ''
  lastImportErrorURL = ''
  Object.assign(form, {
    import_url: '',
    name: '',
    protocol: 'socks5',
    address: '',
    port: 1080,
    username: '',
    password: '',
    uuid: '',
    alter_id: 0,
    security: 'auto',
    encryption: 'none',
    transport: 'tcp',
    tls: '',
    sni: '',
    path: '',
    host_header: '',
    flow: '',
    fingerprint: '',
    public_key: '',
    short_id: '',
    spider_x: '',
    enabled: true,
    remark: '',
  })
}

function openCreateNodeModal() {
  resetForm()
  showNodeModal.value = true
}

function closeNodeModal() {
  if (saving.value) return
  showNodeModal.value = false
  resetForm()
}

function editNode(node: ProxyNode) {
  editingID.value = node.id
  lastAppliedImportURL = ''
  lastImportErrorURL = ''
  Object.assign(form, {
    import_url: '',
    name: node.name,
    protocol: node.protocol,
    address: node.address,
    port: node.port,
    username: node.username,
    password: node.password,
    uuid: node.uuid,
    alter_id: node.alter_id,
    security: node.security || 'auto',
    encryption: node.encryption || 'none',
    transport: node.transport || 'tcp',
    tls: node.tls,
    sni: node.sni,
    path: node.path,
    host_header: node.host_header,
    flow: node.flow,
    fingerprint: node.fingerprint,
    public_key: node.public_key,
    short_id: node.short_id,
    spider_x: node.spider_x,
    enabled: node.enabled,
    remark: node.remark,
  })
  showNodeModal.value = true
}

function importedText(value: unknown) {
  return String(value ?? '').trim()
}

function importedNumber(value: unknown, fallback = 0) {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}

function normalizeProxyImportURL(raw: string) {
  let value = raw.trim()
  const wrappers: Array<[string, string]> = [
    ['(', ')'],
    ['（', '）'],
    ['[', ']'],
    ['【', '】'],
    ['<', '>'],
    ['《', '》'],
    ['"', '"'],
    ["'", "'"],
  ]
  let changed = true
  while (changed) {
    changed = false
    for (const [open, close] of wrappers) {
      if (value.startsWith(open) && value.endsWith(close)) {
        value = value.slice(open.length, value.length - close.length).trim()
        changed = true
      }
    }
  }
  return value
}

function decodeURLPart(value: string) {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function decodeBase64Text(value: string) {
  const cleaned = value.trim().replace(/\s/g, '')
  const candidates = [cleaned, cleaned.replace(/-/g, '+').replace(/_/g, '/')]
  for (const candidate of candidates) {
    try {
      const padded = candidate.padEnd(candidate.length + ((4 - candidate.length % 4) % 4), '=')
      const binary = window.atob(padded)
      const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
      return new TextDecoder().decode(bytes)
    } catch {
      // Try the next base64 variant.
    }
  }
  throw new Error('VMess 链接解析失败')
}

function decodePlainProxyAuth(username: string, password: string) {
  if (!username || password) {
    return { username, password }
  }
  try {
    const decoded = decodeBase64Text(username)
    const separatorIndex = decoded.indexOf(':')
    if (separatorIndex >= 0) {
      return {
        username: decoded.slice(0, separatorIndex),
        password: decoded.slice(separatorIndex + 1),
      }
    }
  } catch {
    // Plain user names are still valid; only SOCKS share links use base64 auth.
  }
  return { username, password }
}

function parseVMessImportURL(raw: string): ImportedProxyNode {
  let payload: Record<string, unknown>
  try {
    payload = JSON.parse(decodeBase64Text(raw.replace(/^vmess:\/\//i, '')))
  } catch {
    throw new Error('VMess 链接不是有效配置')
  }
  const address = importedText(payload.add)
  const port = importedNumber(payload.port)
  const uuid = importedText(payload.id)
  if (!address || port <= 0 || !uuid) {
    throw new Error('VMess 链接缺少地址、端口或 UUID')
  }
  const name = importedText(payload.ps) || `vmess://${address}`
  return {
    name,
    protocol: 'vmess',
    address,
    port,
    uuid,
    alter_id: importedNumber(payload.aid),
    security: importedText(payload.scy) || 'auto',
    transport: importedText(payload.net).toLowerCase() || 'tcp',
    tls: importedText(payload.tls).toLowerCase(),
    sni: importedText(payload.sni),
    path: importedText(payload.path),
    host_header: importedText(payload.host),
    fingerprint: importedText(payload.fp),
  }
}

function parseVLESSImportURL(raw: string): ImportedProxyNode {
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    throw new Error('VLESS 链接解析失败')
  }
  const address = url.hostname
  const port = Number(url.port)
  const uuid = decodeURLPart(url.username || '')
  if (!address || !Number.isFinite(port) || port <= 0 || !uuid) {
    throw new Error('VLESS 链接缺少地址、端口或 UUID')
  }
  const query = url.searchParams
  return {
    name: decodeURLPart(url.hash.slice(1)) || `vless://${address}`,
    protocol: 'vless',
    address,
    port,
    uuid,
    encryption: query.get('encryption') || 'none',
    transport: (query.get('type') || 'tcp').toLowerCase(),
    tls: (query.get('security') || '').toLowerCase(),
    sni: query.get('sni') || '',
    path: query.get('path') || '',
    host_header: query.get('host') || '',
    flow: query.get('flow') || '',
    fingerprint: query.get('fp') || '',
    public_key: query.get('pbk') || '',
    short_id: query.get('sid') || '',
    spider_x: query.get('spx') || '',
  }
}

function normalizePlainImportProtocol(protocol: string): ProxyProtocol {
  const value = protocol.replace(':', '').toLowerCase()
  if (value === 'socks' || value === 'socks5') return 'socks5'
  if (value === 'http' || value === 'https') return 'http'
  throw new Error('仅支持 vmess、vless、socks5、http 导入链接')
}

function parsePlainProxyImportURL(raw: string): ImportedProxyNode {
  const value = normalizeProxyImportURL(raw)
  const match = value.match(/^([a-z][a-z0-9+.-]*):\/\/([\s\S]*)$/i)
  if (!match) {
    throw new Error('代理链接解析失败')
  }
  const protocol = normalizePlainImportProtocol(match[1])
  let body = match[2].trim()
  let fragment = ''
  const hashIndex = body.indexOf('#')
  if (hashIndex >= 0) {
    fragment = body.slice(hashIndex + 1)
    body = body.slice(0, hashIndex)
  }

  let authText = ''
  let endpointText = body
  const atIndex = body.lastIndexOf('@')
  if (atIndex >= 0) {
    authText = body.slice(0, atIndex)
    endpointText = body.slice(atIndex + 1)
  }
  const pathIndex = endpointText.search(/[/?]/)
  if (pathIndex >= 0) {
    endpointText = endpointText.slice(0, pathIndex)
  }

  let address = ''
  let portText = ''
  if (endpointText.startsWith('[')) {
    const closeIndex = endpointText.indexOf(']')
    if (closeIndex >= 0) {
      address = endpointText.slice(1, closeIndex)
      if (endpointText[closeIndex + 1] === ':') {
        portText = endpointText.slice(closeIndex + 2)
      }
    }
  } else {
    const portIndex = endpointText.lastIndexOf(':')
    if (portIndex >= 0) {
      address = endpointText.slice(0, portIndex)
      portText = endpointText.slice(portIndex + 1)
    }
  }

  address = decodeURLPart(address.trim())
  const port = Number(portText)
  if (!address || !Number.isFinite(port) || port <= 0) {
    throw new Error('代理链接缺少地址或端口')
  }
  let username = ''
  let password = ''
  if (authText) {
    const passwordIndex = authText.indexOf(':')
    if (passwordIndex >= 0) {
      username = decodeURLPart(authText.slice(0, passwordIndex))
      password = decodeURLPart(authText.slice(passwordIndex + 1))
    } else {
      username = decodeURLPart(authText)
    }
  }
  const auth = protocol === 'socks5' ? decodePlainProxyAuth(username, password) : { username, password }
  return {
    name: decodeURLPart(fragment) || `${protocol}://${address}`,
    protocol,
    address,
    port,
    username: auth.username,
    password: auth.password,
  }
}

function parseProxyImportURL(raw: string): ImportedProxyNode {
  const value = normalizeProxyImportURL(raw)
  const lower = value.toLowerCase()
  if (lower.startsWith('vmess://')) return parseVMessImportURL(value)
  if (lower.startsWith('vless://')) return parseVLESSImportURL(value)
  if (lower.startsWith('socks5://') || lower.startsWith('socks://') || lower.startsWith('http://') || lower.startsWith('https://')) {
    return parsePlainProxyImportURL(value)
  }
  throw new Error('仅支持 vmess、vless、socks5、http 导入链接')
}

function applyImportedProxyNode(node: ImportedProxyNode, rawURL: string) {
  Object.assign(form, {
    name: node.name,
    protocol: node.protocol,
    address: node.address,
    port: node.port,
    username: node.username || '',
    password: node.password || '',
    uuid: node.uuid || '',
    alter_id: node.alter_id || 0,
    security: node.security || 'auto',
    encryption: node.encryption || 'none',
    transport: node.transport || 'tcp',
    tls: node.tls || '',
    sni: node.sni || '',
    path: node.path || '',
    host_header: node.host_header || '',
    flow: node.flow || '',
    fingerprint: node.fingerprint || '',
    public_key: node.public_key || '',
    short_id: node.short_id || '',
    spider_x: node.spider_x || '',
  })
  lastAppliedImportURL = rawURL
}

function parseImportURLFromField(showError = false) {
  const rawURL = form.import_url.trim()
  if (!rawURL) {
    lastAppliedImportURL = ''
    lastImportErrorURL = ''
    return true
  }
  if (rawURL === lastAppliedImportURL) return true
  try {
    applyImportedProxyNode(parseProxyImportURL(rawURL), rawURL)
    lastImportErrorURL = ''
    return true
  } catch (error) {
    if (showError && rawURL !== lastImportErrorURL) {
      appStore.showError(error instanceof Error ? error.message : '导入链接解析失败')
      lastImportErrorURL = rawURL
    }
    return false
  }
}

function handleImportURLInput() {
  parseImportURLFromField(false)
}

function handleImportURLPaste() {
  window.setTimeout(() => parseImportURLFromField(true), 0)
}

function buildPayload(): SaveProxyNodePayload {
  return {
    import_url: '',
    name: form.name.trim(),
    protocol: form.protocol,
    address: form.address.trim(),
    port: Number(form.port) || 0,
    username: form.username.trim(),
    password: form.password,
    uuid: form.uuid.trim(),
    alter_id: Number(form.alter_id) || 0,
    security: form.security.trim(),
    encryption: form.encryption.trim(),
    transport: form.transport.trim(),
    tls: form.tls.trim(),
    sni: form.sni.trim(),
    path: form.path.trim(),
    host_header: form.host_header.trim(),
    flow: form.flow.trim(),
    fingerprint: form.fingerprint.trim(),
    public_key: form.public_key.trim(),
    short_id: form.short_id.trim(),
    spider_x: form.spider_x.trim(),
    enabled: form.enabled,
    remark: form.remark.trim(),
  }
}

async function submitNode() {
  if (!parseImportURLFromField(true)) return
  saving.value = true
  try {
    if (editingID.value) {
      await updateProxyNode(editingID.value, buildPayload())
      appStore.showSuccess('代理节点已更新')
    } else {
      await createProxyNode(buildPayload())
      appStore.showSuccess('代理节点已添加')
    }
    resetForm()
    showNodeModal.value = false
    await Promise.all([loadNodes(), loadNodeOptions(), loadRuntime()])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存代理节点失败')
  } finally {
    saving.value = false
  }
}

async function removeNode(node: ProxyNode) {
  const confirmed = await appStore.showConfirm({
    title: '删除代理节点',
    message: `确定删除代理节点 ${node.name} 吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  try {
    await deleteProxyNode(node.id)
    appStore.showSuccess('代理节点已删除')
    if (editingID.value === node.id) {
      resetForm()
      showNodeModal.value = false
    }
    await Promise.all([loadNodes(), loadNodeOptions(), loadSettings(), loadRuntime()])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '删除代理节点失败')
  }
}

async function checkNode(node: ProxyNode) {
  testingID.value = node.id
  try {
    await testProxyNode(node.id)
    appStore.showSuccess('代理测试通过')
    await Promise.all([loadNodes(), loadNodeOptions(), loadRuntime()])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '代理测试失败')
    await Promise.all([loadNodes(), loadNodeOptions()])
  } finally {
    testingID.value = null
  }
}

function toggleAllPagedNodes() {
  if (allPagedNodesSelected.value) {
    const pagedIDs = new Set(pagedNodeIDs.value)
    selectedNodeIDs.value = selectedNodeIDs.value.filter((id) => !pagedIDs.has(id))
    return
  }
  selectedNodeIDs.value = Array.from(new Set([...selectedNodeIDs.value, ...pagedNodeIDs.value]))
}

function openImportProxyDataModal() {
  moreActionsOpen.value = false
  importFileName.value = ''
  importFile.value = null
  if (importFileInputRef.value) {
    importFileInputRef.value.value = ''
  }
  importModalOpen.value = true
}

function openExportProxyDataModal() {
  moreActionsOpen.value = false
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value))
}

function stringField(record: Record<string, unknown>, key: string, fallback = '') {
  const value = record[key]
  return typeof value === 'string' ? value : fallback
}

function numberField(record: Record<string, unknown>, key: string, fallback = 0) {
  const value = Number(record[key])
  return Number.isFinite(value) ? value : fallback
}

function booleanField(record: Record<string, unknown>, key: string, fallback = true) {
  const value = record[key]
  return typeof value === 'boolean' ? value : fallback
}

function serializeProxyNode(node: ProxyNode): ExportedProxyNode {
  return {
    name: node.name,
    protocol: node.protocol,
    address: node.address,
    port: node.port,
    username: node.username,
    password: node.password,
    uuid: node.uuid,
    alter_id: node.alter_id,
    security: node.security,
    encryption: node.encryption,
    transport: node.transport,
    tls: node.tls,
    sni: node.sni,
    path: node.path,
    host_header: node.host_header,
    flow: node.flow,
    fingerprint: node.fingerprint,
    public_key: node.public_key,
    short_id: node.short_id,
    spider_x: node.spider_x,
    enabled: node.enabled,
    remark: node.remark,
    created_at: node.created_at,
  }
}

function normalizeImportedProxyNode(raw: unknown, index: number): SaveProxyNodePayload {
  if (!isRecord(raw)) {
    throw new Error(`第 ${index + 1} 条节点格式不正确`)
  }

  const importURL = stringField(raw, 'import_url').trim()
  const protocol = stringField(raw, 'protocol', 'socks5').trim().toLowerCase() as ProxyProtocol
  if (!importURL && !allowedProxyProtocols.includes(protocol)) {
    throw new Error(`第 ${index + 1} 条节点协议不支持`)
  }

  const name = stringField(raw, 'name').trim() || `导入节点 ${index + 1}`
  if (importURL) {
    return {
      import_url: importURL,
      name,
      enabled: booleanField(raw, 'enabled', true),
      remark: stringField(raw, 'remark').trim(),
    }
  }

  const address = stringField(raw, 'address').trim()
  const port = numberField(raw, 'port')
  if (!address) {
    throw new Error(`第 ${index + 1} 条节点缺少地址`)
  }
  if (!Number.isFinite(port) || port <= 0) {
    throw new Error(`第 ${index + 1} 条节点端口无效`)
  }

  return {
    name,
    protocol,
    address,
    port,
    username: stringField(raw, 'username').trim(),
    password: stringField(raw, 'password'),
    uuid: stringField(raw, 'uuid').trim(),
    alter_id: numberField(raw, 'alter_id'),
    security: stringField(raw, 'security', 'auto').trim(),
    encryption: stringField(raw, 'encryption', 'none').trim(),
    transport: stringField(raw, 'transport', 'tcp').trim(),
    tls: stringField(raw, 'tls').trim(),
    sni: stringField(raw, 'sni').trim(),
    path: stringField(raw, 'path').trim(),
    host_header: stringField(raw, 'host_header').trim(),
    flow: stringField(raw, 'flow').trim(),
    fingerprint: stringField(raw, 'fingerprint').trim(),
    public_key: stringField(raw, 'public_key').trim(),
    short_id: stringField(raw, 'short_id').trim(),
    spider_x: stringField(raw, 'spider_x').trim(),
    enabled: booleanField(raw, 'enabled', true),
    remark: stringField(raw, 'remark').trim(),
  }
}

function parseProxyImportFile(value: unknown) {
  const rawNodes = Array.isArray(value) ? value : isRecord(value) && Array.isArray(value.nodes) ? value.nodes : []
  if (rawNodes.length === 0) {
    throw new Error('JSON 文件里没有可导入的节点')
  }
  return rawNodes.map((node, index) => normalizeImportedProxyNode(node, index))
}

function downloadProxyJSONFile(payload: unknown, selectedCount: number) {
  const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
  anchor.href = url
  anchor.download = `proxy-nodes-${selectedCount > 0 ? 'selected' : 'all'}-${stamp}.json`
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

async function loadProxyNodesForExport() {
  if (selectedNodeIDs.value.length > 0) return selectedNodes.value

  const exportPageSize = 500
  const firstPage = await listProxyNodes(currentProxyNodeListParams(1, exportPageSize))
  const result = [...firstPage.items]
  for (let page = 2; page <= firstPage.pages; page += 1) {
    const response = await listProxyNodes(currentProxyNodeListParams(page, exportPageSize))
    result.push(...response.items)
  }
  return result
}

async function exportProxyDataFile() {
  const selectedCount = selectedNodeIDs.value.length
  exportingProxyData.value = true
  try {
    const exportNodes = await loadProxyNodesForExport()
    if (exportNodes.length === 0) {
      appStore.showError('没有可导出的代理节点')
      return
    }
    downloadProxyJSONFile({
      type: 'proxy_nodes',
      version: 1,
      exported_at: new Date().toISOString(),
      nodes: exportNodes.map(serializeProxyNode),
    }, selectedCount)
    exportModalOpen.value = false
    appStore.showSuccess(selectedCount > 0 ? `已导出 ${selectedCount} 个选中节点` : `已导出 ${exportNodes.length} 个筛选节点`)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '导出代理节点失败')
  } finally {
    exportingProxyData.value = false
  }
}

async function startImportProxyData() {
  if (!importFile.value) {
    appStore.showError('请选择要导入的 JSON 文件')
    return
  }

  importingProxyData.value = true
  try {
    const parsed = JSON.parse(await importFile.value.text()) as unknown
    const importNodes = parseProxyImportFile(parsed)
    const result = await importProxyNodes(importNodes)
    importModalOpen.value = false
    selectedNodeIDs.value = []
    await Promise.all([loadNodes(), loadNodeOptions(), loadRuntime()])
    appStore.showSuccess(`导入完成：${result.count} 个节点`)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '导入代理节点失败')
  } finally {
    importingProxyData.value = false
  }
}

async function testSelectedNodes() {
  const targets = [...selectedNodes.value]
  if (targets.length === 0) return

  batchTesting.value = true
  let successCount = 0
  try {
    for (const node of targets) {
      testingID.value = node.id
      try {
        await testProxyNode(node.id)
        successCount += 1
      } catch {
        // Keep testing the remaining selected nodes.
      }
    }
    await Promise.all([loadNodes(), loadNodeOptions(), loadRuntime()])
    if (successCount === targets.length) {
      appStore.showSuccess(`批量测试完成：${successCount} 个节点正常`)
    } else {
      appStore.showError(`批量测试完成：成功 ${successCount} 个，失败 ${targets.length - successCount} 个`)
    }
  } finally {
    testingID.value = null
    batchTesting.value = false
  }
}

async function removeSelectedNodes() {
  const targets = [...selectedNodes.value]
  if (targets.length === 0) return
  const confirmed = await appStore.showConfirm({
    title: '批量删除代理节点',
    message: `确定删除选中的 ${targets.length} 个节点吗？`,
    description: '删除后无法恢复。',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!confirmed) return

  batchDeleting.value = true
  const deletedIDs = new Set<number>()
  try {
    for (const node of targets) {
      try {
        await deleteProxyNode(node.id)
        deletedIDs.add(node.id)
      } catch {
        // Continue deleting the remaining selected nodes.
      }
    }
    if (editingID.value && deletedIDs.has(editingID.value)) {
      resetForm()
      showNodeModal.value = false
    }
    selectedNodeIDs.value = selectedNodeIDs.value.filter((id) => !deletedIDs.has(id))
    await Promise.all([loadNodes(), loadNodeOptions(), loadSettings(), loadRuntime()])
    if (deletedIDs.size === targets.length) {
      appStore.showSuccess(`已删除 ${deletedIDs.size} 个节点`)
    } else {
      appStore.showError(`删除完成：成功 ${deletedIDs.size} 个，失败 ${targets.length - deletedIDs.size} 个`)
    }
  } finally {
    batchDeleting.value = false
  }
}

async function saveSettings(showToast = true, lockControls = true) {
  if (settingsSaving.value) {
    settingsSaveQueued = true
    return
  }
  settingsSaving.value = true
  settingsControlsLocked.value = lockControls
  try {
    do {
      settingsSaveQueued = false
      await persistCurrentProxySettings()
      if (showToast) appStore.showSuccess('代理使用设置已保存')
      showToast = false
      await refreshProxyStateAfterSettingsChange()
    } while (settingsSaveQueued)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存代理设置失败')
    settingsSaveQueued = false
    await Promise.all([loadSettings(true), loadNodes(), loadNodeOptions(), loadRuntime()]).catch(() => undefined)
  } finally {
    await refreshProxyStateAfterSettingsChange().catch(() => undefined)
    settingsControlsLocked.value = false
    settingsSaving.value = false
  }
}

async function handleProxyNodeChange(scope: ProxyScopeKey) {
  if (settingsSaving.value) {
    settingsSaveQueued = true
    return
  }

  const scopeForm = settingsForm[scope]
  const label = proxyScopeActionLabels[scope]
  const nodeID = Number(scopeForm.proxy_node_id) || 0
  const nodeName = proxyNodeDisplayName(selectedProxyNode(scopeForm))
  const targetEnabled = Boolean(scopeForm.enabled)

  settingsSaving.value = true
  settingsControlsLocked.value = true
  testingID.value = targetEnabled ? nodeID || null : null
  try {
    if (targetEnabled && !nodeID) {
      throw new Error(`请先选择${label}节点`)
    }

    await persistCurrentProxySettings()
    await refreshProxyStateAfterSettingsChange()

    const latestNode = nodeID ? nodeOptionList.value.find((item) => item.id === nodeID) : null
    const displayName = latestNode ? proxyNodeDisplayName(latestNode) : nodeName
    const latencyText = latestNode?.latency_ms ? `，延迟：${formatProxyTestLatency(latestNode.latency_ms)}` : ''
    if (targetEnabled) {
      appStore.showSuccess(`${label} 已切换到 ${displayName}${latencyText}`)
    } else {
      appStore.showSuccess(`${label} 已选择 ${displayName}，开启后生效`)
    }
  } catch (error) {
    appStore.showError(error instanceof Error ? `切换失败：${error.message}` : '切换代理节点失败')
    settingsSaveQueued = false
    await Promise.all([loadSettings(true), loadNodes(), loadNodeOptions(), loadRuntime()]).catch(() => undefined)
  } finally {
    testingID.value = null
    settingsControlsLocked.value = false
    settingsSaving.value = false
  }
}

async function handleProxyEnabledChange(scope: ProxyScopeKey) {
  if (settingsSaving.value) {
    window.setTimeout(() => void handleProxyEnabledChange(scope), 120)
    return
  }

  const scopeForm = settingsForm[scope]
  const targetEnabled = Boolean(scopeForm.enabled)
  const label = proxyScopeActionLabels[scope]
  let phase: 'prepare' | 'test' | 'save' = 'prepare'

  settingsSaving.value = true
  settingsControlsLocked.value = true
  try {
    if (targetEnabled) {
      const nodeID = Number(scopeForm.proxy_node_id) || 0
      if (!nodeID) {
        throw new Error(`请先选择${label}节点`)
      }
      phase = 'test'
      testingID.value = nodeID
      const testedNode = await testProxyNode(nodeID)
      appStore.showSuccess(`节点检测完成：正常，延迟：${formatProxyTestLatency(testedNode.latency_ms)}`)
    }

    phase = 'save'
    await persistCurrentProxySettings()
    appStore.showSuccess(`${label} 已${targetEnabled ? '开启' : '关闭'}`)
    await refreshProxyStateAfterSettingsChange()
  } catch (error) {
    if (phase === 'test') {
      appStore.showError('节点检测完成：错误')
    } else {
      appStore.showError(error instanceof Error ? error.message : '保存代理设置失败')
    }
    await Promise.all([loadSettings(true), loadNodes(), loadNodeOptions(), loadRuntime()]).catch(() => undefined)
  } finally {
    testingID.value = null
    settingsControlsLocked.value = false
    settingsSaving.value = false
  }
}

function statusLabel(node: ProxyNode) {
  if (!node.enabled) return '停用'
  if (node.status === 'normal') return '正常'
  if (node.status === 'error') return '错误'
  return '未检查'
}

function shouldShowProxyStatusReason(node: ProxyNode) {
  return node.status === 'error' && Boolean(node.status_reason?.trim())
}

function statusClass(node: ProxyNode) {
  if (!node.enabled) return 'proxy-status-muted'
  if (node.status === 'normal') return 'proxy-status-normal'
  if (node.status === 'error') return 'proxy-status-error'
  return 'proxy-status-muted'
}

function formatNodeEndpoint(node: ProxyNode) {
  if (node.protocol === 'vmess' || node.protocol === 'vless') {
    return `${node.address}:${node.port} -> 127.0.0.1:${node.local_port || '-'}`
  }
  return `${node.address}:${node.port}`
}

function formatLatency(node: ProxyNode) {
  return node.status === 'normal' && node.last_tested_at ? `${node.latency_ms} ms` : '-'
}

function formatProxyDateTime(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}
</script>

<template>
  <div class="proxy-page">
    <div class="proxy-stats-grid">
      <section class="proxy-stat">
        <Globe2 class="h-5 w-5 text-sky-500" />
        <div>
          <p>节点总数</p>
          <strong>{{ nodeStatsTotal }}</strong>
        </div>
      </section>
      <section class="proxy-stat">
        <CheckCircle2 class="h-5 w-5 text-emerald-500" />
        <div>
          <p>节点状态</p>
          <div class="proxy-stat-lines proxy-node-state-lines">
            <span class="proxy-stat-line proxy-stat-success">
              <span class="proxy-stat-number">{{ normalCount }}</span>
              <span>正常</span>
            </span>
            <span class="proxy-stat-line proxy-stat-danger">
              <span class="proxy-stat-number">{{ errorCount }}</span>
              <span>错误</span>
            </span>
          </div>
        </div>
      </section>
      <section class="proxy-stat">
        <RefreshCw class="h-5 w-5 text-indigo-500" />
        <div>
          <p>xray 核心</p>
          <div class="proxy-stat-lines">
            <span class="proxy-stat-line" :class="xrayLoadClass">{{ xrayLoadText }}</span>
            <span class="proxy-stat-line" :class="xrayRunClass">{{ xrayRunText }}</span>
          </div>
        </div>
      </section>
      <section class="proxy-stat">
        <CheckCircle2 class="h-5 w-5 text-cyan-500" />
        <div>
          <p>运行状态</p>
          <div class="proxy-stat-lines proxy-stat-service-lines">
            <span class="proxy-stat-line proxy-stat-service-line" :class="imapProxyRunning ? 'proxy-stat-success' : 'proxy-stat-danger'">
              <span class="proxy-stat-service-name">IMAP邮箱管理</span>
              <span>{{ imapProxyRunning ? '已运行' : '未运行' }}</span>
            </span>
            <span class="proxy-stat-line proxy-stat-service-line" :class="outlookProxyRunning ? 'proxy-stat-success' : 'proxy-stat-danger'">
              <span class="proxy-stat-service-name">微软邮箱管理</span>
              <span>{{ outlookProxyRunning ? '已运行' : '未运行' }}</span>
            </span>
          </div>
        </div>
      </section>
    </div>

    <section class="proxy-panel proxy-table-panel">
      <div class="proxy-settings-grid">
        <div class="proxy-setting-item">
          <div>
            <strong>IMAP邮箱管理</strong>
            <span>收信、发信、检测账号使用的代理</span>
          </div>
          <label class="toggle">
            <input v-model="settingsForm.imap.enabled" type="checkbox" :disabled="settingsControlsLocked" @change="handleProxyEnabledChange('imap')" />
            <span class="toggle-slider"></span>
          </label>
          <select v-model.number="settingsForm.imap.proxy_node_id" class="input" :disabled="settingsControlsLocked" @change="handleProxyNodeChange('imap')">
            <option :value="0">选择代理节点</option>
            <option v-for="node in nodeOptionList" :key="node.id" :value="node.id">{{ node.name }} · {{ node.protocol.toUpperCase() }}</option>
          </select>
        </div>

        <div class="proxy-setting-item">
          <div>
            <strong>微软邮箱管理</strong>
            <span>OAuth、Graph API、读取邮件使用的代理</span>
          </div>
          <label class="toggle">
            <input v-model="settingsForm.outlook.enabled" type="checkbox" :disabled="settingsControlsLocked" @change="handleProxyEnabledChange('outlook')" />
            <span class="toggle-slider"></span>
          </label>
          <select v-model.number="settingsForm.outlook.proxy_node_id" class="input" :disabled="settingsControlsLocked" @change="handleProxyNodeChange('outlook')">
            <option :value="0">选择代理节点</option>
            <option v-for="node in nodeOptionList" :key="node.id" :value="node.id">{{ node.name }} · {{ node.protocol.toUpperCase() }}</option>
          </select>
        </div>
      </div>
      <div v-if="runtime?.xray_error" class="proxy-runtime-warning">{{ runtime.xray_error }}</div>

      <div class="proxy-table-toolbar">
        <div class="proxy-table-actions">
          <button class="proxy-action-primary" type="button" @click="openCreateNodeModal">
            <Plus class="h-4 w-4" />
            新增节点
          </button>
          <div class="relative" data-proxy-more-actions>
            <button class="proxy-action-more" type="button" @click.stop="moreActionsOpen = !moreActionsOpen">
              <MoreHorizontal class="h-4 w-4" />
              <span>更多操作</span>
              <ChevronDown class="h-4 w-4 transition-transform" :class="{ 'rotate-180': moreActionsOpen }" />
            </button>
            <div v-if="moreActionsOpen" class="proxy-more-menu" @click.stop>
              <div class="proxy-more-menu-label">数据操作</div>
              <button class="proxy-more-menu-item" type="button" @click="openImportProxyDataModal">
                <span class="proxy-more-menu-icon import"><Upload class="h-4 w-4" /></span>
                <span>导入</span>
              </button>
              <button class="proxy-more-menu-item" type="button" @click="openExportProxyDataModal">
                <span class="proxy-more-menu-icon export"><Download class="h-4 w-4" /></span>
                <span>{{ selectedNodeIDs.length > 0 ? '导出选中' : '导出' }}</span>
                <span v-if="selectedNodeIDs.length > 0" class="proxy-more-selected-badge">已选 {{ selectedNodeIDs.length }}</span>
              </button>
            </div>
          </div>
          <button class="proxy-action-refresh" type="button" title="刷新" :disabled="loading" @click="refreshAll">
            <RefreshCw class="h-4 w-4" :class="{ 'proxy-refresh-icon-spinning': loading }" />
            刷新
          </button>
          <button v-if="selectedNodeIDs.length > 0" class="proxy-toolbar-batch-button" type="button" :disabled="batchTesting || batchDeleting" @click="testSelectedNodes">
            <Play class="h-4 w-4" />
            批量测试（{{ selectedNodeIDs.length }}）
          </button>
          <button v-if="selectedNodeIDs.length > 0" class="proxy-toolbar-batch-danger" type="button" :disabled="batchTesting || batchDeleting" @click="removeSelectedNodes">
            <Trash2 class="h-4 w-4" />
            批量删除（{{ selectedNodeIDs.length }}）
          </button>
        </div>
        <div class="proxy-search search-clear-field">
          <Search class="h-4 w-4" />
          <input v-model="searchQuery" class="search-clear-input" type="text" placeholder="搜索节点" />
          <button v-if="searchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="searchQuery = ''">
            <X class="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <div ref="proxyTableWrapRef" class="proxy-table-wrap">
        <table class="proxy-table">
          <colgroup>
            <col class="proxy-col-select" />
            <col class="proxy-col-name" />
            <col class="proxy-col-endpoint" />
            <col class="proxy-col-status" />
            <col class="proxy-col-latency" />
            <col class="proxy-col-created" />
            <col class="proxy-col-actions" />
          </colgroup>
          <thead>
            <tr>
              <th class="proxy-col-select">
                <input :checked="allPagedNodesSelected" type="checkbox" @change="toggleAllPagedNodes" />
              </th>
              <th class="proxy-col-name">
                <button class="proxy-sort-button" type="button" @click="toggleProxySort('name')">
                  <span>节点</span>
                  <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': proxySortKey === 'name' && proxySortOrder === 'asc', 'proxy-sort-inactive': proxySortKey !== 'name' }" />
                </button>
              </th>
              <th class="proxy-col-endpoint">
                <button class="proxy-sort-button" type="button" @click="toggleProxySort('address')">
                  <span>地址</span>
                  <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': proxySortKey === 'address' && proxySortOrder === 'asc', 'proxy-sort-inactive': proxySortKey !== 'address' }" />
                </button>
              </th>
              <th class="proxy-col-status">
                <button class="proxy-sort-button" type="button" @click="toggleProxySort('status')">
                  <span>状态</span>
                  <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': proxySortKey === 'status' && proxySortOrder === 'asc', 'proxy-sort-inactive': proxySortKey !== 'status' }" />
                </button>
              </th>
              <th class="proxy-col-latency">
                <button class="proxy-sort-button" type="button" @click="toggleProxySort('latency')">
                  <span>延迟</span>
                  <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': proxySortKey === 'latency' && proxySortOrder === 'asc', 'proxy-sort-inactive': proxySortKey !== 'latency' }" />
                </button>
              </th>
              <th class="proxy-col-created">
                <button class="proxy-sort-button" type="button" @click="toggleProxySort('created_at')">
                  <span>添加时间</span>
                  <ChevronDown class="h-3.5 w-3.5" :class="{ 'rotate-180': proxySortKey === 'created_at' && proxySortOrder === 'asc', 'proxy-sort-inactive': proxySortKey !== 'created_at' }" />
                </button>
              </th>
              <th class="proxy-col-actions sticky-col-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="node in pagedNodes" :key="node.id">
              <td class="proxy-select-col">
                <input v-model="selectedNodeIDs" :value="node.id" type="checkbox" />
              </td>
              <td>
                <div class="proxy-node-name">
                  <strong>{{ node.name }}</strong>
                  <span>{{ node.protocol.toUpperCase() }}</span>
                </div>
              </td>
              <td>
                <span class="proxy-endpoint">{{ formatNodeEndpoint(node) }}</span>
              </td>
              <td>
                <div class="proxy-status-cell">
                  <span class="proxy-status" :class="statusClass(node)">
                    <CheckCircle2 v-if="node.enabled && node.status === 'normal'" class="h-3.5 w-3.5" />
                    <XCircle v-else-if="node.enabled && node.status === 'error'" class="h-3.5 w-3.5" />
                    <CircleOff v-else class="h-3.5 w-3.5" />
                    {{ statusLabel(node) }}
                  </span>
                  <span v-if="shouldShowProxyStatusReason(node)" class="proxy-status-reason" tabindex="0" :aria-label="node.status_reason">
                    <CircleHelp class="h-3.5 w-3.5" />
                    <span class="proxy-status-tooltip">{{ node.status_reason }}</span>
                  </span>
                </div>
              </td>
              <td>
                <span class="proxy-latency-cell" :class="{ 'proxy-latency-empty': !node.latency_ms }">{{ formatLatency(node) }}</span>
              </td>
              <td>
                <span class="proxy-created-time" :title="formatProxyDateTime(node.created_at)">{{ formatProxyDateTime(node.created_at) }}</span>
              </td>
              <td class="sticky-col-right">
                <div class="proxy-actions">
                  <button class="proxy-row-action-button hover:text-primary-600 dark:hover:text-primary-300" type="button" @click="editNode(node)">
                    <Pencil class="h-4 w-4" />
                    <span>编辑</span>
                  </button>
                  <button class="proxy-row-action-button hover:text-emerald-600 dark:hover:text-emerald-300" type="button" :disabled="testingID === node.id || batchTesting" @click="checkNode(node)">
                    <RefreshCw v-if="testingID === node.id" class="h-4 w-4 proxy-refresh-icon-spinning" />
                    <Play v-else class="h-4 w-4" />
                    <span>测试</span>
                  </button>
                  <button class="proxy-row-action-button hover:text-red-600 dark:hover:text-red-400" type="button" :disabled="batchDeleting" @click="removeNode(node)">
                    <Trash2 class="h-4 w-4" />
                    <span>删除</span>
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="nodeTotal === 0" class="proxy-empty-state p-8 text-center text-sm font-semibold text-gray-500 dark:text-dark-400">暂无代理节点</div>
      </div>

      <div class="proxy-pagination-footer flex items-center justify-between border-t border-gray-200 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800 sm:px-6">
        <PaginationBar
          :page="currentPage"
          :pages="totalPages"
          :page-size="pageSize"
          :page-size-options="normalizedPageSizeOptions"
          :total="nodeTotal"
          @page-change="changePage"
          @page-size-change="selectPageSize"
        />
        <div v-if="false" class="flex flex-1 flex-wrap items-center justify-between gap-3">
          <div class="flex items-center space-x-4">
            <p class="text-sm text-gray-700 dark:text-gray-300">
              显示 <span class="font-medium">{{ pageStart }}</span> 至 <span class="font-medium">{{ pageEnd }}</span> 共 <span class="font-medium">{{ nodeTotal }}</span> 条结果
            </p>
            <div class="flex items-center space-x-2">
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
                    v-for="size in normalizedPageSizeOptions"
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

          <nav class="compact-pagination" aria-label="Pagination">
            <button
              class="pagination-arrow-button relative inline-flex items-center rounded-l-md border border-gray-300 bg-white px-2 py-2 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600"
              type="button"
              :disabled="currentPage <= 1"
              @click="changePage(currentPage - 1)"
            >
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
            <button
              class="pagination-arrow-button relative inline-flex items-center rounded-r-md border border-gray-300 bg-white px-2 py-2 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600"
              type="button"
              :disabled="currentPage >= totalPages"
              @click="changePage(currentPage + 1)"
            >
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
          </nav>
        </div>
      </div>
    </section>

    <Teleport to="body">
      <div v-if="showNodeModal" class="proxy-modal-backdrop">
        <section class="proxy-modal proxy-panel" role="dialog" aria-modal="true" :aria-label="editingID ? '编辑节点' : '新增节点'">
          <div class="proxy-node-modal-header">
            <h3>{{ editingID ? '编辑节点' : '新增节点' }}</h3>
            <button class="proxy-modal-close-button" type="button" title="关闭" :disabled="saving" @click="closeNodeModal">
              <X class="h-5 w-5" />
            </button>
          </div>

          <form class="proxy-node-form-shell" @submit.prevent="submitNode">
            <div class="proxy-form proxy-node-modal-body">
          <label>
            <span class="input-label">导入链接</span>
            <textarea
              v-model="form.import_url"
              class="input proxy-import"
              placeholder="vmess://、vless://、socks5://、http://"
              @blur="parseImportURLFromField(true)"
              @input="handleImportURLInput"
              @paste="handleImportURLPaste"
            ></textarea>
          </label>

          <div class="proxy-form-grid">
            <label>
              <span class="input-label">节点名称</span>
              <input v-model="form.name" class="input" type="text" />
            </label>
            <label>
              <span class="input-label">协议</span>
              <select v-model="form.protocol" class="input">
                <option v-for="item in protocols" :key="item.value" :value="item.value">{{ item.label }}</option>
              </select>
            </label>
          </div>

          <div class="proxy-form-grid">
            <label>
              <span class="input-label">服务器地址</span>
              <input v-model="form.address" class="input" type="text" />
            </label>
            <label>
              <span class="input-label">端口</span>
              <input v-model.number="form.port" class="input" type="number" min="1" max="65535" />
            </label>
          </div>

          <div v-if="form.protocol === 'http' || form.protocol === 'socks5'" class="proxy-form-grid">
            <label>
              <span class="input-label">用户名</span>
              <input v-model="form.username" class="input" type="text" />
            </label>
            <label>
              <span class="input-label">密码</span>
              <input v-model="form.password" class="input" type="password" />
            </label>
          </div>

          <template v-if="isXrayProtocol">
            <label>
              <span class="input-label">UUID</span>
              <input v-model="form.uuid" class="input" type="text" />
            </label>
            <div class="proxy-form-grid">
              <label>
                <span class="input-label">传输</span>
                <select v-model="form.transport" class="input">
                  <option value="tcp">TCP</option>
                  <option value="ws">WebSocket</option>
                  <option value="grpc">gRPC</option>
                </select>
              </label>
              <label>
                <span class="input-label">TLS</span>
                <select v-model="form.tls" class="input">
                  <option value="">无</option>
                  <option value="tls">TLS</option>
                  <option value="reality">Reality</option>
                </select>
              </label>
            </div>
            <div class="proxy-form-grid">
              <label>
                <span class="input-label">{{ form.protocol === 'vmess' ? 'Security' : 'Encryption' }}</span>
                <input v-if="form.protocol === 'vmess'" v-model="form.security" class="input" type="text" />
                <input v-else v-model="form.encryption" class="input" type="text" />
              </label>
              <label v-if="form.protocol === 'vmess'">
                <span class="input-label">Alter ID</span>
                <input v-model.number="form.alter_id" class="input" type="number" min="0" />
              </label>
              <label v-else>
                <span class="input-label">Flow</span>
                <input v-model="form.flow" class="input" type="text" />
              </label>
            </div>
            <div class="proxy-form-grid">
              <label>
                <span class="input-label">SNI</span>
                <input v-model="form.sni" class="input" type="text" />
              </label>
              <label>
                <span class="input-label">Path / Service</span>
                <input v-model="form.path" class="input" type="text" />
              </label>
            </div>
            <label>
              <span class="input-label">Host Header</span>
              <input v-model="form.host_header" class="input" type="text" />
            </label>
          </template>

            </div>

            <div class="proxy-node-modal-footer">
              <button class="btn btn-secondary" type="button" :disabled="saving" @click="closeNodeModal">取消</button>
              <button class="btn btn-primary" type="submit" :disabled="saving">{{ saving ? '保存中...' : (editingID ? '保存' : '添加') }}</button>
            </div>
          </form>
        </section>
      </div>
    </Teleport>

    <div v-if="exportModalOpen" class="proxy-data-modal-backdrop" @click.self="exportModalOpen = false">
      <section class="proxy-data-modal proxy-panel" role="dialog" aria-modal="true" aria-label="导出节点">
        <div class="proxy-panel-header">
          <h3>导出节点</h3>
          <button class="proxy-icon-button" type="button" title="关闭" :disabled="exportingProxyData" @click="exportModalOpen = false">
            <X class="h-4 w-4" />
          </button>
        </div>
        <div class="proxy-data-modal-body">
          <p>导出 JSON 文件，里面包含节点连接信息、认证信息和备注。</p>
          <div class="proxy-import-warning">
            当前将导出 {{ selectedNodeIDs.length > 0 ? `选中的 ${exportTargetCount} 个节点` : `当前筛选的 ${exportTargetCount} 个节点` }}。请妥善保存文件。
          </div>
        </div>
        <div class="proxy-data-modal-footer">
          <button class="btn btn-secondary" type="button" @click="exportModalOpen = false">取消</button>
          <button class="btn btn-primary" type="button" :disabled="exportingProxyData || exportTargetCount === 0" @click="exportProxyDataFile">{{ exportingProxyData ? '导出中...' : '开始导出' }}</button>
        </div>
      </section>
    </div>

    <div v-if="importModalOpen" class="proxy-data-modal-backdrop" @click.self="importModalOpen = false">
      <section class="proxy-data-modal proxy-panel" role="dialog" aria-modal="true" aria-label="导入节点">
        <div class="proxy-panel-header">
          <h3>导入节点</h3>
          <button class="proxy-icon-button" type="button" title="关闭" :disabled="importingProxyData" @click="importModalOpen = false">
            <X class="h-4 w-4" />
          </button>
        </div>
        <div class="proxy-data-modal-body">
          <p>上传导出的 JSON 文件以批量导入代理节点。</p>
          <div class="proxy-import-warning">导入文件包含代理账号、密码、UUID 等连接信息；重复节点不会自动覆盖，会作为新节点添加。</div>
          <label class="proxy-import-file-label">
            <span class="input-label">数据文件</span>
            <div class="proxy-import-file-box">
              <div class="min-w-0">
                <div class="truncate text-sm font-bold text-gray-800 dark:text-dark-100">{{ importFileName || '请选择数据文件' }}</div>
                <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">JSON (.json)</div>
              </div>
              <button class="proxy-import-file-button" type="button" @click="chooseImportFile">选择文件</button>
              <input ref="importFileInputRef" class="hidden" type="file" accept=".json,application/json" @change="handleImportFileChange" />
            </div>
          </label>
        </div>
        <div class="proxy-data-modal-footer">
          <button class="btn btn-secondary" type="button" @click="importModalOpen = false">取消</button>
          <button class="btn btn-primary" type="button" :disabled="importingProxyData || !importFile" @click="startImportProxyData">{{ importingProxyData ? '导入中...' : '开始导入' }}</button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.proxy-page {
  display: grid;
  width: 100%;
  max-width: 100%;
  height: calc(100vh - 8rem);
  min-height: 0;
  min-width: 0;
  grid-template-rows: auto minmax(0, 1fr);
  gap: 1rem;
  margin-top: -0.35rem;
  overflow: hidden;
}

.proxy-stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0.85rem;
}

.proxy-stat,
.proxy-panel {
  border: 1px solid rgb(203 213 225 / 0.82);
  border-radius: 1rem;
  background: rgb(255 255 255 / 0.94);
  box-shadow: 0 14px 30px rgb(15 23 42 / 0.06);
}

.dark .proxy-stat,
.dark .proxy-panel {
  border-color: rgb(51 65 85 / 0.86);
  background: rgb(15 23 42 / 0.78);
  box-shadow: none;
}

.proxy-stat {
  display: grid;
  min-height: 4.6rem;
  grid-template-columns: 2.25rem minmax(0, 1fr);
  align-items: center;
  gap: 0.85rem;
  padding: 1rem;
}

.proxy-stat p {
  font-size: 0.8rem;
  font-weight: 700;
  color: rgb(100 116 139);
}

.proxy-stat strong {
  display: block;
  margin-top: 0.15rem;
  font-size: 1.25rem;
  line-height: 1.1;
  color: rgb(15 23 42);
}

.dark .proxy-stat p {
  color: rgb(148 163 184);
}

.dark .proxy-stat strong {
  color: white;
}

.proxy-stat-lines {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem 0.65rem;
  margin-top: 0.35rem;
}

.proxy-stat-line {
  display: inline-flex;
  min-width: 0;
  align-items: baseline;
  gap: 0.28rem;
  white-space: nowrap;
  font-size: 0.8125rem;
  font-weight: 800;
  line-height: 1.2;
}

.proxy-stat-number {
  font-size: 1.15rem;
  line-height: 1;
}

.proxy-node-state-lines {
  column-gap: 1.15rem;
}

.proxy-stat-success {
  color: rgb(5 150 105);
}

.proxy-stat-danger {
  color: rgb(225 29 72);
}

.proxy-stat-muted {
  color: rgb(100 116 139);
}

.dark .proxy-stat-success {
  color: rgb(52 211 153);
}

.dark .proxy-stat-danger {
  color: rgb(251 113 133);
}

.dark .proxy-stat-muted {
  color: rgb(148 163 184);
}

.proxy-stat-service-lines {
  display: grid;
  gap: 0.28rem;
}

.proxy-stat-service-line {
  display: grid;
  grid-template-columns: 5.9rem max-content;
  column-gap: 0.45rem;
  justify-content: flex-start;
}

.proxy-stat-service-name {
  overflow: hidden;
  text-overflow: ellipsis;
}

.proxy-panel-header,
.proxy-table-toolbar {
  display: flex;
  min-height: 4rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0 1rem;
}

.dark .proxy-panel-header,
.dark .proxy-table-toolbar {
  border-bottom-color: rgb(51 65 85);
}

.proxy-panel-header h3 {
  font-size: 1rem;
  font-weight: 800;
  color: rgb(15 23 42);
}

.dark .proxy-panel-header h3 {
  color: white;
}

.proxy-settings-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1rem;
  padding: 1rem;
}

.proxy-setting-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 0.85rem;
  border-radius: 0.75rem;
  background: rgb(248 250 252);
  padding: 1rem;
}

.dark .proxy-setting-item {
  background: rgb(30 41 59 / 0.72);
}

.proxy-setting-item strong {
  display: block;
  font-size: 0.95rem;
  color: rgb(15 23 42);
}

.dark .proxy-setting-item strong {
  color: white;
}

.proxy-setting-item > div > span {
  margin-top: 0.25rem;
  display: block;
  font-size: 0.8rem;
  color: rgb(100 116 139);
}

.proxy-setting-item > .toggle {
  align-self: start;
  margin-top: 0.05rem;
}

.proxy-setting-item select {
  grid-column: 1 / -1;
}

.proxy-runtime-warning {
  margin: 0 1rem 1rem;
  border-radius: 0.7rem;
  border: 1px solid rgb(245 158 11 / 0.35);
  background: rgb(245 158 11 / 0.08);
  padding: 0.75rem 0.85rem;
  font-size: 0.8125rem;
  color: rgb(146 64 14);
}

.dark .proxy-runtime-warning {
  color: rgb(252 211 77);
}

.proxy-form {
  display: grid;
  gap: 1rem;
  padding: 1rem;
}

.proxy-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.85rem;
}

.proxy-import {
  min-height: 4.4rem;
  resize: vertical;
}

.proxy-icon-button {
  display: inline-flex;
  height: 2rem;
  width: 2rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
  color: rgb(100 116 139);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.proxy-icon-button:hover {
  background: rgb(14 165 233 / 0.1);
  color: rgb(2 132 199);
}

.proxy-table-panel {
  display: flex;
  min-height: 0;
  min-width: 0;
  flex-direction: column;
  overflow: hidden;
}

.proxy-table-toolbar {
  border-top: 1px solid rgb(226 232 240);
}

.dark .proxy-table-toolbar {
  border-top-color: rgb(51 65 85);
}

.proxy-table-actions {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-start;
  gap: 0.6rem;
}

.proxy-action-primary,
.proxy-action-more,
.proxy-action-refresh,
.proxy-toolbar-batch-button,
.proxy-toolbar-batch-danger {
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

.proxy-action-primary {
  background: linear-gradient(135deg, rgb(20 184 166), rgb(13 148 136));
  color: white;
  box-shadow: 0 12px 22px rgb(20 184 166 / 0.22);
}

.proxy-action-more {
  min-width: 8.5rem;
  border: 1px solid rgb(148 163 184 / 0.55);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
}

.proxy-action-refresh {
  border: 1px solid rgb(148 163 184 / 0.45);
  background: rgb(248 250 252);
  color: rgb(51 65 85);
}

.proxy-toolbar-batch-button,
.proxy-toolbar-batch-danger {
  color: white;
  font-weight: 700;
  box-shadow: 0 8px 18px rgb(15 23 42 / 0.12);
}

.proxy-toolbar-batch-button {
  background: rgb(37 99 235);
}

.proxy-toolbar-batch-danger {
  background: rgb(239 68 68);
}

.proxy-action-primary:hover,
.proxy-action-more:hover,
.proxy-action-refresh:hover,
.proxy-toolbar-batch-button:hover,
.proxy-toolbar-batch-danger:hover {
  transform: translateY(-1px);
}

.proxy-action-refresh:disabled,
.proxy-toolbar-batch-button:disabled,
.proxy-toolbar-batch-danger:disabled {
  cursor: wait;
  opacity: 0.72;
}

.proxy-action-refresh:disabled:hover,
.proxy-toolbar-batch-button:disabled:hover,
.proxy-toolbar-batch-danger:disabled:hover {
  transform: none;
}

.proxy-refresh-icon-spinning {
  animation: proxy-refresh-spin 0.8s linear infinite;
}

@keyframes proxy-refresh-spin {
  to {
    transform: rotate(360deg);
  }
}

html.dark .proxy-action-more,
html.dark .proxy-action-refresh {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
}

.proxy-more-menu {
  position: absolute;
  right: 0;
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

.proxy-more-menu-label {
  margin-bottom: 0.35rem;
  padding: 0 0.25rem;
  font-size: 0.75rem;
  color: rgb(100 116 139);
}

.proxy-more-menu-item {
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

.proxy-more-menu-item:hover {
  background: rgb(241 245 249);
  color: rgb(15 23 42);
}

.proxy-more-menu-icon {
  display: inline-flex;
  height: 2rem;
  width: 2rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.45rem;
}

.proxy-more-menu-icon.import {
  background: rgb(204 251 241);
  color: rgb(15 118 110);
}

.proxy-more-menu-icon.export {
  background: rgb(237 233 254);
  color: rgb(109 40 217);
}

.proxy-more-selected-badge {
  margin-left: auto;
  border-radius: 999px;
  background: rgb(204 251 241);
  padding: 0.15rem 0.45rem;
  font-size: 0.7rem;
  font-weight: 800;
  color: rgb(15 118 110);
}

html.dark .proxy-more-menu {
  border-color: rgb(71 85 105 / 0.55);
  background: rgb(30 41 59);
  color: rgb(226 232 240);
  box-shadow: 0 18px 38px rgb(2 6 23 / 0.3);
}

html.dark .proxy-more-menu-label {
  color: rgb(148 163 184);
}

html.dark .proxy-more-menu-item {
  color: rgb(226 232 240);
}

html.dark .proxy-more-menu-item:hover {
  background: rgb(51 65 85 / 0.72);
  color: white;
}

html.dark .proxy-more-menu-icon.import {
  background: rgb(20 184 166 / 0.18);
  color: rgb(94 234 212);
}

html.dark .proxy-more-menu-icon.export {
  background: rgb(124 58 237 / 0.2);
  color: rgb(167 139 250);
}

html.dark .proxy-more-selected-badge {
  background: rgb(20 184 166 / 0.18);
  color: rgb(94 234 212);
}

.proxy-search {
  display: inline-flex;
  height: 2.4rem;
  width: min(22rem, 52vw);
  align-items: center;
  gap: 0.55rem;
  border: 1px solid rgb(203 213 225);
  border-radius: 0.75rem;
  background: white;
  padding: 0 0.8rem;
  color: rgb(100 116 139);
}

.dark .proxy-search {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42);
}

.proxy-search input {
  min-width: 0;
  flex: 1;
  border: 0;
  background: transparent;
  color: rgb(15 23 42);
  font-size: 0.875rem;
  outline: none;
}

.dark .proxy-search input {
  color: white;
}

.proxy-table-wrap {
  --proxy-col-select: 4rem;
  --proxy-col-name: 13rem;
  --proxy-col-endpoint: 24rem;
  --proxy-col-status: 9rem;
  --proxy-col-latency: 8rem;
  --proxy-col-created: 12rem;
  --proxy-col-actions: 170px;
  --proxy-table-min-width: calc(
    var(--proxy-col-select) +
    var(--proxy-col-name) +
    var(--proxy-col-endpoint) +
    var(--proxy-col-status) +
    var(--proxy-col-latency) +
    var(--proxy-col-created) +
    var(--proxy-col-actions)
  );
  --proxy-table-divider: rgb(148 163 184 / 0.08);
  width: 100%;
  max-width: 100%;
  min-height: 0;
  flex: 1;
  overflow-x: auto;
  overflow-y: auto;
}

.dark .proxy-table-wrap {
  --proxy-table-divider: rgb(148 163 184 / 0.12);
}

.proxy-table {
  width: max(100%, var(--proxy-table-min-width));
  min-width: var(--proxy-table-min-width);
  table-layout: fixed;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 0.8125rem;
}

.proxy-table th,
.proxy-table td {
  border-bottom: 1px solid rgb(226 232 240);
  padding: 0.78rem 0.85rem;
  text-align: left;
  vertical-align: middle;
}

.proxy-table th {
  border-right: 1px solid var(--proxy-table-divider);
}

.proxy-table th:last-child {
  border-right: 0;
}

.dark .proxy-table th,
.dark .proxy-table td {
  border-bottom-color: rgb(51 65 85);
}

.proxy-table th {
  position: sticky;
  top: 0;
  z-index: 5;
  background: rgb(248 250 252);
  color: rgb(100 116 139);
  font-weight: 800;
  text-align: center;
}

.proxy-sort-button {
  display: inline-flex;
  width: 100%;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  color: inherit;
  font: inherit;
}

.proxy-sort-button svg {
  transition: transform 0.18s ease, opacity 0.18s ease;
}

.proxy-sort-inactive {
  opacity: 0.32;
}

.dark .proxy-table th {
  background: rgb(30 41 59);
  color: rgb(203 213 225);
}

.proxy-col-select { width: var(--proxy-col-select); }
.proxy-col-name { width: var(--proxy-col-name); }
.proxy-col-endpoint { width: var(--proxy-col-endpoint); }
.proxy-col-status { width: var(--proxy-col-status); }
.proxy-col-latency { width: var(--proxy-col-latency); }
.proxy-col-created { width: var(--proxy-col-created); }
.proxy-col-actions { width: var(--proxy-col-actions); }

.sticky-col-right {
  position: sticky;
  right: 0;
  z-index: 10;
  width: var(--proxy-col-actions);
  min-width: var(--proxy-col-actions);
  overflow: visible;
  border-left: 0;
  background: rgb(255 255 255);
  background-clip: padding-box;
  box-shadow: -18px 0 28px rgb(15 23 42 / 0.1);
  text-align: center;
}

.sticky-col-right::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 1px;
  background: var(--proxy-table-divider);
  pointer-events: none;
}

.proxy-table thead .sticky-col-right {
  z-index: 20;
  background: rgb(248 250 252);
}

.dark .sticky-col-right {
  background: rgb(15 23 42);
  box-shadow: -18px 0 30px rgb(0 0 0 / 0.3);
}

.dark .proxy-table thead .sticky-col-right {
  background: rgb(30 41 59);
}

.proxy-col-select,
.proxy-select-col {
  text-align: center !important;
}

.proxy-status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  min-width: 5.25rem;
  border-radius: 0.45rem;
  padding: 0.35rem 0.5rem;
  font-size: 0.75rem;
  font-weight: 800;
}

.proxy-status-normal {
  background: rgb(16 185 129 / 0.12);
  color: rgb(5 150 105);
}

.proxy-status-error {
  background: rgb(244 63 94 / 0.12);
  color: rgb(225 29 72);
}

.proxy-status-muted {
  background: rgb(148 163 184 / 0.14);
  color: rgb(100 116 139);
}

.proxy-status-cell {
  position: relative;
  display: inline-flex;
  width: 100%;
  align-items: center;
  gap: 0.35rem;
}

.proxy-status-reason {
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

.proxy-status-reason:hover,
.proxy-status-reason:focus-visible {
  color: rgb(239 68 68);
}

.proxy-status-tooltip {
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

.proxy-status-tooltip::before {
  content: '';
  position: absolute;
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 0.35rem solid transparent;
  border-bottom-color: rgb(15 23 42);
}

.proxy-status-reason:hover .proxy-status-tooltip,
.proxy-status-reason:focus-visible .proxy-status-tooltip {
  opacity: 1;
  transform: translateX(-50%) translateY(0.1rem);
}

.proxy-node-name {
  display: grid;
  gap: 0.15rem;
}

.proxy-node-name strong,
.proxy-endpoint {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.proxy-node-name strong {
  color: rgb(15 23 42);
}

.dark .proxy-node-name strong {
  color: white;
}

.proxy-node-name span {
  font-size: 0.75rem;
  font-weight: 800;
  color: rgb(14 165 233);
}

.proxy-endpoint {
  display: block;
  max-width: 100%;
  font-weight: 700;
}

.proxy-latency-cell {
  display: inline-flex;
  width: fit-content;
  border-radius: 0.35rem;
  background: rgb(241 245 249);
  padding: 0.15rem 0.35rem;
  font-size: 0.7rem;
  font-weight: 800;
  color: rgb(100 116 139);
}

.dark .proxy-latency-cell {
  background: rgb(51 65 85);
  color: rgb(203 213 225);
}

.proxy-latency-empty {
  color: rgb(148 163 184);
}

.proxy-created-time {
  display: block;
  max-width: 100%;
  overflow: hidden;
  color: rgb(100 116 139);
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .proxy-created-time {
  color: rgb(203 213 225);
}

.proxy-actions {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
}

.proxy-row-action-button {
  display: inline-flex;
  width: 1.95rem;
  flex-shrink: 0;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.15rem;
  line-height: 1;
  color: rgb(100 116 139);
  transition: color 0.15s ease, opacity 0.15s ease;
}

.proxy-row-action-button span {
  display: block;
  white-space: nowrap;
  word-break: keep-all;
  writing-mode: horizontal-tb;
  font-size: 0.6875rem;
  line-height: 0.9rem;
}

.proxy-row-action-button:disabled {
  cursor: wait;
  opacity: 0.62;
}

.proxy-empty-state {
  pointer-events: none;
}

.proxy-pagination-footer {
  border-radius: 0 0 1rem 1rem;
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

.proxy-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: auto;
  background: rgb(0 0 0 / 0.45);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
  padding: 1rem;
}

.proxy-modal {
  display: flex;
  width: min(42rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
  flex-direction: column;
  overflow: hidden;
  border-radius: 1rem;
  border: 1px solid rgb(229 231 235);
  background: white;
  box-shadow: 0 20px 45px rgb(15 23 42 / 0.22);
}

.dark .proxy-modal {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42);
}

.proxy-node-modal-header {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid rgb(229 231 235);
  padding: 0.75rem 1.25rem;
}

.dark .proxy-node-modal-header {
  border-bottom-color: rgb(51 65 85);
}

.proxy-node-modal-header h3 {
  font-size: 1rem;
  font-weight: 800;
  color: rgb(17 24 39);
}

.dark .proxy-node-modal-header h3 {
  color: white;
}

.proxy-modal-close-button {
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

.proxy-modal-close-button:hover {
  background: rgb(226 232 240 / 0.9);
  color: rgb(71 85 105);
}

.proxy-modal-close-button:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.dark .proxy-modal-close-button {
  color: rgb(203 213 225);
}

.dark .proxy-modal-close-button:hover {
  background: rgb(51 65 85 / 0.9);
  color: white;
}

.proxy-node-form-shell {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
}

.proxy-node-modal-body {
  min-height: 0;
  overflow-y: auto;
  padding: 1.25rem;
  font-size: 0.8125rem;
}

.proxy-node-modal-body .input-label {
  margin-bottom: 0.35rem;
  font-size: 0.8125rem;
}

.proxy-node-modal-body .input {
  min-height: 2.25rem;
  border-radius: 0.625rem;
  padding: 0.5rem 0.75rem;
  font-size: 0.8125rem;
}

.proxy-node-modal-body textarea.input {
  line-height: 1.45;
}

.proxy-node-modal-body::-webkit-scrollbar {
  width: 0.55rem;
}

.proxy-node-modal-body::-webkit-scrollbar-track {
  border-radius: 999px;
  background: rgb(226 232 240 / 0.8);
}

.proxy-node-modal-body::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: rgb(148 163 184 / 0.85);
}

.dark .proxy-node-modal-body::-webkit-scrollbar-track {
  background: rgb(15 23 42 / 0.75);
}

.dark .proxy-node-modal-body::-webkit-scrollbar-thumb {
  background: rgb(71 85 105 / 0.95);
}

.proxy-node-modal-footer {
  display: flex;
  flex-shrink: 0;
  justify-content: flex-end;
  gap: 0.5rem;
  border-top: 1px solid rgb(229 231 235);
  padding: 0.75rem 1.25rem;
}

.dark .proxy-node-modal-footer {
  border-top-color: rgb(51 65 85);
}

.proxy-data-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 70;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: auto;
  background: rgb(0 0 0 / 0.45);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
  padding: 1rem;
}

.proxy-data-modal {
  display: flex;
  width: min(32rem, calc(100vw - 1rem));
  max-height: calc(100vh - 1rem);
  flex-direction: column;
  overflow: hidden;
}

.proxy-data-modal-body {
  min-height: 0;
  overflow-y: auto;
  padding: 1.5rem;
}

.proxy-data-modal-body p {
  font-size: 0.875rem;
  color: rgb(75 85 99);
}

.dark .proxy-data-modal-body p {
  color: rgb(203 213 225);
}

.proxy-import-warning {
  margin-top: 1.25rem;
  border: 1px solid rgb(249 115 22 / 0.85);
  border-radius: 0.55rem;
  background: rgb(249 115 22 / 0.08);
  padding: 0.75rem 0.85rem;
  font-size: 0.8rem;
  font-weight: 800;
  color: rgb(234 88 12);
}

.dark .proxy-import-warning {
  background: rgb(249 115 22 / 0.12);
  color: rgb(251 191 36);
}

.proxy-import-file-label {
  margin-top: 1.25rem;
  display: block;
}

.proxy-import-file-box {
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

.dark .proxy-import-file-box {
  border-color: rgb(71 85 105);
}

.proxy-import-file-button {
  flex-shrink: 0;
  border: 1px solid rgb(148 163 184 / 0.6);
  border-radius: 0.75rem;
  padding: 0.65rem 1rem;
  font-size: 0.8rem;
  font-weight: 800;
  color: rgb(51 65 85);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.proxy-import-file-button:hover {
  background: rgb(241 245 249);
}

.dark .proxy-import-file-button {
  border-color: rgb(71 85 105);
  color: rgb(226 232 240);
}

.dark .proxy-import-file-button:hover {
  background: rgb(51 65 85);
}

.proxy-data-modal-footer {
  display: flex;
  flex-shrink: 0;
  justify-content: flex-end;
  gap: 0.75rem;
  border-top: 1px solid rgb(226 232 240);
  padding: 1rem 1.5rem;
}

.dark .proxy-data-modal-footer {
  border-top-color: rgb(51 65 85);
}

@media (max-width: 1180px) {
  .proxy-stats-grid,
  .proxy-settings-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 767px) {
  .proxy-page {
    height: auto;
    min-height: 0;
    overflow: visible;
  }

  .proxy-table-wrap {
    min-height: 18rem;
    max-height: 60vh;
  }

  .proxy-modal-backdrop,
  .proxy-data-modal-backdrop {
    align-items: stretch;
    padding: 0.5rem;
  }

  .proxy-modal,
  .proxy-data-modal {
    width: calc(100vw - 1rem);
    max-height: calc(100svh - 1rem);
    border-radius: 0.875rem;
  }
}

@media (max-width: 640px) {
  .proxy-page {
    height: auto;
    min-height: auto;
    overflow: visible;
  }

  .proxy-stats-grid,
  .proxy-settings-grid,
  .proxy-form-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .proxy-table-toolbar {
    align-items: stretch;
    flex-direction: column;
    padding: 0.85rem;
  }

  .proxy-search {
    width: 100%;
  }

  .proxy-table-actions {
    justify-content: stretch;
  }

  .proxy-table-actions > button,
  .proxy-table-actions > .relative {
    flex: 1 1 auto;
  }

  .proxy-action-primary,
  .proxy-action-more,
  .proxy-action-refresh,
  .proxy-toolbar-batch-button,
  .proxy-toolbar-batch-danger {
    width: 100%;
  }

  .proxy-more-menu {
    right: auto;
    left: 0;
  }

  .proxy-pagination-footer {
    align-items: stretch;
  }

  .proxy-pagination-footer > div {
    align-items: stretch;
    flex-direction: column;
  }

  .proxy-pagination-footer nav {
    overflow-x: auto;
    padding-bottom: 0.1rem;
  }

  .page-size-menu {
    right: auto;
    left: 0;
  }

  .proxy-modal-backdrop {
    padding: 0.75rem;
  }

  .proxy-modal {
    max-height: calc(100vh - 1.5rem);
  }

  .proxy-data-modal-footer {
    flex-direction: column-reverse;
  }

  .proxy-node-modal-footer,
  .proxy-data-modal-footer {
    align-items: stretch;
  }

  .proxy-node-modal-footer > button,
  .proxy-data-modal-footer > button {
    width: 100%;
  }
}
</style>
