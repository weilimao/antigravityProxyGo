<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="triggerTestModal">
    <div class="bg-white dark:bg-[#1e2538] w-[720px] max-w-[95vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col max-h-[90vh] transform scale-95 transition-transform duration-200" id="triggerTestModalContainer">
        <!-- Modal 头部 -->
        <div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
            <div class="flex items-center gap-2">
                <span class="material-symbols-outlined text-primary text-[20px]">bolt</span>
                <span class="text-sm font-bold text-on-surface dark:text-white">触发配额刷新测试</span>
                <span class="text-[11px] font-medium text-primary dark:text-primary-fixed-dim bg-primary/10 px-1.5 py-0.5 rounded-md" id="triggerModalAccountCount">已选择 0 个账号</span>
            </div>
            <button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="btnTriggerModalClose">
                <span class="material-symbols-outlined text-[18px]">close</span>
            </button>
        </div>

        <!-- Modal 主体 -->
        <div class="p-6 overflow-y-auto flex-grow space-y-4 max-h-[70vh]">
            <!-- 配置表单区域 -->
            <div id="triggerConfigSection" class="space-y-4">
                <div>
                    <label class="block text-[12px] font-bold text-outline dark:text-outline-variant mb-1.5">1. 测试触发词 (Prompt)</label>
                    <input type="text" id="inputTriggerPrompt" value="ok" class="w-full px-3 py-1.5 bg-slate-50 dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" placeholder="生成内容请求所使用的 Prompt，默认为 ok" />
                </div>

                <div>
                    <div class="flex items-center justify-between mb-1.5">
                        <label class="block text-[12px] font-bold text-outline dark:text-outline-variant">2. 选择测试模型</label>
                        <div class="flex items-center gap-2 text-[11px]">
                            <button type="button" id="btnTriggerModalSelectAll" class="text-primary dark:text-primary-fixed-dim hover:underline font-medium cursor-pointer">全选</button>
                            <span class="text-outline/30">|</span>
                            <button type="button" id="btnTriggerModalClearAll" class="text-outline hover:text-primary hover:underline font-medium cursor-pointer">清空</button>
                        </div>
                    </div>
                    
                    <!-- 模型选择网格 -->
                    <div class="grid grid-cols-3 gap-4 p-3 bg-slate-50/50 dark:bg-slate-900/20 border border-outline-variant/30 rounded-xl max-h-48 overflow-y-auto text-[11.5px] text-on-surface dark:text-slate-200">
                        <!-- Gemini Models -->
                        <div class="space-y-1">
                            <div class="font-bold text-[10.5px] text-outline uppercase tracking-wider pb-1 border-b border-outline-variant/10">Gemini Models</div>
                            <div class="space-y-0.5 mt-1">
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3.5-flash" checked class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-3.5-flash</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3.5-flash-low" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-3.5-flash-low</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3.5-flash-extra-low" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">3.5-flash-extra-low</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3.1-flash-lite" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-3.1-flash-lite</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3.1-pro-low" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-3.1-pro-low</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3.1-pro-preview" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">3.1-pro-preview</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3-flash" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-3-flash</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3-flash-preview" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">3-flash-preview</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-3-flash-agent" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">3-flash-agent</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-pro-agent" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-pro-agent</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-2.5-flash" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-2.5-flash</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gemini-2.5-flash-lite" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gemini-2.5-flash-lite</span>
                                </label>
                            </div>
                        </div>

                        <!-- Claude Models -->
                        <div class="space-y-1">
                            <div class="font-bold text-[10.5px] text-outline uppercase tracking-wider pb-1 border-b border-outline-variant/10">Claude Models</div>
                            <div class="space-y-0.5 mt-1">
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="claude-sonnet-4-6" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">claude-sonnet-4-6</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="claude-opus-4-6-thinking" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">opus-4-6-thinking</span>
                                </label>
                            </div>
                        </div>

                        <!-- Others -->
                        <div class="space-y-1">
                            <div class="font-bold text-[10.5px] text-outline uppercase tracking-wider pb-1 border-b border-outline-variant/10">Others</div>
                            <div class="space-y-0.5 mt-1">
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="gpt-oss-120b-medium" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">gpt-oss-120b-medium</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="tab_flash_lite_preview" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">tab_flash_lite_preview</span>
                                </label>
                                <label class="flex items-center gap-1.5 hover:bg-outline-variant/10 p-0.5 rounded cursor-pointer transition-colors select-none">
                                    <input type="checkbox" name="triggerModel" value="tab_jump_flash_lite_preview" class="trigger-model-checkbox w-3.5 h-3.5 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
                                    <span class="truncate">jump_flash_lite_prev</span>
                                </label>
                            </div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- 实时日志区域 -->
            <div>
                <label class="block text-[12px] font-bold text-outline dark:text-outline-variant mb-1.5">进程实时日志</label>
                <div id="triggerLogsArea" class="bg-slate-900 dark:bg-slate-950 text-slate-300 font-mono text-[11px] p-3.5 rounded-xl h-44 overflow-y-auto border border-outline-variant/15 leading-relaxed selection:bg-primary/30">
                    <div class="text-outline dark:text-outline-variant italic">等待配置并开始触发...</div>
                </div>
            </div>

            <!-- 汇总表格区域 -->
            <div id="triggerResultsContainer" class="hidden animate-fadeIn space-y-2">
                <label class="block text-[12px] font-bold text-outline dark:text-outline-variant">触发结果汇总</label>
                <div class="overflow-x-auto rounded-xl border border-outline-variant/30 max-h-56 overflow-y-auto bg-slate-50/20 dark:bg-slate-950/10">
                    <table class="w-full text-left border-collapse table-fixed text-[11.5px]">
                        <thead>
                            <tr class="bg-slate-50 dark:bg-[#1a1f30] text-outline border-b border-outline-variant/30 sticky top-0 z-10">
                                <th class="p-2.5 font-bold w-[35%]">账号</th>
                                <th class="p-2.5 font-bold w-[25%]">所试模型</th>
                                <th class="p-2.5 font-bold text-center w-[15%]">状态</th>
                                <th class="p-2.5 font-bold w-[25%]">详情/错误</th>
                            </tr>
                        </thead>
                        <tbody id="triggerResultsTableBody" class="divide-y divide-outline-variant/10 text-on-surface dark:text-slate-200">
                            <!-- JS 动态填充 -->
                        </tbody>
                    </table>
                </div>
            </div>
        </div>

        <!-- Modal 底部 -->
        <div class="px-6 py-4 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-end items-center gap-3 rounded-b-2xl">
            <button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer select-none" id="btnTriggerModalCancel">取消</button>
            <button class="flex items-center gap-1.5 px-4 py-1.5 bg-primary text-white hover:bg-primary/90 disabled:opacity-60 disabled:cursor-not-allowed rounded-lg text-[12px] font-bold transition-all shadow-sm cursor-pointer select-none" id="btnStartTriggerTest">
                <span class="material-symbols-outlined text-[15px]" id="btnStartTriggerIcon">play_arrow</span>
                <span>开始触发</span>
            </button>
        </div>
    </div>
</div>
</template>

<script setup lang="ts">
// Handles statically through Accounts controller to keep code unified.
</script>

<style scoped>
@keyframes fadeIn {
    from { opacity: 0; transform: translateY(4px); }
    to { opacity: 1; transform: translateY(0); }
}
.animate-fadeIn {
    animation: fadeIn 0.25s ease-out forwards;
}
</style>
