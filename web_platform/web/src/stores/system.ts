import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { systemApi, type SystemConfig } from '../api/client'

export const useSystemStore = defineStore('system', () => {
  const config = ref<SystemConfig | null>(null)
  const isLoaded = ref(false)
  const loading = ref(false)

  const siteName = computed(() => {
    const raw = config.value?.siteName
    if (raw && raw !== 'Antigravity Web' && raw !== 'open max api') {
      return raw
    }
    return 'MAX API'
  })

  const currentHostname = typeof window !== 'undefined' ? (window.location.hostname || '127.0.0.1') : '127.0.0.1'

  const apiBaseUrl = computed(() => {
    if (config.value?.apiBaseUrl) {
      return config.value.apiBaseUrl.trim().replace(/\/+$/, '')
    }
    return `http://${currentHostname}:18444`
  })

  const openaiBaseUrl = computed(() => `${apiBaseUrl.value}/v1`)
  const anthropicBaseUrl = computed(() => apiBaseUrl.value)

  async function fetchSystemConfig(force = false): Promise<SystemConfig | null> {
    if (!force && isLoaded.value && config.value) {
      return config.value
    }
    loading.value = true
    try {
      const cfg = await systemApi.getPublicConfig()
      config.value = cfg
      isLoaded.value = true
      return cfg
    } catch (err) {
      console.warn('获取系统公开配置失败:', err)
      return null
    } finally {
      loading.value = false
    }
  }

  return {
    config,
    isLoaded,
    loading,
    siteName,
    apiBaseUrl,
    openaiBaseUrl,
    anthropicBaseUrl,
    fetchSystemConfig,
  }
})
