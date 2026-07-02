<template>
<div>
  <!-- 一级弹窗：管理 API Keys -->
  <div class="hidden fixed inset-0 bg-black/50 z-[9999] flex items-center justify-center" id="remoteKeysModal">
    <div class="bg-white dark:bg-[#1e2538] rounded-xl shadow-2xl w-[640px] p-6 border border-outline-variant/20 flex flex-col max-h-[80vh]">
      <div class="flex items-center gap-2 mb-5 flex-shrink-0">
        <span class="material-symbols-outlined text-[22px] text-primary">key</span>
        <h3 class="text-[16px] font-bold text-on-surface dark:text-white" data-i18n="manageApiKeys">管理 API Keys</h3>
        <div class="flex-grow"></div>
        <button id="btnRemoteKeysClose" class="text-outline hover:text-on-surface dark:hover:text-white transition-colors">
          <span class="material-symbols-outlined text-[20px]">close</span>
        </button>
      </div>
      <div class="flex gap-2 mb-4 flex-shrink-0">
        <input class="flex-1 px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" id="remoteNewKeyName" placeholder="新 Key 备注名称..." data-i18n-placeholder="newKeyPlaceholder" type="text">
        <button class="px-4 py-2 text-[12px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors flex items-center gap-1 shadow-sm whitespace-nowrap" id="btnRemoteCreateKey">
          <span class="material-symbols-outlined text-[16px]">add</span>
          <span data-i18n="btnCreateKey">+ 创建 Key</span>
        </button>
      </div>
      <div class="flex-1 overflow-y-auto pr-1 min-h-[200px]">
        <table class="w-full text-left text-[12px]">
          <thead class="sticky top-0 bg-white dark:bg-[#1e2538] z-10">
            <tr class="border-b border-outline-variant/25 text-outline/80">
              <th class="py-2.5 font-bold pl-2 w-[110px]" data-i18n="colName">名称</th>
              <th class="py-2.5 font-bold w-[160px]" data-i18n="colApiKey">API Key (脱敏展示)</th>
              <th class="py-2.5 font-bold w-[130px]" data-i18n="colGeminiUsage">Gemini (已用/限额)</th>
              <th class="py-2.5 font-bold w-[130px]" data-i18n="colClaudeUsage">Claude (已用/限额)</th>
              <th class="py-2.5 font-bold text-center w-[90px]" data-i18n="colAction">操作</th>
            </tr>
          </thead>
          <tbody id="remoteKeysTableBody">
            <!-- 动态渲染 -->
          </tbody>
        </table>
      </div>
    </div>
  </div>

  <!-- 二级弹窗：设置限额 -->
  <div class="hidden fixed inset-0 bg-black/50 z-[99999] flex items-center justify-center" id="remoteKeyQuotaModal">
    <div class="bg-white dark:bg-[#1e2538] rounded-xl shadow-2xl w-[360px] p-5 border border-outline-variant/20 flex flex-col animate-in fade-in zoom-in-95 duration-150">
      <div class="flex items-center gap-2 mb-4 justify-between flex-shrink-0">
        <div class="flex items-center gap-1.5">
          <span class="material-symbols-outlined text-[20px] text-primary">settings</span>
          <h3 class="text-[14px] font-bold text-on-surface dark:text-white" id="remoteQuotaEditTitle" data-i18n="modifyKeyQuota">修改 Key 限额</h3>
        </div>
        <button id="btnRemoteQuotaClose" class="text-outline hover:text-on-surface dark:hover:text-white transition-colors" onclick="document.getElementById('remoteKeyQuotaModal').classList.add('hidden')">
          <span class="material-symbols-outlined text-[18px]">close</span>
        </button>
      </div>

      <input type="hidden" id="remoteQuotaEditId">

      <div class="mb-4">
        <label class="block text-[11px] font-medium text-outline mb-1" data-i18n="geminiQuotaLabel">Gemini 限额 Token 数</label>
        <input class="w-full px-3 py-2 text-[12px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary/30" id="remoteQuotaEditGemini" placeholder="例如: 500k, 1m 或输入 0/留空不限制" data-i18n-placeholder="quotaPlaceholder" type="text">
      </div>

      <div class="mb-4">
        <label class="block text-[11px] font-medium text-outline mb-1" data-i18n="claudeQuotaLabel">Claude 限额 Token 数</label>
        <input class="w-full px-3 py-2 text-[12px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary/30" id="remoteQuotaEditClaude" placeholder="例如: 500k, 1m 或输入 0/留空不限制" data-i18n-placeholder="quotaPlaceholder" type="text">
      </div>

      <div class="flex gap-2 justify-end mt-2 flex-shrink-0">
        <button class="px-4 py-2 text-[11px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors flex items-center gap-1 shadow-sm" id="btnRemoteQuotaSave">
          <span data-i18n="btnSave">保存</span>
        </button>
        <button class="px-4 py-2 text-[11px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors" id="btnRemoteQuotaCancel" onclick="document.getElementById('remoteKeyQuotaModal').classList.add('hidden')">
          <span data-i18n="btnCancel">取消</span>
        </button>
      </div>
    </div>
  </div>
</div>
</template>

<script setup lang="ts">
</script>
