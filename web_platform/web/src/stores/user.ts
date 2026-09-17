import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  authApi,
  keyApi,
  logsApi,
  type UserInfo,
  getToken as getClientToken,
  setToken as setClientToken,
  clearToken as clearClientToken,
  authState,
} from '../api/client'

export const useUserStore = defineStore('user', () => {
  const token = ref<string>(getClientToken())
  const user = ref<UserInfo | null>(authState.user || null)
  const isUserLoaded = ref(false)
  const loadingUser = ref(false)

  const keys = ref<any[]>([])
  const isKeysLoaded = ref(false)
  const loadingKeys = ref(false)

  const totalRequests = ref<number>(0)
  const isRequestsLoaded = ref(false)
  const loadingRequests = ref(false)

  const isLoggedIn = computed(() => !!token.value)
  const isAdmin = computed(() => user.value?.role === 'admin' || authState.user?.role === 'admin')
  const username = computed(() => user.value?.username || authState.user?.username || '')

  const extraTokens = computed(() => Number(user.value?.extraTokens) || 0)
  const hasActivePlan = computed(() => Boolean(user.value?.isActive && user.value?.plan))

  const planTokenLimit = computed(() => {
    const baseLimit = hasActivePlan.value ? (Number(user.value?.plan?.tokenLimit) || 0) : 0
    if (hasActivePlan.value && Number(user.value?.plan?.tokenLimit) <= 0) {
      return 0
    }
    return baseLimit + extraTokens.value
  })

  const isUnlimitedPlan = computed(() => {
    return hasActivePlan.value && Number(user.value?.plan?.tokenLimit) <= 0
  })

  const totalUsedTokens = computed(() => {
    return keys.value.reduce((acc, k) => acc + (Number(k.usedTokens) || 0), 0)
  })

  const remainingTokens = computed(() => {
    if (isUnlimitedPlan.value) return 0
    if (!hasActivePlan.value && extraTokens.value <= 0) return 0
    return Math.max(0, planTokenLimit.value - totalUsedTokens.value)
  })

  const usagePercent = computed(() => {
    if (!hasActivePlan.value || isUnlimitedPlan.value || planTokenLimit.value <= 0) return 0
    return Math.min(100, (totalUsedTokens.value / planTokenLimit.value) * 100)
  })

  const usageBadgeText = computed(() => {
    if (!hasActivePlan.value) return '未激活套餐'
    if (isUnlimitedPlan.value) return '不限配额'
    if (usagePercent.value >= 90) return '额度告急'
    if (usagePercent.value >= 70) return '注意余量'
    return '额度充裕'
  })

  const usageBadgeClass = computed(() => {
    if (!hasActivePlan.value) return 'bg-slate-500/15 text-slate-300 border border-slate-500/30'
    if (isUnlimitedPlan.value) return 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/30'
    if (usagePercent.value >= 90) return 'bg-rose-500/15 text-rose-300 border border-rose-500/30'
    if (usagePercent.value >= 70) return 'bg-amber-500/15 text-amber-300 border border-amber-500/30'
    return 'bg-indigo-500/15 text-indigo-300 border border-indigo-500/30'
  })

  const remainingTokensColor = computed(() => {
    if (!hasActivePlan.value) return 'text-slate-400'
    if (isUnlimitedPlan.value) return 'text-emerald-400'
    if (usagePercent.value >= 90) return 'text-rose-400'
    if (usagePercent.value >= 70) return 'text-amber-400'
    return 'text-emerald-400'
  })

  const usagePercentColor = computed(() => {
    if (!hasActivePlan.value) return 'text-slate-400'
    if (isUnlimitedPlan.value) return 'text-emerald-400'
    if (usagePercent.value >= 90) return 'text-rose-400'
    if (usagePercent.value >= 70) return 'text-amber-400'
    return 'text-indigo-400'
  })

  const progressBarClass = computed(() => {
    if (usagePercent.value >= 90) return 'bg-gradient-to-r from-rose-600 via-rose-500 to-red-400'
    if (usagePercent.value >= 70) return 'bg-gradient-to-r from-amber-500 via-orange-500 to-amber-400'
    return 'bg-gradient-to-r from-indigo-500 via-cyan-400 to-emerald-400'
  })

  const selectableModels = computed(() => {
    const set = new Set<string>()
    if (user.value?.plan?.allowedModels && Array.isArray(user.value.plan.allowedModels)) {
      for (const m of user.value.plan.allowedModels) {
        if (m && m.trim()) set.add(m.trim())
      }
    }
    if (user.value?.plan?.autoModels && Array.isArray(user.value.plan.autoModels)) {
      for (const m of user.value.plan.autoModels) {
        if (m && m.trim()) set.add(m.trim())
      }
    }
    set.add('auto')
    return Array.from(set)
  })

  async function fetchUserProfile(force = false): Promise<UserInfo | null> {
    if (!token.value) {
      user.value = null
      isUserLoaded.value = false
      return null
    }
    if (!force && isUserLoaded.value && user.value) {
      return user.value
    }
    loadingUser.value = true
    try {
      const u = await authApi.getMe()
      user.value = u
      authState.user = u
      isUserLoaded.value = true
      return u
    } catch (err) {
      console.warn('获取当前用户信息失败:', err)
      if (!force && user.value) {
        return user.value
      }
      user.value = null
      isUserLoaded.value = false
      throw err
    } finally {
      loadingUser.value = false
    }
  }

  async function fetchKeys(force = false): Promise<any[]> {
    if (!token.value) {
      keys.value = []
      isKeysLoaded.value = false
      return []
    }
    if (!force && isKeysLoaded.value) {
      return keys.value
    }
    loadingKeys.value = true
    try {
      const list = await keyApi.list()
      keys.value = list || []
      isKeysLoaded.value = true
      return keys.value
    } catch (err) {
      console.warn('获取 API Key 列表失败:', err)
      return keys.value
    } finally {
      loadingKeys.value = false
    }
  }

  async function fetchRequestCount(force = false): Promise<number> {
    if (!token.value) {
      totalRequests.value = 0
      isRequestsLoaded.value = false
      return 0
    }
    if (!force && isRequestsLoaded.value) {
      return totalRequests.value
    }
    loadingRequests.value = true
    try {
      const res = await logsApi.getUserLogs({ pageSize: 1 })
      totalRequests.value = res?.summary?.totalRequests ?? res?.total ?? 0
      isRequestsLoaded.value = true
      return totalRequests.value
    } catch (err) {
      console.warn('获取请求次数统计失败:', err)
      return totalRequests.value
    } finally {
      loadingRequests.value = false
    }
  }

  async function refreshAll() {
    await Promise.all([
      fetchUserProfile(true),
      fetchKeys(true),
      fetchRequestCount(true),
    ])
  }

  async function createKey(body: any) {
    const res = await keyApi.create(body)
    await fetchKeys(true)
    return res
  }

  async function deleteKey(id: number) {
    await keyApi.delete(id)
    keys.value = keys.value.filter((k) => k.id !== id)
  }

  function setAuthToken(newToken: string) {
    token.value = newToken
    setClientToken(newToken)
  }

  function logout() {
    token.value = ''
    user.value = null
    keys.value = []
    totalRequests.value = 0
    isUserLoaded.value = false
    isKeysLoaded.value = false
    isRequestsLoaded.value = false
    clearClientToken()
  }

  return {
    token,
    user,
    isUserLoaded,
    loadingUser,
    keys,
    isKeysLoaded,
    loadingKeys,
    totalRequests,
    isRequestsLoaded,
    loadingRequests,
    isLoggedIn,
    isAdmin,
    username,
    extraTokens,
    hasActivePlan,
    planTokenLimit,
    isUnlimitedPlan,
    totalUsedTokens,
    remainingTokens,
    usagePercent,
    usageBadgeText,
    usageBadgeClass,
    remainingTokensColor,
    usagePercentColor,
    progressBarClass,
    selectableModels,
    fetchUserProfile,
    fetchKeys,
    fetchRequestCount,
    refreshAll,
    createKey,
    deleteKey,
    setAuthToken,
    logout,
  }
})
