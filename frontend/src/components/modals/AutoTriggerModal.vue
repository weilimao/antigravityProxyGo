<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="autoTriggerModal">
    <div class="bg-white dark:bg-[#1e2538] w-[800px] max-w-[95vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col max-h-[85vh] transform scale-95 transition-transform duration-200" id="autoTriggerModalContainer">
        <!-- Modal 头部 -->
        <div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
            <div class="flex items-center gap-2">
                <span class="material-symbols-outlined text-primary text-[20px]">timer</span>
                <span class="text-sm font-bold text-on-surface dark:text-white" id="autoTriggerModalTitle">自动化触发任务管理</span>
            </div>
            <button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="btnAutoTriggerModalClose">
                <span class="material-symbols-outlined text-[18px]">close</span>
            </button>
        </div>

        <!-- 任务列表面板 -->
        <div id="panelTaskList" class="p-6 overflow-y-auto flex-grow flex flex-col max-h-[65vh]">
            <div class="flex justify-between items-center mb-4">
                <div class="text-[11px] text-outline leading-relaxed max-w-[70%]">
                    配置自动刷新机制，在定时到达或配额刷新完成后执行指定的测试回复，从而保持账号冷静期和额度可用性。
                </div>
                <button class="flex items-center gap-1 px-3.5 py-1.5 bg-primary text-white hover:bg-primary/90 rounded-lg text-[12px] font-bold transition-all cursor-pointer select-none" id="btnCreateNewTask">
                    <span class="material-symbols-outlined text-[15px]">add</span>
                    <span>新建任务包</span>
                </button>
            </div>

            <!-- 表格列表 -->
            <div class="overflow-x-auto rounded-xl border border-outline-variant/30 bg-slate-50/30 dark:bg-slate-950/20 max-h-[380px] overflow-y-auto">
                <table class="w-full text-left border-collapse table-fixed text-[11.5px]">
                    <thead>
                        <tr class="bg-slate-50 dark:bg-[#1a1f30] text-outline border-b border-outline-variant/30 sticky top-0 z-10">
                            <th class="p-3 font-bold w-[25%]">任务名称</th>
                            <th class="p-3 font-bold w-[20%]">触发方式</th>
                            <th class="p-3 font-bold w-[12%]">账号数</th>
                            <th class="p-3 font-bold w-[12%]">模型数</th>
                            <th class="p-3 font-bold text-center w-[15%]">启用状态</th>
                            <th class="p-3 font-bold text-center w-[16%]">操作</th>
                        </tr>
                    </thead>
                    <tbody id="autoTriggerTasksTableBody" class="divide-y divide-outline-variant/10 text-on-surface dark:text-slate-200">
                        <!-- 动态渲染 -->
                        <tr>
                            <td class="p-8 text-center text-outline dark:text-outline-variant italic" colspan="6">
                                ⏳ 正在加载定时任务列表...
                            </td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>

        <!-- 任务编辑面板 -->
        <div id="panelTaskEdit" class="hidden p-6 overflow-y-auto flex-grow space-y-4 max-h-[65vh]">
            <input type="hidden" id="editTaskId" value="" />
            
            <div class="grid grid-cols-2 gap-4">
                <div>
                    <label class="block class text-[11px] font-bold text-outline dark:text-outline-variant mb-1">任务名称</label>
                    <input type="text" id="editTaskName" class="w-full px-3 py-1.5 bg-slate-50 dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" placeholder="例如：Gemini定时刷新任务" />
                </div>
                <div>
                    <label class="block text-[11px] font-bold text-outline dark:text-outline-variant mb-1">测试回复触发词 (Prompt)</label>
                    <input type="text" id="editTaskPrompt" class="w-full px-3 py-1.5 bg-slate-50 dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" placeholder="默认使用 ok" value="ok" />
                </div>
            </div>

            <div class="grid grid-cols-2 gap-4 border-t border-outline-variant/20 pt-3">
                <div>
                    <label class="block text-[11px] font-bold text-outline dark:text-outline-variant mb-1">触发方式选择</label>
                    <select id="editTaskTriggerType" class="w-full px-3 py-1.5 bg-slate-50 dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
                        <option value="timer">定时触发 (Timer)</option>
                        <option value="quota_refreshed">配额刷新后触发 (Quota Refreshed)</option>
                    </select>
                </div>
                <div id="containerTaskInterval">
                    <label class="block text-[11px] font-bold text-outline dark:text-outline-variant mb-1">触发时间间隔 (分钟)</label>
                    <input type="number" id="editTaskInterval" class="w-full px-3 py-1.5 bg-slate-50 dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" value="60" min="5" />
                </div>
            </div>

            <!-- 选择账号 -->
            <div class="border-t border-outline-variant/20 pt-3">
                <div class="flex items-center justify-between mb-1.5">
                    <label class="block text-[11px] font-bold text-outline dark:text-outline-variant">选择关联账号</label>
                    <div class="flex items-center gap-2 text-[10px]">
                        <button type="button" id="btnEditSelectAllAccounts" class="text-primary dark:text-primary-fixed-dim hover:underline font-medium cursor-pointer">全选</button>
                        <span class="text-outline/30">|</span>
                        <button type="button" id="btnEditClearAllAccounts" class="text-outline hover:text-primary hover:underline font-medium cursor-pointer">清空</button>
                    </div>
                </div>
                <div id="editAccountsGrid" class="grid grid-cols-2 gap-2 p-3 bg-slate-50/50 dark:bg-slate-900/20 border border-outline-variant/30 rounded-xl max-h-36 overflow-y-auto text-[11px] text-on-surface dark:text-slate-200">
                    <!-- 动态生成当前系统中的所有账号列表复选框 -->
                </div>
            </div>

            <!-- 选择模型 -->
            <div class="border-t border-outline-variant/20 pt-3">
                <div class="flex items-center justify-between mb-1.5">
                    <label class="block text-[11px] font-bold text-outline dark:text-outline-variant">选择触发测试模型</label>
                    <div class="flex items-center gap-2 text-[10px]">
                        <button type="button" id="btnEditSelectAllModels" class="text-primary dark:text-primary-fixed-dim hover:underline font-medium cursor-pointer">全选</button>
                        <span class="text-outline/30">|</span>
                        <button type="button" id="btnEditClearAllModels" class="text-outline hover:text-primary hover:underline font-medium cursor-pointer">清空</button>
                    </div>
                </div>
                <!-- 模型选择网格 -->
                <div class="grid grid-cols-3 gap-3 p-3 bg-slate-50/50 dark:bg-slate-900/20 border border-outline-variant/30 rounded-xl max-h-36 overflow-y-auto text-[11px] text-on-surface dark:text-slate-200">
                    <!-- Gemini -->
                    <div class="space-y-1">
                        <div class="font-bold text-[10px] text-outline uppercase tracking-wider pb-0.5 border-b border-outline-variant/10">Gemini Models</div>
                        <div class="space-y-0.5 mt-1 animate-fadeIn" id="editModelsGemini">
                        </div>
                    </div>
                    <!-- Claude -->
                    <div class="space-y-1">
                        <div class="font-bold text-[10px] text-outline uppercase tracking-wider pb-0.5 border-b border-outline-variant/10">Claude Models</div>
                        <div class="space-y-0.5 mt-1 animate-fadeIn" id="editModelsClaude">
                        </div>
                    </div>
                    <!-- Others -->
                    <div class="space-y-1">
                        <div class="font-bold text-[10px] text-outline uppercase tracking-wider pb-0.5 border-b border-outline-variant/10">Others</div>
                        <div class="space-y-0.5 mt-1 animate-fadeIn" id="editModelsOthers">
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <!-- Modal 底部 -->
        <div class="px-6 py-4 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-between items-center rounded-b-2xl">
            <!-- 列表面板的底部 -->
            <div id="footerTaskList" class="flex justify-end w-full">
                <button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer select-none" id="btnAutoTriggerModalCloseSecondary">关闭</button>
            </div>
            <!-- 编辑面板的底部 -->
            <div id="footerTaskEdit" class="hidden flex justify-between w-full">
                <button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer select-none" id="btnCancelEditTask">返回列表</button>
                <button class="px-4 py-1.5 bg-primary text-white hover:bg-primary/90 rounded-lg text-[12px] font-bold transition-all shadow-sm cursor-pointer select-none" id="btnSaveTask">保存任务</button>
            </div>
        </div>
    </div>
</div>
</template>

<script setup lang="ts">
// Logic handled inside accountsController.ts to maintain structure
</script>

<style scoped>
/* Switch toggle styles in Tailwind */
.switch {
  position: relative;
  display: inline-block;
  width: 32px;
  height: 18px;
}
.switch input { 
  opacity: 0;
  width: 0;
  height: 0;
}
.slider {
  position: absolute;
  cursor: pointer;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: #cbd5e1;
  transition: .2s;
  border-radius: 34px;
}
.slider:before {
  position: absolute;
  content: "";
  height: 14px;
  width: 14px;
  left: 2px;
  bottom: 2px;
  background-color: white;
  transition: .2s;
  border-radius: 50%;
}
input:checked + .slider {
  background-color: #6366f1; /* Primary color */
}
input:checked + .slider:before {
  transform: translateX(14px);
}
</style>
