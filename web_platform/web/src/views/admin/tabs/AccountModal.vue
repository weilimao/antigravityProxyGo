<template>
  <Teleport to="body">
    <div
      v-if="show"
      class="fixed inset-0 z-[999] flex items-center justify-center p-4 bg-black/75 backdrop-blur-sm animate-fade-in"
      @click.self="$emit('close')"
    >
      <div class="bg-slate-900 border border-slate-700/80 rounded-2xl w-full max-w-lg shadow-2xl overflow-hidden flex flex-col max-h-[90vh]">
        <!-- 弹窗 Header -->
        <div class="px-6 py-4 border-b border-slate-800 flex items-center justify-between bg-slate-900/60">
          <div class="flex items-center gap-2.5">
            <span class="material-symbols-outlined text-indigo-400">
              {{ editAccount ? 'edit' : 'add_circle' }}
            </span>
            <h3 class="text-base font-bold text-white">
              {{ editAccount ? '编辑账号配置' : '添加新账号' }}
            </h3>
          </div>
          <button
            type="button"
            class="text-slate-400 hover:text-white p-1 rounded-lg hover:bg-slate-800 transition-colors"
            @click="$emit('close')"
          >
            <span class="material-symbols-outlined text-18px">close</span>
          </button>
        </div>

        <!-- 错误提示 -->
        <div v-if="errorMessage" class="mx-6 mt-4 p-3 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-300 text-xs flex items-center gap-2">
          <span class="material-symbols-outlined text-16px text-rose-400">error</span>
          <span>{{ errorMessage }}</span>
        </div>

        <!-- 表单区域 -->
        <div class="p-6 space-y-4 overflow-y-auto flex-1 text-xs">
          <!-- 通道类别 -->
          <div>
            <label class="block text-slate-400 mb-1.5 font-medium">号池通道 (Provider)</label>
            <select
              v-model="form.provider"
              :disabled="!!editAccount"
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500 disabled:opacity-60"
              @change="onProviderChange"
            >
              <option value="nvidia">NVIDIA 号池 (第三方 API Key)</option>
              <option value="other">Other 自定义上游号池</option>
              <option value="grok">Grok 号池 (xAI API Key)</option>
              <option value="workbuddy">WorkBuddy 号池 (官方账号)</option>
              <option value="antigravity">Antigravity 官方账号</option>
              <option value="project">谷歌云项目 API</option>
            </select>
          </div>

          <!-- Other 号池选择已有组 -->
          <div v-if="form.provider === 'other' && existingOtherGroups && existingOtherGroups.length > 0 && !editAccount">
            <label class="block text-slate-400 mb-1.5 font-medium">选择已有渠道组 (快速填充)</label>
            <select
              v-model="selectedGroupId"
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500"
              @change="onSelectExistingGroup"
            >
              <option value="">-- 手动创建新渠道组 --</option>
              <option v-for="g in existingOtherGroups" :key="g.groupId" :value="g.groupId">
                {{ g.groupName }} ({{ g.groupId }})
              </option>
            </select>
          </div>

          <!-- Other 号池分组 ID 与名称 -->
          <div v-if="form.provider === 'other'" class="grid grid-cols-2 gap-3">
            <div>
              <label class="block text-slate-400 mb-1.5 font-medium">组唯一标识 (Group ID) *</label>
              <input
                v-model="form.groupId"
                type="text"
                placeholder="例如: aliyun / bitdeer"
                class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500"
              />
            </div>
            <div>
              <label class="block text-slate-400 mb-1.5 font-medium">组展示名 (Group Name) *</label>
              <input
                v-model="form.groupName"
                type="text"
                placeholder="例如: 阿里云百炼 / Bitdeer"
                class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500"
              />
            </div>
          </div>

          <!-- 账号标识 / 邮箱 -->
          <div>
            <label class="block text-slate-400 mb-1.5 font-medium">账号标识 / 邮箱 (Email / Label) *</label>
            <input
              v-model="form.email"
              type="text"
              placeholder="例如: user@domain.com 或 token_alias"
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500"
            />
          </div>

          <!-- API Key / Token -->
          <div>
            <label class="block text-slate-400 mb-1.5 font-medium">
              API Key / 访问令牌 (Access Token)
              <span v-if="editAccount" class="text-slate-500 font-normal ml-1">(留空表示保持当前 Key 不变)</span>
              <span v-else class="text-rose-400">*</span>
            </label>
            <input
              v-model="form.accessToken"
              type="password"
              :placeholder="editAccount ? '•••••••• 已配置，留空不修改' : '填入 API 访问凭据 Key'"
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500 font-mono"
            />
          </div>

          <!-- Base URL -->
          <div>
            <label class="block text-slate-400 mb-1.5 font-medium">上游端点 (Base URL)</label>
            <input
              v-model="form.baseUrl"
              type="text"
              placeholder="例如: https://integrate.api.nvidia.com/v1"
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500 font-mono"
            />
          </div>

          <!-- Other 协议格式 -->
          <div v-if="form.provider === 'other'">
            <label class="block text-slate-400 mb-1.5 font-medium">兼容协议格式 (多选)</label>
            <div class="flex items-center gap-4 pt-1">
              <label class="flex items-center gap-2 cursor-pointer text-slate-300">
                <input
                  type="checkbox"
                  value="openai"
                  :checked="form.formats.includes('openai')"
                  class="rounded bg-slate-800 border-slate-700 text-indigo-500 focus:ring-0"
                  @change="toggleFormat('openai')"
                />
                <span>OpenAI (/v1/chat/completions)</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer text-slate-300">
                <input
                  type="checkbox"
                  value="anthropic"
                  :checked="form.formats.includes('anthropic')"
                  class="rounded bg-slate-800 border-slate-700 text-indigo-500 focus:ring-0"
                  @change="toggleFormat('anthropic')"
                />
                <span>Anthropic (/v1/messages)</span>
              </label>
            </div>
          </div>

          <!-- 默认模型 -->
          <div>
            <label class="block text-slate-400 mb-1.5 font-medium">默认模型 (Default Model，可选)</label>
            <input
              v-model="form.defaultModel"
              type="text"
              placeholder="例如: moonshotai/kimi-k2.6 或 deepseek-ai/deepseek-v3"
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500"
            />
          </div>
        </div>

        <!-- 弹窗 Footer -->
        <div class="px-6 py-4 border-t border-slate-800 flex items-center justify-end gap-3 bg-slate-900/60">
          <button
            type="button"
            class="px-4 py-2 rounded-lg text-xs font-semibold text-slate-300 hover:text-white hover:bg-slate-800 transition-colors"
            @click="$emit('close')"
          >
            取消
          </button>
          <button
            type="button"
            :disabled="saving"
            class="px-5 py-2 rounded-lg text-xs font-semibold bg-indigo-600 hover:bg-indigo-500 text-white transition-all shadow-md shadow-indigo-600/30 flex items-center gap-2 disabled:opacity-50"
            @click="handleSubmit"
          >
            <span v-if="saving" class="material-symbols-outlined text-16px animate-spin">progress_activity</span>
            <span>{{ editAccount ? '保存修改' : '确认添加' }}</span>
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { accountApi, type AccountItem } from '../../../api/client'

