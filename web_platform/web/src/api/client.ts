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
  status?: string
  planId?: number
  planExpireAt?: number | string
  extraTokens?: number
  isActive?: boolean
  plan?: any
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
  logout: () => request<any>('/auth/logout', { method: 'POST' }),
  getMe: () => request<any>('/auth/me'),
  changePassword: (body: any) => request<any>('/auth/password', { method: 'POST', body: JSON.stringify(body) }),
}

// 套餐服务
export const planApi = {
  listActive: () => request<any[]>('/plans'),
  getDetail: (id: number) => request<any>(`/plans/${id}`),
  adminList: () => request<any[]>('/admin/plans'),
  adminCreate: (body: any) => request<any>('/admin/plans', { method: 'POST', body: JSON.stringify(body) }),
  adminUpdate: (id: number, body: any) => request<any>(`/admin/plans/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  adminDelete: (id: number) => request<any>(`/admin/plans/${id}`, { method: 'DELETE' }),
}

// 订单与收银
export const checkoutApi = {
  getQuote: (planId: number) => request<any>(`/checkout/quote?planId=${planId}`),
  createOrder: (planId: number) => request<any>('/checkout/create', { method: 'POST', body: JSON.stringify({ planId }) }),
  getOrderStatus: (orderNo: string) => request<any>(`/checkout/orders/${orderNo}`),
}

// 用户订单管理 (对标 ProxySubForClash)
export const orderApi = {
  listMyOrders: (page = 1, pageSize = 20, status = '') => {
    const params = new URLSearchParams()
    params.set('page', String(page))
    params.set('pageSize', String(pageSize))
    if (status) params.set('status', status)
    return request<{ list: any[]; total: number; page: number; pageSize: number }>(`/user/orders?${params.toString()}`)
  },
  getPayUrl: (orderNo: string) => request<{ orderNo: string; payUrl: string }>(`/user/orders/${orderNo}/pay-url`),
  cancelOrder: (orderNo: string) => request<any>(`/user/orders/${orderNo}/cancel`, { method: 'POST' }),
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

export const benchmarkApi = {
  get: () => request<any>('/admin/benchmark'),
  saveConfig: (body: any) => request<any>('/admin/benchmark/config', { method: 'POST', body: JSON.stringify(body) }),
  run: () => request<any>('/admin/benchmark/run', { method: 'POST' }),
  runModel: (model: string) => request<any>('/admin/benchmark/run-model', { method: 'POST', body: JSON.stringify({ model }) }),
  getModels: () => request<any>('/admin/benchmark/models'),
}

// 模型映射与 OCR
export const mappingApi = {
  getMappings: () => request<any[]>('/admin/models/mappings'),
  getOtherGroups: () => request<{ success: boolean; groups: any[] }>('/admin/models/other-groups'),
  pullMappings: () => request<any>('/admin/models/mappings/pull', { method: 'POST' }),
  getMappingClientModels: () => request<string[]>('/admin/models/mapping-clients'),
  setMappings: (mappings: any[]) => request<any>('/admin/models/mappings', { method: 'POST', body: JSON.stringify({ mappings }) }),
  getAvailableModels: () => request<string[]>('/admin/models/available'),
  fetchChannelModels: (channel: string) => request<any>('/admin/models/fetch-channel', { method: 'POST', body: JSON.stringify({ channel }) }),
  fetchOtherGroupModels: (groupId: string) => request<any>('/admin/models/fetch-other', { method: 'POST', body: JSON.stringify({ groupId }) }),
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

// 支付跳转与收银切单配置
export interface PaymentConfig {
  payProvider: string
  siteUrl: string
  payReturnUrl: string
  relayUrl: string
  relayCheckoutBase: string
  relaySecret: string
  relayNotifyUrl: string
  epayUrl: string
  epayPid: string
  epayKey: string
  epayType: string
  epayNotifyUrl: string
}

export const paymentApi = {
  getConfig: () => request<PaymentConfig>('/admin/settings/payment'),
  saveConfig: (cfg: PaymentConfig) => request<any>('/admin/settings/payment', { method: 'POST', body: JSON.stringify(cfg) }),
}

// 账号池数据结构与管理 API
export interface AccountItem {
  id: string
  email: string
  access_token?: string
  refresh_token?: string
  provider: string
  projectId?: string
  projectLabel?: string
  scopeType?: string
  addedAt?: string
  tier?: string
  enabled: boolean
  enableOverages?: boolean
  credits?: number | null
  maskedKey?: string
  baseUrl?: string
  egressIp?: string
  tokenEndpoint?: string
  defaultModel?: string
  modelSonnet?: string
  modelOpus?: string
  modelHaiku?: string
  modelFable?: string
  groupId?: string
  groupName?: string
  formats?: string[]
  cooldownUntil?: number
}

export interface PoolConfig {
  poolMode: boolean
  projectPoolMode: boolean
  geminiCliPoolMode: boolean
  activeChannel: string
  otherLbModes: Record<string, string>
  nvidiaLbMode: string
  grokLbMode: string
  nvidiaMaxConcurrency: number
  antigravityMaxConcurrency: number
  antigravityCliVersion: string
  projectMaxConcurrency: number
  otherMaxConcurrency: Record<string, number>
  grokMaxConcurrency: number
  grokCliVersion: string
  grokQuotaCooldownHours: number
  workbuddyLbMode?: string
  workbuddyMaxConcurrency?: number
}

export interface AccountsResponse {
  accounts: AccountItem[]
  config: PoolConfig
  otherGroups: Array<{
    groupId: string
    groupName: string
    formats: string[]
    accountCount: number
    enabledCount: number
  }>
}

export const accountApi = {
  getAccounts: () => request<AccountsResponse>('/admin/accounts'),
  savePoolConfig: (body: PoolConfig) => request<any>('/admin/accounts/pool-config', { method: 'POST', body: JSON.stringify(body) }),
  addAccount: (body: any) => request<AccountItem>('/admin/accounts', { method: 'POST', body: JSON.stringify(body) }),
  updateAccount: (id: string, body: any) => request<AccountItem>(`/admin/accounts/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteAccount: (id: string) => request<any>(`/admin/accounts/${id}`, { method: 'DELETE' }),
  batchDeleteAccounts: (ids: string[]) => request<any>('/admin/accounts/batch-delete', { method: 'POST', body: JSON.stringify({ ids }) }),
  toggleAccount: (id: string, enabled: boolean) => request<any>(`/admin/accounts/${id}/toggle`, { method: 'PUT', body: JSON.stringify({ enabled }) }),
  importAccounts: (accounts: any[]) => request<any>('/admin/accounts/import', { method: 'POST', body: JSON.stringify({ accounts }) }),
  exportAccounts: (channel = '') => request<{ channel: string; count: number; accounts: AccountItem[] }>(`/admin/accounts/export?channel=${encodeURIComponent(channel)}`),
}

// 系统全局与对外 API 服务配置
export interface SystemConfig {
  apiBaseUrl: string
  siteName?: string
  announcement?: string
}

export const systemApi = {
  getPublicConfig: () => request<SystemConfig>('/system/config'),
  getAdminConfig: () => request<SystemConfig>('/admin/settings/system'),
  saveAdminConfig: (cfg: SystemConfig) => request<any>('/admin/settings/system', { method: 'POST', body: JSON.stringify(cfg) }),
}

// 请求命中模型日志类型与 API
export interface LogItem {
  id: number
  reqId: string
  timestamp: string
  method: string
  host: string
  path: string
  sessionId: string
  model: string
  account: string
  inTokens: number
  outTokens: number
  cachedTokens: number
  cost: number
  inputCost: number
  outputCost: number
  cachedCost: number
  firstByteMs: number
  durationMs: number
  cacheStatus: string
  statusCode: number
  family: string
  reasoningEffort: string
  requestBody?: string
  requestHeaders?: string
  responseBody?: string
  responseHeaders?: string
  errorMessage?: string
}

export interface LogSummary {
  totalRequests: number
  totalCost: number
  totalInputTokens?: number
  totalOutputTokens?: number
  totalCachedTokens?: number
  inputTokens?: number
  outputTokens?: number
  cachedTokens?: number
  inputCost: number
  outputCost: number
  cachedCost: number
  cacheHitRate: number
}

export interface ModelPerfStat {
  model: string
  count: number
  avgDurationMs: number
  avgTtftMs: number
}

export interface LogsResponse {
  success: boolean
  total: number
  page: number
  pageSize: number
  summary: LogSummary
  modelPerf: ModelPerfStat[]
  list: LogItem[]
  accounts: string[]
}

export const logsApi = {
  getUserLogs: (params: { account?: string; status?: string; search?: string; page?: number; pageSize?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.account) q.set('account', params.account)
    if (params.status) q.set('status', params.status)
    if (params.search) q.set('search', params.search)
    if (params.page) q.set('page', String(params.page))
    if (params.pageSize) q.set('pageSize', String(params.pageSize))
    return request<LogsResponse>(`/user/logs?${q.toString()}`)
  },
  getUserLogDetail: (reqId: string, id?: number) => {
    const q = new URLSearchParams()
    if (reqId) q.set('req_id', reqId)
    if (id) q.set('id', String(id))
    return request<LogItem>(`/user/logs/detail?${q.toString()}`)
  },
  getUserLogAccounts: () => request<string[]>('/user/logs/accounts'),
  getAdminLogs: (params: { account?: string; status?: string; search?: string; page?: number; pageSize?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.account) q.set('account', params.account)
    if (params.status) q.set('status', params.status)
    if (params.search) q.set('search', params.search)
    if (params.page) q.set('page', String(params.page))
    if (params.pageSize) q.set('pageSize', String(params.pageSize))
    return request<LogsResponse>(`/admin/logs?${q.toString()}`)
  },
  getAdminLogDetail: (reqId: string, id?: number) => {
    const q = new URLSearchParams()
    if (reqId) q.set('req_id', reqId)
    if (id) q.set('id', String(id))
    return request<LogItem>(`/admin/logs/detail?${q.toString()}`)
  },
  getLogAccounts: () => request<string[]>('/admin/logs/accounts'),
}


