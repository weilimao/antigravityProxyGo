<template>
<div class="flex flex-col gap-5 w-full" id="view-packets">
        <!-- 页面标题与操作 -->
        <div class="flex flex-col md:flex-row md:items-end justify-between gap-4">
            <div>
                <h1 class="text-2xl font-bold text-on-surface dark:text-white" data-i18n="packetsTitle">抓包分析</h1>
                <p class="text-xs text-outline dark:text-outline-variant" data-i18n="packetsDesc">抓取所有通过代理的接口请求报文和响应报文，并一键分析生成 Markdown 格式接口文档</p>
            </div>
            <div class="flex flex-wrap items-center gap-3">
                <!-- 账号选择 -->
                <div class="flex items-center gap-2">
                    <span class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="analyzeAccount">分析账号:</span>
                    <select class="bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded-md px-3 py-1.5 text-[13px] font-medium text-slate-700 dark:text-slate-200 focus:outline-none focus:border-primary" id="packetAnalyzeAccountSelect">
                        <!-- JS 动态填充 Enabled 账号 -->
                        <option value="" data-i18n="selectAnalyzeAccount">请选择分析账号...</option>
                    </select>
                </div>
                <!-- 功能按钮组 -->
                <button class="flex items-center gap-1.5 px-4 py-1.5 bg-primary text-white hover:bg-primary/95 border border-primary rounded-md text-[13px] font-medium transition-colors shadow-sm disabled:opacity-50" id="btnStartPacketAnalyze">
                    <span class="material-symbols-outlined text-[16px]">psychology</span>
                    <span data-i18n="btnAiAnalyze">一键 AI 分析接口文档</span>
                </button>
                <button class="flex items-center gap-1.5 px-4 py-1.5 bg-emerald-600 text-white hover:bg-emerald-700 border border-emerald-600 rounded-md text-[13px] font-medium transition-colors shadow-sm disabled:opacity-40" disabled id="btnDownloadPacketDoc">
                    <span class="material-symbols-outlined text-[16px]">download</span>
                    <span data-i18n="btnDownloadDoc">下载接口文档</span>
                </button>
                <button class="flex items-center gap-1.5 px-4 py-1.5 bg-blue-600 text-white hover:bg-blue-700 border border-blue-600 rounded-md text-[13px] font-medium transition-colors shadow-sm" id="btnExportPacketLog">
                    <span class="material-symbols-outlined text-[16px]">description</span>
                    <span data-i18n="btnExportLogs">导出接口日志</span>
                </button>
                <button class="flex items-center gap-1.5 px-3 py-1.5 bg-white dark:bg-[#1a1f30] border border-red-200 dark:border-red-950/30 rounded-md text-[13px] font-medium text-red-500 hover:bg-red-50 dark:hover:bg-red-950/20 transition-colors" id="btnClearPackets">
                    <span class="material-symbols-outlined text-[16px]">delete_sweep</span>
                    <span data-i18n="btnClearPackets">清空列表</span>
                </button>
            </div>
        </div>
        <!-- 双分栏：左侧列表，右侧详情 -->
        <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
            <!-- 左侧接口列表 -->
            <div class="lg:col-span-1 glass-card rounded-xl p-4 flex flex-col gap-3 h-[calc(100vh-220px)] min-h-[450px]">
                <div class="flex justify-between items-center pb-2 border-b border-outline-variant/30">
                    <span class="text-[12px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="packetListHeader">接口列表</span>
                    <span class="bg-primary/10 text-primary dark:text-primary-fixed-dim text-[11px] font-bold px-2 py-0.5 rounded-full" id="packetCountBadge">0 个接口</span>
                </div>
                <!-- 接口筛选过滤 -->
                <div class="flex flex-wrap gap-1 pb-1 border-b border-outline-variant/10 text-[10px]">
                    <button class="px-2 py-0.5 font-bold rounded bg-primary text-white transition-colors cursor-pointer" id="btnFilterPacketAll" onclick="window.setPacketFilter('ALL')" data-i18n="packetFilterAll">全部</button>
                    <button class="px-2 py-0.5 font-bold rounded bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-slate-600 dark:text-slate-300 transition-colors cursor-pointer" id="btnFilterPacketCli" onclick="window.setPacketFilter('CLI')">CLI</button>
                    <button class="px-2 py-0.5 font-bold rounded bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-slate-600 dark:text-slate-300 transition-colors cursor-pointer" id="btnFilterPacketIde" onclick="window.setPacketFilter('IDE')">IDE</button>
                    <button class="px-2 py-0.5 font-bold rounded bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-slate-600 dark:text-slate-300 transition-colors cursor-pointer" id="btnFilterPacketAgent" onclick="window.setPacketFilter('Agent')">Agent</button>
                    <button class="px-2 py-0.5 font-bold rounded bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-slate-600 dark:text-slate-300 transition-colors cursor-pointer" id="btnFilterPacketUnknown" onclick="window.setPacketFilter('UNKNOWN')" data-i18n="packetFilterUnknown">未知</button>
                </div>
                <!-- 接口列表容器 -->
                <div class="flex-grow overflow-y-auto flex flex-col gap-2 pr-1" id="packetListContainer">
                    <!-- 动态渲染的列表 -->
                    <div class="text-center py-12 text-outline text-[13px]" data-i18n="packetEmptyText">暂无已抓取的接口包</div>
                </div>
            </div>
            <!-- 右侧接口详细 data -->
            <div class="lg:col-span-2 glass-card rounded-xl p-5 flex flex-col gap-4 h-[calc(100vh-220px)] min-h-[450px]">
                <div class="flex justify-between items-center pb-2 border-b border-outline-variant/30">
                    <span class="text-[12px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="packetDetailHeader">接口报文详情</span>
                    <div class="flex gap-2">
                        <button class="hidden items-center gap-1 px-2.5 py-0.5 bg-primary/10 hover:bg-primary/20 text-primary dark:text-primary-fixed-dim rounded text-[11px] font-bold transition-colors border border-primary/20 cursor-pointer" id="btnExportSinglePacket">
                            <span class="material-symbols-outlined text-[12px]">description</span>
                            <span data-i18n="btnExportMd">导出 MD</span>
                        </button>
                        <span class="hidden font-bold px-2 py-0.5 text-[11px] rounded text-white bg-primary" id="selectedPacketMethod">POST</span>
                        <span class="hidden font-bold px-2 py-0.5 text-[11px] rounded bg-emerald-50 text-emerald-600 dark:bg-emerald-950/30 dark:text-emerald-400" id="selectedPacketStatusCode">200</span>
                    </div>
                </div>
                <!-- 未选中提示 -->
                <div class="flex-grow flex flex-col items-center justify-center text-outline text-[13px] py-24" id="packetDetailsPlaceholder" data-i18n="packetDetailTip">
                    <span class="material-symbols-outlined text-[48px] mb-2 text-outline/30">info</span>
                    点击左侧接口查看请求报文和响应报文的完整内容
                </div>
                <!-- 报文详情容器 -->
                <div class="hidden flex-grow flex flex-col gap-4 overflow-y-auto pr-1" id="packetDetailsContainer">
                    <div class="flex-shrink-0">
                        <div class="text-[13px] font-semibold text-on-surface dark:text-white mb-1" data-i18n="packetUrlLabel">接口 URL:</div>
                        <div class="bg-slate-50 dark:bg-slate-900/60 p-2.5 rounded-md font-mono text-[12px] text-slate-600 dark:text-slate-300 break-all select-all border border-outline-variant/20" id="selectedPacketUrl"></div>
                    </div>
                    <!-- 请求报文 -->
                    <div class="border border-outline-variant/30 rounded-xl overflow-hidden flex-shrink-0">
                        <div class="bg-slate-50 dark:bg-slate-900/40 px-4 py-2 border-b border-outline-variant/30 flex justify-between items-center">
                            <span class="text-[13px] font-bold text-slate-700 dark:text-slate-300" data-i18n="reqPacketLabel">📤 请求报文 (Request)</span>
                            <button class="text-[11px] text-primary hover:underline flex items-center gap-0.5" id="btnCopyReqBody">
                                <span class="material-symbols-outlined text-[12px]">content_copy</span><span data-i18n="btnCopyBody">复制 Body</span>
                            </button>
                        </div>
                        <div class="p-4 space-y-3">
                            <div>
                                <div class="text-[12px] font-semibold text-slate-500 mb-1">Headers:</div>
                                <pre class="bg-slate-950 text-slate-300 p-3 rounded-lg font-mono text-[11px] overflow-x-auto whitespace-pre-wrap max-h-[220px]" id="selectedPacketReqHeaders"></pre>
                            </div>
                            <div>
                                <div class="text-[12px] font-semibold text-slate-500 mb-1">Body:</div>
                                <pre class="bg-slate-950 text-slate-300 p-3 rounded-lg font-mono text-[11px] overflow-x-auto whitespace-pre-wrap max-h-[400px]" id="selectedPacketReqBody"></pre>
                            </div>
                        </div>
                    </div>
                    <!-- 响应报文 -->
                    <div class="border border-outline-variant/30 rounded-xl overflow-hidden flex-shrink-0">
                        <div class="bg-slate-50 dark:bg-slate-900/40 px-4 py-2 border-b border-outline-variant/30 flex justify-between items-center">
                            <span class="text-[13px] font-bold text-slate-700 dark:text-slate-300" data-i18n="respPacketLabel">📥 响应报文 (Response)</span>
                            <button class="text-[11px] text-primary hover:underline flex items-center gap-0.5" id="btnCopyResBody">
                                <span class="material-symbols-outlined text-[12px]">content_copy</span><span data-i18n="btnCopyBody">复制 Body</span>
                            </button>
                        </div>
                        <div class="p-4 space-y-3">
                            <div>
                                <div class="text-[12px] font-semibold text-slate-500 mb-1">Headers:</div>
                                <pre class="bg-slate-950 text-slate-300 p-3 rounded-lg font-mono text-[11px] overflow-x-auto whitespace-pre-wrap max-h-[220px]" id="selectedPacketResHeaders"></pre>
                            </div>
                            <div>
                                <div class="text-[12px] font-semibold text-slate-500 mb-1">Body:</div>
                                <pre class="bg-slate-950 text-slate-300 p-3 rounded-lg font-mono text-[11px] overflow-x-auto whitespace-pre-wrap max-h-[450px]" id="selectedPacketResBody"></pre>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>
        <!-- AI 分析文档生成结果与预览区 -->
        <div class="hidden glass-card rounded-xl p-5 flex flex-col gap-4 border border-emerald-500/30" id="packetDocPreviewContainer">
            <div class="flex justify-between items-center pb-2 border-b border-outline-variant/30">
                <span class="text-[12px] font-bold text-emerald-600 dark:text-emerald-400 uppercase tracking-wider flex items-center gap-1.5" data-i18n="aiDocPreview">
                    AI 分析文档预览 (API Documentation Preview)
                </span>
                <button class="text-[11px] text-primary hover:underline flex items-center gap-0.5" id="btnCopyGeneratedDoc">
                    <span class="material-symbols-outlined text-[12px]">content_copy</span><span data-i18n="btnCopyDoc">复制文档内容</span>
                </button>
            </div>
            <!-- 生成的 Markdown 实时显示（支持打字机或直接文本渲染，暗色磨砂编辑器卡片） -->
            <textarea class="w-full h-[550px] bg-slate-950 text-slate-200 font-mono text-[12.5px] p-4 rounded-lg focus:outline-none border border-outline-variant/20 resize-y leading-relaxed" id="packetDocPreviewText" readonly></textarea>
        </div>
        <!-- AI 分析骨架屏/Loading 遮罩 -->
        <div class="hidden fixed inset-0 z-50 bg-[#10131c]/80 flex flex-col items-center justify-center" id="packetAnalyzeLoading">
            <div class="bg-white dark:bg-[#1a1f30] border border-outline-variant/30 rounded-2xl p-8 max-w-[420px] text-center flex flex-col items-center gap-5 shadow-2xl">
                <!-- 旋转的 AI 脑电波动画 -->
                <div class="relative w-16 h-16 flex items-center justify-center">
                    <div class="absolute inset-0 rounded-full border-4 border-primary/20 animate-ping"></div>
                    <div class="absolute inset-0 rounded-full border-4 border-t-primary animate-spin"></div>
                    <span class="material-symbols-outlined text-primary text-[32px] animate-pulse">psychology</span>
                </div>
                <div>
                    <h3 class="text-base font-bold text-on-surface dark:text-white mb-1.5" data-i18n="aiAnalyzingTitle">正在利用 AI 分析并生成接口文档...</h3>
                    <p class="text-xs text-outline leading-relaxed" data-i18n="aiAnalyzingDesc">正在梳理抓包报文并结合接口上下文推断字段、类型及参数，这一过程可能需要 15-45 秒，请耐心等待。</p>
                </div>
                <!-- 进度描述 -->
                <div class="text-[12px] font-semibold font-data-mono text-primary animate-pulse bg-primary/10 px-3 py-1 rounded" id="packetAnalyzeProgressMsg" data-i18n="aiConnecting">正在连接 Gemini 2.5 Flash...</div>
        </div>
    </div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
  // Logic from packets controller will go here
});
</script>
