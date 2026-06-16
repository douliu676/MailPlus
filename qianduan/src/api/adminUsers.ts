export type AdminUser = {
  id: number
  username: string
  email: string
  avatar_url: string
  balance: number
  role: 'admin' | 'user'
  status: 'active' | 'disabled'
  created_at: string
}

export type UserListResponse = {
  items: AdminUser[]
  total: number
  page: number
  page_size: number
  pages: number
}

export type BalanceRecord = {
  id: number
  user_id: number
  type: 'deposit' | 'deduct'
  amount: number
  balance_after: number
  remark: string
  created_at: string
}

export type BalanceRecordResponse = {
  user: AdminUser
  records: BalanceRecord[]
}

export type UserPayload = {
  username: string
  email: string
  password?: string
  balance?: number
  role: 'admin' | 'user'
  enabled: boolean
}

export type UserSortBy = 'email' | 'id' | 'username' | 'role' | 'balance' | 'status' | 'created_at'

export type UserSortOrder = 'asc' | 'desc'

export type UserListParams = {
  page: number
  page_size: number
  search?: string
  role?: string
  status?: string
  sort_by?: UserSortBy
  sort_order?: UserSortOrder
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options?.headers || {}),
    },
  })
  const text = await response.text()
  let result: { code?: number; msg?: string; data?: T }

  try {
    result = text ? JSON.parse(text) : {}
  } catch {
    throw new Error(text || '\u63a5\u53e3\u8fd4\u56de\u683c\u5f0f\u9519\u8bef')
  }

  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '\u8bf7\u6c42\u5931\u8d25')
  }
  return result.data as T
}

export function listUsers(params: UserListParams) {
  const query = new URLSearchParams()
  query.set('page', String(params.page))
  query.set('page_size', String(params.page_size))
  if (params.search) query.set('search', params.search)
  if (params.role) query.set('role', params.role)
  if (params.status) query.set('status', params.status)
  if (params.sort_by) query.set('sort_by', params.sort_by)
  if (params.sort_order) query.set('sort_order', params.sort_order)
  return request<UserListResponse>(`/api/admin/users?${query.toString()}`)
}

export function createUser(payload: UserPayload) {
  return request<AdminUser>('/api/admin/users', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateUser(id: number, payload: UserPayload) {
  return request<AdminUser>(`/api/admin/users/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function updateUserStatus(id: number, status: 'active' | 'disabled') {
  return request<AdminUser>(`/api/admin/users/${id}/status`, {
    method: 'PATCH',
    body: JSON.stringify({ status }),
  })
}

export function updateUserBalance(id: number, payload: { amount: number; type: 'deposit' | 'deduct'; remark?: string }) {
  return request<AdminUser>(`/api/admin/users/${id}/balance`, {
    method: 'PATCH',
    body: JSON.stringify(payload),
  })
}

export function listUserBalanceRecords(id: number, type?: 'deposit' | 'deduct' | 'all') {
  const query = new URLSearchParams()
  if (type && type !== 'all') query.set('type', type)
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return request<BalanceRecordResponse>(`/api/admin/users/${id}/balance-records${suffix}`)
}

export function deleteUser(id: number) {
  return request<void>(`/api/admin/users/${id}`, {
    method: 'DELETE',
  })
}
