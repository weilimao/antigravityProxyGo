<template>
  <div class="w-full flex flex-col gap-6">
    <div class="glass-card p-6">
      <div class="flex items-center justify-between gap-4 mb-4 pb-3 border-b border-slate-800">
        <div class="flex items-center gap-3 min-w-0">
          <span class="w-10 h-10 rounded-xl bg-indigo-500/10 text-indigo-400 flex items-center justify-center border border-indigo-500/20 shrink-0">
            <span class="material-symbols-outlined text-24px">document_scanner</span>
          </span>
          <div class="min-w-0">
            <h3 class="text-base font-bold text-white">OCR 图像自愈与预处理降级模型配置</h3>
            <p class="text-xs text-slate-400 truncate sm:whitespace-normal">
              当客户端向不支持原生视觉（非多模态）的上游模型发送图片时，网关调用此 OCR 模型自动提取文字与视觉描述
            </p>
          </div>
        </div>

        <div class="flex items-center gap-2 shrink-0">
          <button
            type="button"
            :disabled="pulling"
            class="btn-secondary text-xs flex items-center gap-1.5 whitespace-nowrap"
            @click="pullOcrModel"
            title="从远端 18444 服务端网关拉取当前生效配置"
          >
            <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': pulling }">cloud_download</span>
            <span>{{ pulling ? '拉取中...' : '从服务端拉取' }}</span>
          </button>
          <button
            type="button"
            :disabled="saving"
            class="btn-primary text-xs flex items-center gap-1.5 whitespace-nowrap"
            @click="saveOcrModel"
          >
            <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': saving }">save</span>
            <span>{{ saving ? '保存中...' : '保存 OCR 配置' }}</span>
          </button>
        </div>
      </div>

      <!-- 同步反馈提示条 -->
      <div v-if="syncMsg" class="mb-4 p-3 rounded-lg flex items-center justify-between text-xs" :class="syncSuccess ? 'bg-emerald-500/10 text-emerald-300 border border-emerald-500/30' : 'bg-rose-500/10 text-rose-300 border border-rose-500/30'">
        <div class="flex items-center gap-2">
          <span class="material-symbols-outlined text-16px">{{ syncSuccess ? 'check_circle' : 'error' }}</span>
          <span>{{ syncMsg }}</span>
        </div>
        <button type="button" @click="syncMsg = ''" class="hover:opacity-80">
          <span class="material-symbols-outlined text-14px">close</span>
        </button>
      </div>

      <div class="flex flex-col gap-4 text-xs">
        <div>
          <div class="flex items-center justify-between mb-1.5">
            <label class="block font-semibold text-slate-200">
              全局 OCR 图片解析候选模型池 (并发竞速模式)
            </label>
            <span v-if="ocrModels.length > 0" class="text-11px text-indigo-400 font-mono">
              已选 {{ ocrModels.length }} 个候选模型
            </span>
            <span v-else class="text-11px text-slate-500">
              未配置 (网关静默)
            </span>
          </div>
          <div class="w-full">
            <ModelSearchSelect
              :model-ids="ocrModels"
              :options="availableModels"
              :allow-custom="true"
              :multiple="true"
              placeholder="请搜索、勾选或手动输入候选 OCR 多模态模型（可多选）..."
              @update:model-ids="(val) => ocrModels = val"
            />
          </div>
          <p class="text-11px text-slate-500 mt-1.5">
            支持配置多个多模态视觉模型。当配置多个模型时，网关将启动并发竞速模式，以最快返回的结果自愈请求；未配置时网关不执行图片 OCR 预处理。
          </p>
        </div>

        <!-- 工作原理图解与说明 -->
        <div class="p-4 rounded-xl bg-slate-900/40 border border-slate-800/80 text-slate-400 space-y-2">
          <div class="flex items-center gap-1.5 text-slate-300 font-bold text-xs">
            <span class="material-symbols-outlined text-emerald-400 text-16px">bolt</span>
            <span>自愈降级并发竞速原理解析 (零400错误 + 极速响应保障)</span>
          </div>
          <p class="text-11px leading-relaxed">
            1. 当开发者在 Cursor、NextChat 或 Claude Code 中发送包含图片附件的提问，而映射的目标上游是只支持文本的纯文本大模型时；<br/>
            2. 网关内部的 <code class="text-slate-300 font-mono">OCRService</code> 提前捕获图片 Base64 编码，<strong class="text-indigo-300">并发向上述候选池中的所有模型发起图像识别解析</strong>；<br/>
            3. 网关采纳首个成功返回的视觉描述并即刻写入缓存，将其拼装入 Prompt 对话上下文中透明转发给上游，杜绝上游 400 Unsupported Multimodal 报错，同时消除单点模型限流或网络卡顿造成的延迟。
          </p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { mappingApi } from '../../../api/client'
