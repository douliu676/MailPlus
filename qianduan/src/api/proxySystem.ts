export type ProxyProtocol = 'http' | 'socks5' | 'vmess' | 'vless'

export type ProxyNode = {
  id: number
  name: string
  protocol: ProxyProtocol
  address: string
  port: number
  username: string
  password: string
  uuid: string
  alter_id: number
  security: string
  encryption: string
  transport: string
  tls: string
  sni: string
  path: string
  host_header: string
  flow: string
  fingerprint: string
  public_key: string
  short_id: string
  spider_x: string
  enabled: boolean
  local_port: number
  status: 'unchecked' | 'normal' | 'error'
  status_reason: string
  latency_ms: number
  last_tested_at: string
  remark: string
  created_at: string
  updated_at: string
}

export type ProxyNodeListParams = {
  ids?: number[]
  search?: string
  page?: number
  page_size?: number
  sort_by?: 'id' | 'name' | 'protocol' | 'address' | 'status' | 'latency' | 'created_at' | 'updated_at'
  sort_order?: 'asc' | 'desc'
}

export type ProxyNodeListResponse = {
  items: ProxyNode[]
  total: number
  page: number
  page_size: number
  pages: number
  normal: number
  error: number
  stats_total?: number
  stats_normal?: number
  stats_error?: number
}

export type ImportProxyNodesResponse = {
  count: number
}

export type SaveProxyNodePayload = Partial<Omit<ProxyNode, 'id' | 'local_port' | 'status' | 'status_reason' | 'latency_ms' | 'last_tested_at' | 'created_at' | 'updated_at'>> & {
  import_url?: string
}

export type ProxySetting = {
  scope: 'imap' | 'outlook'
  enabled: boolean
  proxy_node_id: number
  updated_at: string
}

export type ProxySettings = {
  imap: ProxySetting
  outlook: ProxySetting
}

export type ProxyRuntime = {
  running: boolean
  config_path: string
  last_error: string
  platform: string
  xray_bin: string
  xray_error?: string
  started_at?: string
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options?.headers || {}),
    },
  })
  const result = await response.json().catch(() => ({ code: 500, msg: '请求失败' }))
  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '请求失败')
  }
  return result.data as T
}

export function listProxyNodes(params: ProxyNodeListParams = {}) {
  const query = new URLSearchParams()
  if (params.ids?.length) query.set('ids', params.ids.join(','))
  if (params.search) query.set('search', params.search)
  if (params.page) query.set('page', String(params.page))
  if (params.page_size) query.set('page_size', String(params.page_size))
  if (params.sort_by) query.set('sort_by', params.sort_by)
  if (params.sort_order) query.set('sort_order', params.sort_order)
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<ProxyNodeListResponse>(`/api/admin/proxy/nodes${suffix}`)
}

export function createProxyNode(payload: SaveProxyNodePayload) {
  return request<ProxyNode>('/api/admin/proxy/nodes', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function importProxyNodes(nodes: SaveProxyNodePayload[]) {
  return request<ImportProxyNodesResponse>('/api/admin/proxy/nodes/import', {
    method: 'POST',
    body: JSON.stringify({ nodes }),
  })
}

export function updateProxyNode(id: number, payload: SaveProxyNodePayload) {
  return request<ProxyNode>(`/api/admin/proxy/nodes/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteProxyNode(id: number) {
  return request<void>(`/api/admin/proxy/nodes/${id}`, {
    method: 'DELETE',
  })
}

export function testProxyNode(id: number) {
  return request<ProxyNode>(`/api/admin/proxy/nodes/${id}/test`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
}

export function getProxySettings() {
  return request<ProxySettings>('/api/admin/proxy/settings')
}

export function updateProxySettings(payload: { imap: Pick<ProxySetting, 'enabled' | 'proxy_node_id'>; outlook: Pick<ProxySetting, 'enabled' | 'proxy_node_id'> }) {
  return request<ProxySettings>('/api/admin/proxy/settings', {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function getProxyRuntime() {
  return request<ProxyRuntime>('/api/admin/proxy/runtime')
}
