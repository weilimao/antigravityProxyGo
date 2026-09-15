import { reactive } from 'vue'

const API_BASE = '/api/v1'

export interface ApiResponse<T = any> {
  code: number
  success: boolean
  message: string
  data: T
}

export interface UserInfo {
  id?: number
  username: string
  email?: string
  role: string
  planId?: number
  planExpireAt?: string
}

export const authState = reactive({
  token: localStorage.getItem('antigravity_token') || '',
  user: null as UserInfo | null,
})

export function getToken(): string {
  return authState.token
}

export function setToken(token: string): void {
  authState.token = token
  localStorage.setItem('antigravity_token', token)
}

export function clearToken(): void {
  authState.token = ''
  authState.user = null
  localStorage.removeItem('antigravity_token')
}

export async function refreshCurrentUser(): Promise<UserInfo | null> {
  if (!authState.token) {
    authState.user = null
    return null
  }
  try {
    const u = await authApi.getMe()
    authState.user = u
    return u
  } catch {
    clearToken()
    return null
  }
}

export async function request<T = any>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const token = getToken()
  const headers = new Headers(options.headers || {})
  
  if (!headers.has('Content-Type') && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }

  const res = await fetch(`${API_BASE}${endpoint}`, {
    ...options,
    headers,
  })

  if (res.status === 401) {
    clearToken()
    if (window.location.hash !== '#/login') {
      window.location.hash = '#/login'
    }
    throw new Error('未登录或登录已过期')
  }

  const text = await res.text()
  let data: ApiResponse<T> | null = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    if (!res.ok) {
      if (res.status === 404) {
        throw new Error(`接口不存在 (404 Not Found)，请确认后端服务已重启生效: ${endpoint}`)
      }
      throw new Error(`请求失败 (HTTP ${res.status}): ${text.slice(0, 120)}`)
    }
    throw new Error(`服务器返回了非 JSON 格式响应: ${text.slice(0, 120)}`)
  }

  if (!res.ok) {
    throw new Error((data && data.message) || `请求失败 (HTTP ${res.status})`)
  }

  if (data && !data.success && data.code !== 0) {
    throw new Error(data.message || '请求失败')
  }

  return data?.data as T
}

// 认证服务
export const authApi = {
  login: (body: any) => request<any>('/auth/login', { method: 'POST', body: JSON.stringify(body) }),
  register: (body: any) => request<any>('/auth/register', { method: 'POST', body: JSON.stringify(body) }),
  getMe: () => request<any>('/auth/me'),
  changePassword: (body: any) => request<any>('/auth/password', { method: 'POST', body: JSON.stringify(body) }),
}

// 套餐服务
export const planApi = {
  listActive: () => request<any[]>('/plans'),
  adminList: () => request<any[]>('/admin/plans'),
  adminCreate: (body: any) => request<any>('/admin/plans', { method: 'POST', body: JSON.stringify(body) }),
  adminUpdate: (id: number, body: any) => request<any>(`/admin/plans/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  adminDelete: (id: number) => request<any>(`/admin/plans/${id}`, { method: 'DELETE' }),
}

// 订单与收银
export const checkoutApi = {
  createOrder: (planId: number) => request<any>('/checkout/create', { method: 'POST', body: JSON.stringify({ planId }) }),
  getOrderStatus: (orderNo: string) => request<any>(`/checkout/orders/${orderNo}`),
}

// API Key 凭证
export const keyApi = {
  list: () => request<any[]>('/keys'),
  create: (body: any) => request<any>('/keys', { method: 'POST', body: JSON.stringify(body) }),
  delete: (id: number) => request<any>(`/keys/${id}`, { method: 'DELETE' }),
}

// Auto 竞速规则
export const autoApi = {
  getUserConfig: () => request<any>('/user/auto-config'),
  setUserConfig: (body: any) => request<any>('/user/auto-config', { method: 'POST', body: JSON.stringify(body) }),
  getGlobalConfig: () => request<any>('/admin/models/auto-config'),
  setGlobalConfig: (body: any) => request<any>('/admin/models/auto-config', { method: 'POST', body: JSON.stringify(body) }),
  pullGlobalConfig: () => request<any>('/admin/models/auto-config/pull', { method: 'POST' }),
}

// 模型映射与 OCR
export const mappingApi = {
  getMappings: () => request<any[]>('/admin/models/mappings'),
  pullMappings: () => request<any>('/admin/models/mappings/pull', { method: 'POST' }),
  getMappingClientModels: () => request<string[]>('/admin/models/mapping-clients'),
  setMappings: (mappings: any[]) => request<any>('/admin/models/mappings', { method: 'POST', body: JSON.stringify({ mappings }) }),
  getAvailableModels: () => request<string[]>('/admin/models/available'),
  getOcrModel: () => request<{ ocrModel: string; ocrModels?: string[] }>('/admin/settings/ocr'),
  pullOcrModel: () => request<{ ocrModel: string; ocrModels?: string[] }>('/admin/settings/ocr/pull', { method: 'POST' }),
  setOcrModel: (data: { ocrModel?: string; ocrModels: string[] } | string) => {
    const body = typeof data === 'string' ? { ocrModel: data, ocrModels: data ? [data] : [] } : data
    return request<any>('/admin/settings/ocr', { method: 'POST', body: JSON.stringify(body) })
  },
}

// 管理员后台用户与订单
export const adminApi = {
  listUsers: (page = 1, pageSize = 10, search = '') => 
    request<any>(`/admin/users?page=${page}&pageSize=${pageSize}&search=${encodeURIComponent(search)}`),
  toggleUserStatus: (id: number) => request<any>(`/admin/users/${id}/status`, { method: 'PUT' }),
  assignPlan: (id: number, planId: number) => request<any>(`/admin/users/${id}/assign-plan`, { method: 'POST', body: JSON.stringify({ planId }) }),
  listOrders: (page = 1, pageSize = 10, status = '') => 
    request<any>(`/admin/orders?page=${page}&pageSize=${pageSize}&status=${status}`),
  fulfillOrder: (orderNo: string) => request<any>(`/admin/orders/${orderNo}/fulfill`, { method: 'POST' }),
}