import ModelSearchSelect from '../../../components/common/ModelSearchSelect.vue'

const ocrModels = ref<string[]>([])
const availableModels = ref<string[]>([])
const saving = ref(false)
const pulling = ref(false)
const syncMsg = ref('')
const syncSuccess = ref(true)

function onModelsSelect(val: string | string[]) {
  if (Array.isArray(val)) {
    ocrModels.value = val.map(m => m.trim()).filter(Boolean)
  } else if (typeof val === 'string' && val.trim() !== '') {
    ocrModels.value = [val.trim()]
  } else {
    ocrModels.value = []
  }
}

async function fetchAvailableModels() {
  try {
    const list = await mappingApi.getAvailableModels()
    if (Array.isArray(list) && list.length > 0) {
      availableModels.value = list
    }
  } catch (err) {
    console.warn('获取可用模型列表失败:', err)
  }
}

async function fetchOcrModel() {
  try {
    const res = await mappingApi.getOcrModel()
    if (res) {
      if (Array.isArray(res.ocrModels) && res.ocrModels.length > 0) {
        ocrModels.value = res.ocrModels
      } else if (res.ocrModel) {
        ocrModels.value = [res.ocrModel]
      } else {
        ocrModels.value = []
      }
    }
  } catch (err) {
    console.error('获取 OCR 模型失败:', err)
  }
}

async function pullOcrModel() {
  pulling.value = true
  syncMsg.value = ''
  try {
    const res = await mappingApi.pullOcrModel()
    if (res) {
      if (Array.isArray(res.ocrModels) && res.ocrModels.length > 0) {
        ocrModels.value = res.ocrModels
      } else if (res.ocrModel) {
        ocrModels.value = [res.ocrModel]
      } else {
        ocrModels.value = []
      }
      syncSuccess.value = true
      const count = ocrModels.value.length
      syncMsg.value = count > 0
        ? `已成功从服务端网关同步 OCR 降级候选池 (${count} 个模型): ${ocrModels.value.join(', ')}`
        : '已成功从服务端网关同步 OCR 配置: 当前未配置模型'
    }
  } catch (err: any) {
    syncSuccess.value = false
    syncMsg.value = `从服务端网关同步失败: ${err.message || err}`
  } finally {
    pulling.value = false
  }
}

async function saveOcrModel() {
  saving.value = true
  syncMsg.value = ''
  try {
    await mappingApi.setOcrModel({
      ocrModels: ocrModels.value,
      ocrModel: ocrModels.value.length > 0 ? ocrModels.value[0] : ''
    })
    syncSuccess.value = true
    const count = ocrModels.value.length
    syncMsg.value = count > 0
      ? `OCR 降级竞速模型候选池 (${count} 个模型: ${ocrModels.value.join(', ')}) 已持久化保存并热同步至服务端网关！`
      : 'OCR 降级模型配置已清空，网关图片 OCR 预处理已关闭'
  } catch (err: any) {
    syncSuccess.value = false
    syncMsg.value = err.message || '保存失败'
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  fetchOcrModel()
  fetchAvailableModels()
})
</script>

