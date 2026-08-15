<template>
  <div class="code-editor-container flex flex-col rounded-xl overflow-hidden border border-outline-variant/30 bg-slate-900 text-slate-100 shadow-md transition-all">
    <!-- 编辑器顶部工具栏 (macOS 终端/IDE 风格) -->
    <div class="flex items-center justify-between px-4 py-2.5 bg-slate-950/80 border-b border-slate-800 select-none backdrop-blur-sm">
      <!-- 左侧：macOS 视窗小圆点 + 文件名与徽章 -->
      <div class="flex items-center gap-3">
        <div class="flex items-center gap-1.5">
          <span class="w-3 h-3 rounded-full bg-[#ff5f56] border border-[#e0443e]/40 inline-block shadow-sm"></span>
          <span class="w-3 h-3 rounded-full bg-[#ffbd2e] border border-[#dea123]/40 inline-block shadow-sm"></span>
          <span class="w-3 h-3 rounded-full bg-[#27c93f] border border-[#1aab29]/40 inline-block shadow-sm"></span>
        </div>
        <div class="h-3.5 w-px bg-slate-700/60 mx-0.5"></div>
        <div class="flex items-center gap-2">
          <span class="material-symbols-outlined text-[15px] text-amber-400">javascript</span>
          <span class="text-[12px] font-mono font-medium text-slate-200">{{ fileName }}</span>
          <span class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-slate-800 text-slate-400 border border-slate-700/50 uppercase">{{ language }}</span>
        </div>
      </div>

      <!-- 右侧：统计信息 + 模式切换 + 重置 + 一键复制按钮 -->
      <div class="flex items-center gap-2">
        <span class="text-[11px] font-mono text-slate-400 hidden sm:inline-block">
          {{ lineCount }} 行 · {{ codeSize }}
        </span>

        <!-- 展开/收起切换按钮 -->
        <button
          type="button"
          @click="toggleExpand"
          class="px-2.5 py-1 rounded-md text-[11px] font-medium flex items-center gap-1 transition-all cursor-pointer border bg-slate-800 text-slate-300 border-slate-700 hover:bg-slate-700 hover:text-white"
          :title="isExpanded ? '收起代码视图 (恢复限制高度)' : '展开显示全部代码 (消除内部纵向滚动条)'"
        >
          <span class="material-symbols-outlined text-[14px]">{{ isExpanded ? 'unfold_less' : 'unfold_more' }}</span>
          <span>{{ isExpanded ? '收起代码' : '展开代码' }}</span>
        </button>

        <!-- 模式切换：查看/编辑 -->
        <button
          type="button"
          @click="toggleEditMode"
          class="px-2.5 py-1 rounded-md text-[11px] font-medium flex items-center gap-1 transition-all cursor-pointer border"
          :class="isEditing 
            ? 'bg-amber-500/20 text-amber-300 border-amber-500/40 hover:bg-amber-500/30' 
            : 'bg-slate-800 text-slate-300 border-slate-700 hover:bg-slate-700 hover:text-white'"
          :title="isEditing ? '切换为语法高亮预览模式' : '切换为原位代码编辑模式'"
        >
          <span class="material-symbols-outlined text-[14px]">{{ isEditing ? 'visibility' : 'edit' }}</span>
          <span>{{ isEditing ? '预览高亮' : '编辑代码' }}</span>
        </button>

        <!-- 重置默认按钮 (仅当修改过时展示) -->
        <button
          v-if="isModified"
          type="button"
          @click="resetToDefault"
          class="px-2 py-1 rounded-md bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-white border border-slate-700 text-[11px] font-medium flex items-center gap-1 transition-all cursor-pointer"
          title="恢复为默认初始代码"
        >
          <span class="material-symbols-outlined text-[14px]">restart_alt</span>
          <span>恢复默认</span>
        </button>

        <!-- 一键复制代码按钮 -->
        <button
          type="button"
          @click="handleCopy"
          class="px-3 py-1 rounded-md text-[11.5px] font-semibold flex items-center gap-1.5 transition-all duration-200 cursor-pointer shadow-sm active:scale-95"
          :class="copied
            ? 'bg-emerald-600 text-white shadow-emerald-900/30'
            : 'bg-primary hover:bg-primary/90 text-white shadow-primary/20'"
        >
          <span class="material-symbols-outlined text-[15px] transition-transform duration-200" :class="{ 'scale-110': copied }">
            {{ copied ? 'check' : 'content_copy' }}
          </span>
          <span>{{ copied ? '已复制到剪贴板' : '一键复制代码' }}</span>
        </button>
      </div>
    </div>

    <!-- 代码主体区域：支持行号与高亮/编辑 -->
    <div
      class="relative flex font-mono text-[11.5px] leading-6 bg-[#0d1117] text-slate-100 select-text transition-all"
      :class="isExpanded ? 'max-h-none overflow-x-auto' : 'max-h-[380px] overflow-auto overscroll-contain'"
      ref="codeContainerRef"
    >
      <!-- 左侧行号栏 (不可选中) -->
      <div class="sticky left-0 flex flex-col py-3 px-3 text-right bg-[#0d1117] text-slate-500/70 border-r border-slate-800/80 select-none z-10 font-mono text-[11px] min-w-[42px]">
        <span v-for="n in lineCount" :key="n" class="leading-6">{{ n }}</span>
      </div>

      <!-- 右侧：查看模式 (高亮渲染) -->
      <div v-if="!isEditing" class="flex-1 py-3 px-4 overflow-x-auto whitespace-pre font-mono">
        <div v-html="highlightedHtml"></div>
      </div>

      <!-- 右侧：编辑模式 (Textarea 实时编辑) -->
      <div v-else class="flex-1 flex flex-col relative py-3 px-4">
        <textarea
          v-model="currentCode"
          class="w-full bg-transparent text-slate-100 font-mono text-[11.5px] leading-6 resize-y focus:outline-none border-none p-0 whitespace-pre overflow-x-auto"
          :class="isExpanded ? 'min-h-[500px] h-auto' : 'min-h-[300px] h-full'"
          spellcheck="false"
          placeholder="在此编辑 Worker 脚本..."
        ></textarea>
      </div>
    </div>

    <!-- 编辑器底部状态条 -->
    <div class="flex items-center justify-between px-4 py-1.5 bg-slate-950/90 border-t border-slate-800 text-[10.5px] text-slate-400 font-mono select-none">
      <div class="flex items-center gap-3">
        <span class="flex items-center gap-1">
          <span class="w-2 h-2 rounded-full" :class="isEditing ? 'bg-amber-400' : 'bg-emerald-400'"></span>
          {{ isEditing ? '编辑中 (实时同步)' : '只读预览 (已语法着色)' }}
        </span>
        <span v-if="isModified" class="text-amber-400">● 已自定义修改</span>
        <button
          type="button"
          @click="toggleExpand"
          class="text-slate-400 hover:text-slate-200 flex items-center gap-1 cursor-pointer transition-colors"
          :title="isExpanded ? '收起为紧凑窗口' : '展开查看全部代码'"
        >
          <span class="material-symbols-outlined text-[13px]">{{ isExpanded ? 'unfold_less' : 'unfold_more' }}</span>
          <span>{{ isExpanded ? '收起视图' : `展开全部 (${lineCount} 行)` }}</span>
        </button>
      </div>
      <div class="flex items-center gap-4">
        <span>UTF-8</span>
        <span>JavaScript (ES Modules)</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    defaultCode?: string;
    fileName?: string;
    language?: string;
    initialExpanded?: boolean;
  }>(),
  {
    modelValue: '',
    defaultCode: '',
    fileName: 'cloudflare-worker.js',
    language: 'JavaScript',
    initialExpanded: false,
  }
);

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void;
  (e: 'copy', value: string): void;
}>();