const props = defineProps<{
  show: boolean
  editAccount: AccountItem | null
  currentChannel: string
  existingOtherGroups?: Array<{ groupId: string; groupName: string; formats: string[] }>
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved'): void
}>()

const saving = ref(false)
const errorMessage = ref('')
const selectedGroupId = ref('')

const form = ref({
  provider: 'nvidia',
  email: '',
  accessToken: '',
  baseUrl: '',
  groupId: '',
  groupName: '',
  formats: ['openai'] as string[],
  defaultModel: '',
})

watch(
  () => props.show,
  (val) => {
    if (val) {
      errorMessage.value = ''
      selectedGroupId.value = ''
      if (props.editAccount) {
        form.value = {
          provider: props.editAccount.provider || 'nvidia',
          email: props.editAccount.email || '',
          accessToken: '',
          baseUrl: props.editAccount.baseUrl || '',
          groupId: props.editAccount.groupId || '',
          groupName: props.editAccount.groupName || '',
          formats: props.editAccount.formats && props.editAccount.formats.length > 0 ? [...props.editAccount.formats] : ['openai'],
          defaultModel: props.editAccount.defaultModel || '',
        }
      } else {
        const p = props.currentChannel || 'nvidia'
        form.value = {
          provider: p,
          email: '',
          accessToken: '',
          baseUrl: defaultBaseUrl(p),
          groupId: '',
          groupName: '',
          formats: ['openai'],
          defaultModel: '',
        }
      }
    }
  },
  { immediate: true }
)

function defaultBaseUrl(provider: string): string {
  switch (provider) {
    case 'nvidia':
      return 'https://integrate.api.nvidia.com/v1'
    case 'grok':
      return 'https://cli-chat-proxy.grok.com'
    case 'workbuddy':
      return 'https://www.codebuddy.ai'
    default:
      return ''
  }
}

function onProviderChange() {
  if (!props.editAccount) {
    form.value.baseUrl = defaultBaseUrl(form.value.provider)
  }
}

function onSelectExistingGroup() {
  if (!selectedGroupId.value || !props.existingOtherGroups) return
  const g = props.existingOtherGroups.find((x) => x.groupId === selectedGroupId.value)
  if (g) {
    form.value.groupId = g.groupId
    form.value.groupName = g.groupName
    form.value.formats = g.formats && g.formats.length > 0 ? [...g.formats] : ['openai']
  }
}

function toggleFormat(fmt: string) {
  const idx = form.value.formats.indexOf(fmt)
  if (idx >= 0) {
    if (form.value.formats.length > 1) {
      form.value.formats.splice(idx, 1)
    }
  } else {
    form.value.formats.push(fmt)
  }
}

async function handleSubmit() {
  errorMessage.value = ''
  if (!form.value.email.trim()) {
    errorMessage.value = '请输入账号标识或邮箱'
    return
  }
  if (!props.editAccount && !form.value.accessToken.trim()) {
    errorMessage.value = '请输入 API Key 或访问凭据'
    return
  }
  if (form.value.provider === 'other' && (!form.value.groupId.trim() || !form.value.groupName.trim())) {
    errorMessage.value = 'Other 号池请填写渠道组唯一标识与展示名'
    return
  }

  saving.value = true
  try {
    if (props.editAccount) {
      await accountApi.updateAccount(props.editAccount.id, {
        email: form.value.email,
        access_token: form.value.accessToken,
        baseUrl: form.value.baseUrl,
        groupId: form.value.groupId,
        groupName: form.value.groupName,
        formats: form.value.formats,
        defaultModel: form.value.defaultModel,
      })
    } else {
      await accountApi.addAccount({
        provider: form.value.provider,
        email: form.value.email,
        access_token: form.value.accessToken,
        baseUrl: form.value.baseUrl,
        groupId: form.value.groupId,
        groupName: form.value.groupName,
        formats: form.value.formats,
        defaultModel: form.value.defaultModel,
      })
    }
    emit('saved')
    emit('close')
  } catch (err: any) {
    errorMessage.value = err?.message || '保存失败'
  } finally {
    saving.value = false
  }
}
</script>
