import { defineStore } from 'pinia'
import { ref } from 'vue'
import { accountApi, type AccountItem, type PoolConfig } from '../api/client'

const defaultPoolConfig: PoolConfig = {
  poolMode: true,
  projectPoolMode: false,
  geminiCliPoolMode: false,
  activeChannel: 'nvidia',
  otherLbModes: {},
  nvidiaLbMode: 'round-robin',
  grokLbMode: 'round-robin',
  nvidiaMaxConcurrency: 40,
  antigravityMaxConcurrency: 10,
  antigravityCliVersion: '2.3.1',
  projectMaxConcurrency: 10,
  otherMaxConcurrency: {},
  grokMaxConcurrency: 10,
  grokCliVersion: '1.0.0',
  grokQuotaCooldownHours: 24,
  workbuddyLbMode: 'round-robin',
  workbuddyMaxConcurrency: 10,
}

export const useAccountPoolStore = defineStore('accountPool', () => {
  const accounts = ref<AccountItem[]>([])
  const poolConfig = ref<PoolConfig>({ ...defaultPoolConfig })
  const otherGroups = ref<any[]>([])
  const isLoaded = ref(false)
  const loading = ref(false)

  async function fetchAccounts(force = false) {
    if (!force && isLoaded.value) {
      return accounts.value
    }
    loading.value = true
    try {
      const res = await accountApi.getAccounts()
      if (res) {
        accounts.value = res.accounts || []
        if (res.config) {
          poolConfig.value = {
            ...poolConfig.value,
            ...res.config,
            otherLbModes: res.config.otherLbModes || {},
            otherMaxConcurrency: res.config.otherMaxConcurrency || {},
          }
        }
        otherGroups.value = res.otherGroups || []
        isLoaded.value = true
      }
      return accounts.value
    } catch (err) {
      console.error('加载账号池数据失败:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function saveConfig(cfg?: Partial<PoolConfig>) {
    if (cfg) {
      poolConfig.value = { ...poolConfig.value, ...cfg }
    }
    try {
      await accountApi.savePoolConfig(poolConfig.value)
    } catch (err) {
      console.error('保存号池配置失败:', err)
      throw err
    }
  }

  async function toggleAccount(id: string, enabled: boolean) {
    await accountApi.toggleAccount(id, enabled)
    const target = accounts.value.find((a) => a.id === id)
    if (target) {
      target.enabled = enabled
    }
  }

  async function deleteAccount(id: string) {
    await accountApi.deleteAccount(id)
    accounts.value = accounts.value.filter((a) => a.id !== id)
  }

  async function batchDeleteAccounts(ids: string[]) {
    await accountApi.batchDeleteAccounts(ids)
    const idSet = new Set(ids)
    accounts.value = accounts.value.filter((a) => !idSet.has(a.id))
  }

  async function importAccounts(list: any[]) {
    const res = await accountApi.importAccounts(list)
    await fetchAccounts(true)
    return res
  }

  function reset() {
    accounts.value = []
    poolConfig.value = { ...defaultPoolConfig }
    otherGroups.value = []
    isLoaded.value = false
  }

  return {
    accounts,
    poolConfig,
    otherGroups,
    isLoaded,
    loading,
    fetchAccounts,
    saveConfig,
    toggleAccount,
    deleteAccount,
    batchDeleteAccounts,
    importAccounts,
    reset,
  }
})
