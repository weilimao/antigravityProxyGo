<template>
  <div
    :id="id"
    :class="[
      hideType === 'hidden' ? 'hidden fixed inset-0 modal-backdrop-animate' : 'fixed inset-0 opacity-0 pointer-events-none transition-opacity duration-200',
      'bg-slate-950/75 flex items-center justify-center',
      zIndex || 'z-50'
    ]"
  >
    <div
      :id="containerId"
      :class="[
        'bg-white dark:bg-[#1e2538] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col transform scale-95 transition-transform duration-200',
        maxWidth || 'w-[600px] max-w-[90vw]',
        maxHeight || 'max-h-[85vh]',
        containerClass
      ]"
    >
      <!-- Modal Header -->
      <div
        v-if="showHeader"
        :class="[
          'px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center rounded-t-2xl',
          headerBg || 'bg-slate-50/50 dark:bg-white/5'
        ]"
      >
        <div class="flex items-center gap-2 min-w-0">
          <slot name="header-icon">
            <span v-if="icon" :class="['material-symbols-outlined shrink-0', iconClass || 'text-primary text-[20px]']">{{ icon }}</span>
          </slot>
          <slot name="header-title">
            <span
              v-if="title || titleI18n"
              class="text-sm font-bold text-on-surface dark:text-white truncate"
              :data-i18n="titleI18n"
            >
              {{ title }}
            </span>
          </slot>
        </div>

        <div class="flex items-center gap-2 shrink-0">
          <slot name="header-extra"></slot>
          <button
            v-if="showClose"
            :id="closeBtnId"
            @click="handleClose"
            class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5 cursor-pointer"
          >
            <span class="material-symbols-outlined text-[18px]">close</span>
          </button>
        </div>
      </div>

      <!-- Modal Body -->
      <div :class="['flex-grow overflow-y-auto', bodyClass || 'p-6']">
        <slot></slot>
      </div>

      <!-- Modal Footer -->
      <div
        v-if="$slots.footer"
        :class="[
          'px-6 py-4 border-t border-outline-variant/30 flex justify-end gap-3 rounded-b-2xl shrink-0',
          footerBg || 'bg-slate-50/50 dark:bg-white/5',
          footerClass
        ]"
      >
        <slot name="footer"></slot>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    id?: string;
    containerId?: string;
    closeBtnId?: string;
    title?: string;
    titleI18n?: string;
    icon?: string;
    iconClass?: string;
    maxWidth?: string;
    maxHeight?: string;
    hideType?: 'opacity' | 'hidden';
    zIndex?: string;
    showHeader?: boolean;
    showClose?: boolean;
    headerBg?: string;
    containerClass?: string;
    bodyClass?: string;
    footerBg?: string;
    footerClass?: string;
  }>(),
  {
    hideType: 'opacity',
    zIndex: 'z-50',
    showHeader: true,
    showClose: true,
  }
);

const emit = defineEmits(['close']);

function handleClose() {
  emit('close');
  if (props.id && (window as any)._relayCloseModal) {
    (window as any)._relayCloseModal(props.id);
  }
}
</script>