const currentCode = ref(props.modelValue || props.defaultCode);
const isEditing = ref(false);
const isExpanded = ref(props.initialExpanded);
const copied = ref(false);
const codeContainerRef = ref<HTMLElement | null>(null);

// 切换展开/收起
const toggleExpand = () => {
  isExpanded.value = !isExpanded.value;
};

// 监听外部 modelValue 变化
watch(
  () => props.modelValue,
  (newVal) => {
    if (newVal !== undefined && newVal !== currentCode.value) {
      currentCode.value = newVal;
    }
  }
);

// 监听内部 currentCode 变化向外同步
watch(currentCode, (newVal) => {
  emit('update:modelValue', newVal);
});

// 计算行数
const lineCount = computed(() => {
  if (!currentCode.value) return 1;
  return currentCode.value.split('\n').length;
});

// 计算代码体积大小
const codeSize = computed(() => {
  const bytes = new Blob([currentCode.value]).size;
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
});

// 是否已修改
const isModified = computed(() => {
  const base = props.defaultCode || props.modelValue;
  return Boolean(base && currentCode.value !== base);
});

// 切换编辑模式
const toggleEditMode = () => {
  isEditing.value = !isEditing.value;
};

// 恢复初始默认代码
const resetToDefault = () => {
  const target = props.defaultCode || props.modelValue;
  if (target) {
    currentCode.value = target;
  }
};

// HTML 特殊字符转义防 XSS
const escapeHtml = (text: string): string => {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
};

