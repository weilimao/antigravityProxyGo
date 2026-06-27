<template>
<div class="flex flex-col gap-5 w-full" id="view-accounts">
    <!-- 顶部单行 Header 区域 (Single-Row Header) -->
    <div class="flex flex-wrap items-center justify-between gap-4 pb-1">
        <!-- 左侧：标题 + 分割线 + 通道切换 Tab -->
        <div class="flex flex-wrap items-center gap-3 sm:gap-4">
            <div class="flex items-center gap-1.5">
                <h1 class="text-xl font-bold text-on-surface dark:text-white whitespace-nowrap">账号池管理</h1>
                <div class="relative group flex items-center">
                    <span class="material-symbols-outlined text-[16px] text-outline hover:text-primary transition-colors cursor-help">help_outline</span>
                    <div class="account-pool-tooltip absolute left-0 top-full mt-2 hidden flex-col items-start z-50 pointer-events-none w-64">
                        <div class="bg-slate-900/95 dark:bg-[#1f293d]/95 text-white text-[11px] leading-relaxed p-2.5 rounded-lg shadow-xl border border-white/10 text-left font-normal">
                            统一管理多个 Google OAuth 账号，开启负载均衡可自动进行请求分发，智能突破单账号额度限制。
                        </div>
                    </div>
                </div>
            </div>
            <div class="h-4 w-[1px] bg-outline-variant/30 hidden sm:block"></div>
            <!-- 通道分类切换 Tab -->
            <div class="flex gap-1 bg-slate-100 dark:bg-white/5 p-1 rounded-lg text-[12px]">
                <button class="px-3 py-1.5 rounded-md font-bold cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm whitespace-nowrap" id="btnChannelAntigravity" type="button">
                    Antigravity 官方账号
                </button>
                <button class="px-3 py-1.5 rounded-md font-medium cursor-pointer transition-all duration-200 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 whitespace-nowrap" id="btnChannelProject" type="button">
                    谷歌云项目 API
                </button>
            </div>
        </div>
        <!-- 右侧：负载均衡开关与操作按钮组 -->
        <div class="flex flex-wrap items-center gap-2 md:gap-3">
            <div class="flex items-center gap-2 bg-slate-50/50 dark:bg-white/5 px-3 py-1.5 rounded-lg border border-outline-variant/30 flex-shrink-0" id="poolModeContainer">
                <span class="text-[13px] font-medium text-on-surface dark:text-white" id="lblPoolMode">账号负载均衡</span>
                <div class="relative inline-block w-10 mr-1 align-middle select-none transition duration-200 ease-in">
                    <input class="toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out" id="poolModeToggle" type="checkbox">
                    <label class="toggle-label block overflow-hidden h-5 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer" for="poolModeToggle"></label>
                </div>
            </div>
            <button class="flex items-center gap-1.5 px-3 py-1.5 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded-md text-[13px] font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-white/5 transition-colors shadow-sm cursor-pointer whitespace-nowrap flex-shrink-0" id="btnExportAccounts">
                <span class="material-symbols-outlined text-[16px]">download</span>
                <span>导出</span>
            </button>
            <button class="flex items-center gap-1.5 px-3 py-1.5 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded-md text-[13px] font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-white/5 transition-colors shadow-sm cursor-pointer whitespace-nowrap flex-shrink-0" id="btnImportAccounts">
                <span class="material-symbols-outlined text-[16px]">upload</span>
                <span>导入</span>
            </button>
            <div class="relative flex-shrink-0">
                <button class="flex items-center gap-1.5 px-4 py-1.5 bg-primary text-white hover:bg-primary/90 rounded-md text-[13px] font-bold transition-colors shadow-sm whitespace-nowrap" id="btnAddAccount">
                    <span class="material-symbols-outlined text-[16px]">add_circle</span>
                    添加账号
                    <span class="material-symbols-outlined text-[16px]">arrow_drop_down</span>
                </button>
                <!-- 下拉菜单 -->
                <div class="absolute right-0 mt-2 w-48 bg-white dark:bg-[#1a1f30] border border-outline-variant/30 rounded-xl shadow-xl py-2 hidden z-50" id="addAccountDropdown">
                    <button class="w-full text-left px-4 py-2 text-[13px] text-on-surface dark:text-white hover:bg-slate-50 dark:hover:bg-white/5 transition-colors flex items-center gap-2" id="btnAntigravityLogin" onclick="startLogin('antigravity')">
                        <span class="material-symbols-outlined text-primary text-[16px]">extension</span>
                        <div>
                            <div class="font-bold">Antigravity (推荐)</div>
                            <div class="text-[10px] text-outline">使用官方插件凭证授权</div>
                        </div>
                    </button>
                </div>
            </div>
        </div>
    </div>
