<template>
  <div class="fixed inset-0 z-[100] flex items-center justify-center p-4">
    <!-- Backdrop -->
    <div class="absolute inset-0 bg-black/60 backdrop-blur-sm transition-opacity" @click="$emit('close')"></div>
    
    <!-- Modal Panel -->
    <div class="relative bg-slate-900 border border-slate-700 rounded-2xl shadow-2xl w-full max-w-lg overflow-hidden flex flex-col max-h-[90vh]">
      <!-- Header -->
      <div class="px-6 py-4 border-b border-slate-800 flex justify-between items-center bg-slate-800/30">
        <h3 class="text-lg font-bold text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-indigo-400">settings</span>
          网关模型测速配置
        </h3>
        <button class="text-slate-400 hover:text-white transition-colors" @click="$emit('close')">
          <span class="material-symbols-outlined">close</span>
        </button>
      </div>

      <!-- Body -->
      <div class="p-6 overflow-y-auto flex-1 custom-scrollbar">
        <div class="flex flex-col gap-5">
          
          <!-- Enable Toggle -->
          <div class="flex items-center justify-between p-4 rounded-xl bg-slate-800/50 border border-slate-700/50">
            <div>
              <div class="text-sm font-semibold text-slate-200">启用定时自动测速</div>
              <div class="text-xs text-slate-500 mt-1">关闭后仍可手动触发测速</div>
            </div>
            <input type="checkbox" v-model="form.enabled" class="w-5 h-5 text-indigo-600 rounded bg-slate-900 border-slate-700 cursor-pointer" />
          </div>

          <!-- Models Selection -->
          <div class="flex flex-col gap-2">
            <label class="text-sm font-semibold text-slate-300">测试模型列表 <span class="text-red-400">*</span></label>
            <div class="text-xs text-slate-500 mb-1">
              请输入参与竞速的模型名称，每行一个
            </div>
            <textarea
              v-model="modelsText"
              class="w-full bg-slate-950 border border-slate-700 rounded-lg p-3 text-sm text-slate-300 focus:outline-none focus:border-indigo-500 min-h-[100px]"
              placeholder="gpt-4o
claude-3-5-sonnet"
            ></textarea>
            
            <div v-if="availableModels.length > 0" class="mt-2">
              <div class="text-xs text-slate-500 mb-1.5">快捷添加(从网关拉取):</div>
              <div class="flex flex-wrap gap-1.5">
                <button
                  v-for="m in availableModels"
                  :key="m"
                  type="button"
                  @click="toggleModel(m)"
                  class="px-2 py-0.5 rounded text-[11px] border transition-colors flex items-center gap-1"
                  :class="form.models.includes(m) ? 'bg-indigo-500/20 text-indigo-300 border-indigo-500/30' : 'bg-slate-800 text-slate-400 border-slate-700 hover:text-white hover:border-slate-500'"
                >
                  <span class="material-symbols-outlined text-[12px]">{{ form.models.includes(m) ? 'check' : 'add' }}</span>
                  {{ m }}
                </button>
              </div>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-4">
            <!-- Interval -->
            <div class="flex flex-col gap-2">
              <label class="text-sm font-semibold text-slate-300">测速间隔 (分钟)</label>
              <input type="number" v-model.number="form.intervalMinutes" min="1" max="1440" class="w-full bg-slate-950 border border-slate-700 rounded-lg p-2 text-sm text-slate-300 focus:outline-none focus:border-indigo-500" />
            </div>
            
            <!-- Timeout -->
            <div class="flex flex-col gap-2">
              <label class="text-sm font-semibold text-slate-300">超时时间 (毫秒)</label>
              <input type="number" v-model.number="form.timeoutMs" min="1000" max="120000" step="1000" class="w-full bg-slate-950 border border-slate-700 rounded-lg p-2 text-sm text-slate-300 focus:outline-none focus:border-indigo-500" />
            </div>
          </div>

          <!-- Prompt -->
          <div class="flex flex-col gap-2">
            <label class="text-sm font-semibold text-slate-300">测试 Prompt</label>
            <div class="text-xs text-slate-500 mb-1">发送给模型的最小探测文本，建议尽可能简短</div>
            <input type="text" v-model="form.prompt" class="w-full bg-slate-950 border border-slate-700 rounded-lg p-2 text-sm text-slate-300 focus:outline-none focus:border-indigo-500" placeholder="Hi" />
          </div>

        </div>
      </div>

      <!-- Footer -->
      <div class="px-6 py-4 border-t border-slate-800 bg-slate-900 flex justify-end gap-3">
        <button class="btn-secondary" @click="$emit('close')">取消</button>
        <button class="btn-primary flex items-center gap-1.5" @click="handleSave" :disabled="saving">
          <span class="material-symbols-outlined text-[16px]" :class="saving ? 'animate-spin' : ''">
            {{ saving ? 'sync' : 'save' }}
          </span>
          保存并应用
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { benchmarkApi } from '@/api/client'

const props = defineProps<{
  initialConfig: any
}>()

const emit = defineEmits(['close', 'saved'])

const saving = ref(false)
const availableModels = ref<string[]>([])

const form = ref({
  enabled: true,
  models: [] as string[],
  intervalMinutes: 5,
  timeoutMs: 30000,
  prompt: 'Hi'
})

if (props.initialConfig) {
  form.value = {
    enabled: props.initialConfig.enabled ?? true,
    models: [...(props.initialConfig.models || [])],
    intervalMinutes: props.initialConfig.intervalMinutes || 5,
    timeoutMs: props.initialConfig.timeoutMs || 30000,
    prompt: props.initialConfig.prompt || 'Hi'
  }
}

const modelsText = computed({
  get: () => form.value.models.join('\n'),
  set: (val) => {
    form.value.models = val.split('\n').map(s => s.trim()).filter(s => s)
  }
})

const toggleModel = (m: string) => {
  if (form.value.models.includes(m)) {
    form.value.models = form.value.models.filter(x => x !== m)
  } else {
    form.value.models.push(m)
  }
}

onMounted(async () => {
  try {
    const res = await benchmarkApi.getModels()
    if (res.success && res.models) {
      availableModels.value = res.models
    }
  } catch (e) {
    console.error('Failed to load available models', e)
  }
})

const handleSave = async () => {
  if (!form.value.models || form.value.models.length === 0) {
    alert('请至少输入一个测试模型')
    return
  }
  saving.value = true
  try {
    const res = await benchmarkApi.saveConfig({
      enabled: form.value.enabled,
      models: form.value.models,
      intervalMinutes: form.value.intervalMinutes,
      timeoutMs: form.value.timeoutMs,
      prompt: form.value.prompt
    })
    if (res.success) {
      alert('竞速测试配置已保存并生效')
      emit('saved')
    } else {
      alert(res.error || '保存失败')
    }
  } catch (error: any) {
    alert(error.message || '保存请求失败')
  } finally {
    saving.value = false
  }
}
</script>