// 高性能 JavaScript 纯分词语法着色器
const highlightedHtml = computed(() => {
  const code = currentCode.value || '';
  const lines = code.split('\n');

  return lines
    .map((line) => {
      // 1. 单行注释处理
      const commentIdx = line.indexOf('//');
      if (commentIdx !== -1) {
        // 检查注释是否在字符串内部 (简单启发式匹配)
        const before = line.slice(0, commentIdx);
        const singleQuotes = (before.match(/'/g) || []).length;
        const doubleQuotes = (before.match(/"/g) || []).length;
        const backticks = (before.match(/`/g) || []).length;
        if (singleQuotes % 2 === 0 && doubleQuotes % 2 === 0 && backticks % 2 === 0) {
          const codePart = highlightCodeTokens(before);
          const commentPart = `<span class="text-slate-500 italic">${escapeHtml(line.slice(commentIdx))}</span>`;
          return codePart + commentPart;
        }
      }
      return highlightCodeTokens(line);
    })
    .join('\n');
});

// 代码行内 Token 高亮
function highlightCodeTokens(lineStr: string): string {
  if (!lineStr) return '';

  // 正则匹配 JS Token：字符串、数字、关键字、内置类、方法调用、属性名
  const tokenRegex =
    /(`(?:\\.|[^`])*`|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')|(\b\d+(?:\.\d+)?\b)|(\b(?:export|default|async|await|function|const|let|var|if|else|return|for|while|try|catch|new|break|continue|switch|case|typeof|instanceof|void|delete|null|undefined|true|false)\b)|(\b(?:Response|Request|URL|Headers|JSON|Promise|ArrayBuffer|Uint8Array|Math|Date|fetch|setTimeout|clearTimeout|console)\b)|(\b[a-zA-Z_$][a-zA-Z0-9_$]*(?=\s*\())|(\b[a-zA-Z_$][a-zA-Z0-9_$]*(?=\s*:))|([{}()[\],;.:=+\-*/%&|^!<>?~]+)|([a-zA-Z_$][a-zA-Z0-9_$]*)/g;

  return lineStr.replace(
    tokenRegex,
    (
      match,
      str,
      num,
      kw,
      builtin,
      fnCall,
      propKey,
      punct,
      ident
    ) => {
      if (str) {
        // 字符串 (绿色)
        return `<span class="text-emerald-400">${escapeHtml(str)}</span>`;
      }
      if (num) {
        // 数字 (橙色)
        return `<span class="text-amber-400">${escapeHtml(num)}</span>`;
      }
      if (kw) {
        // 关键字 / 布尔 / null (紫红色/洋红)
        if (kw === 'true' || kw === 'false' || kw === 'null' || kw === 'undefined') {
          return `<span class="text-amber-400 font-semibold">${escapeHtml(kw)}</span>`;
        }
        return `<span class="text-purple-400 font-semibold">${escapeHtml(kw)}</span>`;
      }
      if (builtin) {
        // 内置类 / 对象 / 全局 API (亮青色)
        return `<span class="text-sky-300 font-medium">${escapeHtml(builtin)}</span>`;
      }
      if (fnCall) {
        // 函数调用 (浅黄色)
        return `<span class="text-yellow-200">${escapeHtml(fnCall)}</span>`;
      }
      if (propKey) {
        // 对象属性名 (亮白/浅蓝)
        return `<span class="text-indigo-300">${escapeHtml(propKey)}</span>`;
      }
      if (punct) {
        // 标点操作符 (中灰)
        return `<span class="text-slate-400">${escapeHtml(punct)}</span>`;
      }
      if (ident) {
        // 普通标识符 (白)
        return `<span class="text-slate-100">${escapeHtml(ident)}</span>`;
      }
      return escapeHtml(match);
    }
  );
}

// 复制到剪贴板实现 (具备现代 API 与 DOM 降级兜底)
const handleCopy = async () => {
  const codeToCopy = currentCode.value;
  let success = false;

  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(codeToCopy);
      success = true;
    }
  } catch (err) {
    // 降级使用 textarea execCommand
    success = false;
  }

  if (!success) {
    try {
      const textarea = document.createElement('textarea');
      textarea.value = codeToCopy;
      textarea.style.position = 'fixed';
      textarea.style.left = '-999999px';
      textarea.style.top = '-999999px';
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      const res = document.execCommand('copy');
      document.body.removeChild(textarea);
      success = Boolean(res);
    } catch (e) {
      success = false;
    }
  }

  if (success) {
    copied.value = true;
    emit('copy', codeToCopy);
    setTimeout(() => {
      copied.value = false;
    }, 2000);
  }
};
</script>

<style scoped>
/* 滚动条美化 */
.code-editor-container ::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}
.code-editor-container ::-webkit-scrollbar-track {
  background: #0d1117;
}
.code-editor-container ::-webkit-scrollbar-thumb {
  background: #30363d;
  border-radius: 4px;
}
.code-editor-container ::-webkit-scrollbar-thumb:hover {
  background: #484f58;
}

/* 锁定内部滚动事件，防止滚轮穿透到外层主页面引发跳动 */
.overscroll-contain {
  overscroll-behavior: contain;
}
</style>