<!-- 账号列表卡片 -->
<div class="glass-card rounded-xl flex flex-col flex-1 min-h-[300px]">
        <div class="p-3 border-b border-outline-variant/30 flex flex-wrap items-center justify-between gap-3 bg-slate-50/50 dark:bg-white/5 rounded-t-xl" id="accountsToolbar">
            <div class="flex flex-wrap items-center gap-2">
                <div class="relative flex items-center">
                    <span class="material-symbols-outlined absolute left-2.5 text-[16px] text-outline pointer-events-none">search</span>
                    <input type="text" id="inputAccountSearch" placeholder="搜索邮箱或项目ID..." class="pl-8 pr-3 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary w-44 sm:w-52 transition-all" />
                </div>
                <select id="selectAccountStatus" class="px-2.5 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
                    <option value="all">全部状态</option>
                    <option value="enabled">仅看启用中</option>
                    <option value="disabled">仅看已停用</option>
                    <option value="cooling">仅看冷静中</option>
                </select>
                <select id="selectAccountTier" class="px-2.5 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
                    <option value="all">全部 Tier</option>
                    <option value="pro">Pro</option>
                    <option value="ultra">Ultra</option>
                    <option value="enterprise">Enterprise</option>
                    <option value="standard">Standard</option>
                    <option value="free">Free</option>
                </select>
            </div>
            <div class="flex flex-wrap items-center gap-2">
                <span class="text-[12px] font-medium text-primary dark:text-primary-fixed-dim bg-primary/10 px-2 py-0.5 rounded-md" id="accountCountBadge">共 0 个账号</span>
                <button class="flex items-center gap-1 text-[11px] font-medium text-outline dark:text-outline-variant hover:text-primary dark:hover:text-primary-fixed-dim bg-outline-variant/10 hover:bg-primary/10 border border-outline-variant/20 hover:border-primary/30 px-2.5 py-1 rounded-lg transition-all duration-200 select-none" id="btnShowSessionBindings" title="查看当前会话与账号的绑定映射关系">
                    <span class="material-symbols-outlined text-[14px]">hub</span>
                    <span>绑定关系</span>
                </button>
                <button class="flex items-center gap-1 text-[11px] font-medium text-outline dark:text-outline-variant hover:text-amber-500 dark:hover:text-amber-400 bg-outline-variant/10 hover:bg-amber-500/10 border border-outline-variant/20 hover:border-amber-500/30 px-2.5 py-1 rounded-lg transition-all duration-200 select-none" id="btnClearSessions" title="清空所有会话绑定，下次请求将重新均匀分配账号">
                    <span class="material-symbols-outlined text-[14px]">cleaning_services</span>
                    <span>清空绑定</span>
                </button>
                <button class="flex items-center gap-1 text-[11px] font-medium text-outline dark:text-outline-variant hover:text-primary dark:hover:text-primary-fixed-dim bg-outline-variant/10 hover:bg-primary/10 border border-outline-variant/20 hover:border-primary/30 px-2.5 py-1 rounded-lg transition-all duration-200 select-none" id="btnRefreshAllQuota" title="刷新所有账号配额">
                    <span class="material-symbols-outlined text-[14px]" id="btnRefreshAllIcon">sync</span>
                    <span>刷新配额</span>
                </button>
            </div>
        </div>
        <div class="flex-grow overflow-y-auto p-4 flex flex-col justify-between">
            <div>
                <div class="grid gap-3" id="accountsList">
                    <!-- 动态生成的账号卡片 -->
                </div>
                <div class="hidden flex-col items-center justify-center py-12 text-outline/50" id="accountsEmptyState">
                    <span class="material-symbols-outlined text-[48px] mb-2">account_circle_off</span>
                    <span class="text-[13px]">暂无符合条件的账号</span>
                </div>
            </div>
            <!-- 分页控制栏 -->
            <div class="flex flex-wrap items-center justify-between pt-3 border-t border-outline-variant/20 mt-4 text-[12px]" id="accountsPagination">
                <span class="text-outline text-[11px]" id="accountsPaginationInfo">显示 0 - 0 条，共 0 条</span>
                <div class="flex items-center gap-1.5" id="accountsPaginationBtns">
                    <button id="btnPrevAccountPage" class="px-2 py-1 rounded border border-outline-variant/30 text-outline hover:text-primary hover:bg-primary/5 disabled:opacity-40 disabled:pointer-events-none transition-colors text-[11px] flex items-center gap-0.5">
                        <span class="material-symbols-outlined text-[14px]">chevron_left</span> 上一页
                    </button>
                    <div id="accountPageNumbers" class="flex items-center gap-1"></div>
                    <button id="btnNextAccountPage" class="px-2 py-1 rounded border border-outline-variant/30 text-outline hover:text-primary hover:bg-primary/5 disabled:opacity-40 disabled:pointer-events-none transition-colors text-[11px] flex items-center gap-0.5">
                        下一页 <span class="material-symbols-outlined text-[14px]">chevron_right</span>
                    </button>
                </div>
            </div>
        </div>
    </div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
  // Logic from accounts controller will go here
});
</script>
