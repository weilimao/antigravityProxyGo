<template>
  <div class="relative w-full">
    <input
      :id="inputId"
      :type="show ? 'text' : 'password'"
      :placeholder="placeholder"
      :data-i18n-placeholder="dataI18nPlaceholder"
      :class="inputClass"
    />
    <button
      type="button"
      :aria-label="buttonLabel"
      :title="buttonTitle"
      class="absolute right-2 top-1/2 -translate-y-1/2 flex items-center justify-center w-6 h-6 text-outline hover:text-on-surface dark:hover:text-white transition-colors cursor-pointer"
      @click="handleToggle"
    >
      <span class="material-symbols-outlined text-[18px] block leading-none">{{ show ? 'visibility_off' : 'visibility' }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { revealFromBackend, nvidiaRevealAccountId, otherRevealAccountId } from '../../shared/revealKeyState';

const props = defineProps<{
  inputId: string;
  placeholder?: string;
  dataI18nPlaceholder?: string;
  inputClass?: string;
  // 明文查看模式开关:
  // - revealProvider: "nvidia" | "other"(对应后端 account:reveal-key 的 provider 校验);
  // - 添加态不传 provider → 眼睛退化为纯视觉切换(type 在 password/text 间切换),绝不发起 IPC。
  revealProvider?: string;
}>();

const show = ref(false);
// 记录是否已向后端取回过明文(已取回后再次点击仅做显示/隐藏切换,不再重复 IPC)。
let revealed = false;

// 编辑账号 id 变化时(切账号/关闭重开/复位为添加态),需要重新取明文;
// 旧的 revealed 标记与输入的明文对不上号时必须清零,避免眼睛不再发起 IPC。
watch([nvidiaRevealAccountId, otherRevealAccountId], () => {
  revealed = false;
  show.value = false;
});

// 取当前模态框实际应查看的账号 id:编辑态由 openEdit* 透传,添加态为 null。
function currentRevealAccountId(): string | null {
  if (props.revealProvider === 'nvidia') return nvidiaRevealAccountId.value;
  if (props.revealProvider === 'other') return otherRevealAccountId.value;
  return null;
}

const revealMode = computed(() => {
  // 明文查看模式判定:有 provider 且有绑定账号 id。
  return !!props.revealProvider && !!currentRevealAccountId();
});

const buttonLabel = computed(() => {
  if (revealMode.value && !revealed) return '显示明文 Key';
  return show.value ? '隐藏' : '显示';
});
const buttonTitle = computed(() => {
  if (revealMode.value && !revealed) return '点击显示完整 Key';
  return show.value ? '隐藏' : '显示';
});

async function handleToggle() {
  const provider = props.revealProvider;
  const accountId = currentRevealAccountId();
  // 明文查看模式且已有绑定 key:先向后端取明文回填输入框,再切明文显示。
  if (provider && accountId) {
    const inputEl = document.getElementById(props.inputId) as HTMLInputElement | null;
    if (!inputEl) return;
    let ok = false;
    if (!revealed) {
      ok = await revealFromBackend('account:reveal-key', provider, accountId, inputEl);
      if (ok) revealed = true;
    } else {
      ok = true; // 已取回过明文,直接切换显隐
    }
    if (ok) show.value = !show.value;
    // 取明文失败:提示由全局 warnHandler 代打(编辑弹窗的 error 红条),不切换到明文。
    return;
  }
  // 纯视觉切换(添加态,未绑定任何账号)。
  show.value = !show.value;
}
</script>