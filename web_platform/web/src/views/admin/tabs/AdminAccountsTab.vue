<template>
  <div class="space-y-4">
    <!-- 顶部单行 Header 与控制区 -->
    <div class="flex flex-wrap items-center justify-between gap-4 pb-2 border-b border-slate-800">
      <!-- 左侧：标题 + 说明 Tooltip + 通道切换 Tab -->
      <div class="flex flex-wrap items-center gap-3">
        <div class="flex items-center gap-2">
          <h2 class="text-xl font-bold text-white tracking-tight">账号池管理</h2>
          <div class="group relative flex items-center">
            <span class="material-symbols-outlined text-[16px] text-slate-400 hover:text-indigo-400 cursor-help transition-colors">help_outline</span>
            <div class="absolute left-0 top-full mt-2 hidden group-hover:flex flex-col z-50 pointer-events-none w-72">
              <div class="bg-slate-900 text-slate-200 text-xs leading-relaxed p-3 rounded-xl shadow-2xl border border-slate-700/80">
                统一调度与管理多通道多账号凭据。开启负载均衡与轮询策略后自动按并发限制路由切号，无缝对齐桌面端底层号池。
              </div>
            </div>
          </div>
        </div>

        <div class="h-4 w-[1px] bg-slate-800 hidden sm:block"></div>

        <!-- 通道分类切换 Tab -->
        <div class="flex gap-1 bg-slate-900/80 border border-slate-800 p-1 rounded-xl text-xs">
          <button
            v-for="ch in channels"
            :key="ch.id"
            type="button"
            class="px-3 py-1.5 rounded-lg font-medium transition-all duration-150 cursor-pointer whitespace-nowrap flex items-center gap-1.5"
            :class="activeChannel === ch.id ? 'bg-indigo-600 text-white shadow-sm shadow-indigo-600/30 font-semibold' : 'text-slate-400 hover:text-white hover:bg-slate-800/60'"
            @click="switchChannel(ch.id)"
          >
            <span class="material-symbols-outlined text-14px">{{ ch.icon }}</span>
            <span>{{ ch.name }}</span>
            <span class="text-[10px] px-1.5 py-0.2 rounded-full" :class="activeChannel === ch.id ? 'bg-indigo-500/50 text-white' : 'bg-slate-800 text-slate-400'">
              {{ getChannelCount(ch.id) }}
            </span>
          </button>
        </div>
      </div>

      <!-- 右侧：号池调度算法、并发限制与操作按钮组 -->
      <div class="flex flex-wrap items-center gap-2.5">
        <!-- NVIDIA 号池控制栏 -->
        <div v-if="activeChannel === 'nvidia'" class="flex items-center gap-2 bg-slate-900/80 border border-slate-800 px-3 py-1.5 rounded-xl text-xs">
          <span class="text-slate-400 font-medium">轮询算法</span>
          <select
            v-model="poolConfig.nvidiaLbMode"
            class="bg-slate-800 text-white font-medium border border-slate-700/80 rounded-lg px-2 py-1 focus:outline-none focus:border-indigo-500 cursor-pointer"
            @change="saveConfig"
          >
            <option value="round-robin">游标轮询 (默认)</option>
            <option value="sticky">粘性会话</option>
            <option value="balance">指标均衡</option>
          </select>

          <div class="h-4 w-[1px] bg-slate-800 mx-1"></div>

          <span class="text-slate-400 font-medium whitespace-nowrap">并发上限</span>
          <input
            v-model.number="poolConfig.nvidiaMaxConcurrency"
            type="number"
            min="1"
            max="1000"
            class="w-14 px-1.5 py-1 bg-slate-800 border border-slate-700/80 rounded-lg text-white text-center focus:outline-none focus:border-indigo-500"
            title="0=未配置(默认10);超过自动换号"
            @blur="saveConfig"
          />
        </div>

        <!-- Grok 号池控制栏 -->
        <div v-else-if="activeChannel === 'grok'" class="flex items-center gap-2 bg-slate-900/80 border border-slate-800 px-3 py-1.5 rounded-xl text-xs">
          <span class="text-slate-400 font-medium">轮询算法</span>
          <select
            v-model="poolConfig.grokLbMode"
            class="bg-slate-800 text-white font-medium border border-slate-700/80 rounded-lg px-2 py-1 focus:outline-none focus:border-indigo-500 cursor-pointer"
            @change="saveConfig"
          >
            <option value="round-robin">游标轮询 (默认)</option>
            <option value="sticky">粘性会话</option>
          </select>

          <div class="h-4 w-[1px] bg-slate-800 mx-1"></div>

          <span class="text-slate-400 font-medium whitespace-nowrap">并发上限</span>
          <input
            v-model.number="poolConfig.grokMaxConcurrency"
            type="number"
            min="1"
            max="1000"
            class="w-14 px-1.5 py-1 bg-slate-800 border border-slate-700/80 rounded-lg text-white text-center focus:outline-none focus:border-indigo-500"
            @blur="saveConfig"
          />
        </div>

        <!-- Other 号池按组控制栏 -->
        <div v-else-if="activeChannel === 'other' && selectedOtherGroup" class="flex items-center gap-2 bg-slate-900/80 border border-slate-800 px-3 py-1.5 rounded-xl text-xs">
          <span class="text-slate-400 font-medium">组内负载均衡</span>
          <select
            v-model="currentOtherGroupLbMode"
            class="bg-slate-800 text-white font-medium border border-slate-700/80 rounded-lg px-2 py-1 focus:outline-none focus:border-indigo-500 cursor-pointer"
            @change="saveOtherGroupConfig"
          >
            <option value="round-robin">轮询 (默认)</option>
            <option value="sticky">粘性</option>
          </select>

          <div class="h-4 w-[1px] bg-slate-800 mx-1"></div>

          <span class="text-slate-400 font-medium whitespace-nowrap">并发上限</span>
          <input
            v-model.number="currentOtherGroupConcurrency"
            type="number"
            min="1"
            max="1000"
            class="w-14 px-1.5 py-1 bg-slate-800 border border-slate-700/80 rounded-lg text-white text-center focus:outline-none focus:border-indigo-500"
            @blur="saveOtherGroupConfig"
          />
        </div>

        <!-- Antigravity / Project 控制栏 -->
        <div v-else-if="activeChannel === 'antigravity' || activeChannel === 'project'" class="flex items-center gap-2 bg-slate-900/80 border border-slate-800 px-3 py-1.5 rounded-xl text-xs">
          <span class="text-slate-400 font-medium">负载均衡</span>
          <button
            type="button"
            class="relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none"
            :class="poolConfig.poolMode ? 'bg-indigo-600' : 'bg-slate-700'"
            @click="togglePoolMode"
          >
            <span
              class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out"
              :class="poolConfig.poolMode ? 'translate-x-4' : 'translate-x-0'"
            ></span>
          </button>

          <div class="h-4 w-[1px] bg-slate-800 mx-1"></div>

          <span class="text-slate-400 font-medium whitespace-nowrap">并发上限</span>
          <input
            v-if="activeChannel === 'antigravity'"
            v-model.number="poolConfig.antigravityMaxConcurrency"
            type="number"
            min="1"
            max="1000"
            class="w-14 px-1.5 py-1 bg-slate-800 border border-slate-700/80 rounded-lg text-white text-center focus:outline-none focus:border-indigo-500"
            @blur="saveConfig"
          />
          <input
            v-else
            v-model.number="poolConfig.projectMaxConcurrency"
            type="number"
            min="1"
            max="1000"
            class="w-14 px-1.5 py-1 bg-slate-800 border border-slate-700/80 rounded-lg text-white text-center focus:outline-none focus:border-indigo-500"
            @blur="saveConfig"
          />
        </div>

        <!-- 导出按钮 -->
        <button
          type="button"
          class="flex items-center gap-1.5 px-3 py-1.5 bg-slate-900/80 hover:bg-slate-800 border border-slate-800 rounded-xl text-xs font-semibold text-slate-300 hover:text-white transition-all shadow-sm"
          @click="exportCurrentAccounts"
        >
          <span class="material-symbols-outlined text-16px">download</span>
          <span>导出</span>
        </button>

        <!-- 导入按钮 -->
        <button
          type="button"
          class="flex items-center gap-1.5 px-3 py-1.5 bg-slate-900/80 hover:bg-slate-800 border border-slate-800 rounded-xl text-xs font-semibold text-slate-300 hover:text-white transition-all shadow-sm"
          @click="showImportModal = true"
        >
          <span class="material-symbols-outlined text-16px">upload</span>
          <span>导入</span>
        </button>

        <!-- 添加账号按钮 -->
        <button
          type="button"
          class="flex items-center gap-1.5 px-4 py-1.5 bg-indigo-600 hover:bg-indigo-500 text-white rounded-xl text-xs font-bold transition-all shadow-md shadow-indigo-600/30"
          @click="openAddModal"
        >
          <span class="material-symbols-outlined text-16px">add_circle</span>
          <span>添加账号</span>
        </button>
      </div>
    </div>

    <!-- Other 号池二级渠道组药丸导航 (仅在 Other 通道展示) -->
    <div v-if="activeChannel === 'other'" class="flex flex-wrap items-center gap-1.5 p-1.5 bg-slate-900/60 border border-slate-800/80 rounded-xl text-xs">
      <button
        type="button"
        class="px-3 py-1 rounded-lg transition-all"
        :class="selectedOtherGroup === '' ? 'bg-indigo-600 text-white font-bold' : 'text-slate-400 hover:text-white hover:bg-slate-800'"
        @click="selectedOtherGroup = ''"
      >
        全部显示 ({{ otherAccountsTotalCount }})
      </button>
      <button
        v-for="grp in otherGroups"
        :key="grp.groupId"
        type="button"
        class="px-3 py-1 rounded-lg transition-all flex items-center gap-1.5"
        :class="selectedOtherGroup === grp.groupId ? 'bg-indigo-600 text-white font-bold' : 'text-slate-400 hover:text-white hover:bg-slate-800'"
        @click="selectedOtherGroup = grp.groupId"
      >
        <span>{{ grp.groupName }}</span>
        <span class="text-[10px] px-1.5 py-0.2 rounded-full bg-slate-800 text-slate-300">{{ grp.accountCount }}</span>
      </button>
    </div>

    <!-- 工具栏 Toolbar -->
    <div class="p-3 bg-slate-900/80 border border-slate-800/80 rounded-2xl flex flex-wrap items-center justify-between gap-3 text-xs">
      <div class="flex flex-wrap items-center gap-2.5">
        <!-- 搜索框 -->
        <div class="relative flex items-center">
          <span class="material-symbols-outlined absolute left-2.5 text-16px text-slate-500 pointer-events-none">search</span>
          <input
            v-model="searchQuery"
            type="text"
            placeholder="搜索邮箱、标识或项目 ID..."
            class="pl-8 pr-3 py-1.5 bg-slate-800/80 border border-slate-700/80 rounded-xl text-xs text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 w-52 sm:w-64 transition-all"
          />
        </div>

        <!-- 状态筛选 -->
        <select
          v-model="statusFilter"
          class="px-2.5 py-1.5 bg-slate-800/80 border border-slate-700/80 rounded-xl text-xs text-white focus:outline-none focus:border-indigo-500 cursor-pointer"
        >
          <option value="all">全部状态</option>
          <option value="enabled">仅看启用中</option>
          <option value="disabled">仅看已停用</option>
          <option value="cooling">仅看冷静中</option>
        </select>

        <!-- 布局切换 -->
        <div class="flex items-center bg-slate-800/60 p-0.5 rounded-lg border border-slate-700/60 ml-1">
          <button
            type="button"
            class="p-1 rounded-md transition-all cursor-pointer flex items-center justify-center"
            :class="layoutMode === 'grid' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white'"
            title="网格卡片布局"
            @click="layoutMode = 'grid'"
          >
            <span class="material-symbols-outlined text-16px">grid_view</span>
          </button>
          <button
            type="button"
            class="p-1 rounded-md transition-all cursor-pointer flex items-center justify-center"
            :class="layoutMode === 'list' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white'"
            title="列表表格布局"
            @click="layoutMode = 'list'"
          >
            <span class="material-symbols-outlined text-16px">view_list</span>
          </button>
        </div>

        <!-- 列数选择器 (网格模式) -->
        <select
          v-if="layoutMode === 'grid'"
          v-model="gridColumns"
          class="px-2 py-1.5 bg-slate-800/80 border border-slate-700/80 rounded-xl text-xs text-white focus:outline-none focus:border-indigo-500 cursor-pointer"
        >
          <option :value="3">3 列</option>
          <option :value="4">4 列</option>
          <option :value="5">5 列</option>
        </select>
      </div>

      <!-- 右侧统计与全选 -->
      <div class="flex items-center gap-3">
        <span class="text-xs font-semibold text-indigo-400 bg-indigo-500/10 border border-indigo-500/20 px-3 py-1 rounded-xl">
          共 {{ filteredAccounts.length }} 个账号
        </span>
      </div>
    </div>

    <!-- 批量操作栏 (当有复选项时滑出) -->
    <div
      v-if="selectedAccountIds.length > 0"
      class="p-3 bg-indigo-950/40 border border-indigo-500/30 rounded-2xl flex items-center justify-between gap-3 text-xs animate-fade-in"
    >
      <div class="flex items-center gap-2 text-indigo-200">
        <span class="material-symbols-outlined text-16px text-indigo-400">check_circle</span>
        <span class="font-bold">已选择 {{ selectedAccountIds.length }} 个账号</span>
      </div>
      <div class="flex items-center gap-2">
        <button
          type="button"
          class="px-3 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg font-medium transition-colors"
          @click="selectedAccountIds = []"
        >
          取消选择
        </button>
        <button
          type="button"
          :disabled="batchDeleting"
          class="px-3.5 py-1.5 bg-rose-500/20 hover:bg-rose-500 text-rose-300 hover:text-white border border-rose-500/40 rounded-lg font-bold transition-all flex items-center gap-1.5 shadow-sm"
          @click="handleBatchDelete"
        >
          <span v-if="batchDeleting" class="material-symbols-outlined text-14px animate-spin">progress_activity</span>
          <span v-else class="material-symbols-outlined text-14px">delete</span>
          <span>批量删除</span>
        </button>
      </div>
    </div>

    <!-- 账号展示主体 -->
    <div v-if="loading" class="py-16 flex flex-col items-center justify-center text-slate-500">
      <span class="material-symbols-outlined text-32px animate-spin text-indigo-500 mb-2">progress_activity</span>
      <span class="text-xs">加载账号池中...</span>
    </div>

    <div v-else-if="filteredAccounts.length === 0" class="py-16 flex flex-col items-center justify-center text-slate-500 border border-dashed border-slate-800 rounded-2xl">
      <span class="material-symbols-outlined text-48px text-slate-600 mb-2">account_circle_off</span>
      <span class="text-xs text-slate-400">暂无符合条件的账号</span>
    </div>

    <!-- 视图 1: 卡片九宫格模式 (Grid) -->
    <div
      v-else-if="layoutMode === 'grid'"
      class="grid gap-3.5"
      :class="gridColsClass"
    >
      <div
        v-for="acc in paginatedAccounts"
        :key="acc.id"
        class="bg-slate-900/80 border rounded-2xl p-4 flex flex-col justify-between transition-all hover:border-slate-700 shadow-lg relative group"
        :class="[
          selectedAccountIds.includes(acc.id) ? 'border-indigo-500/70 bg-indigo-950/20' : 'border-slate-800/80',
          !acc.enabled ? 'opacity-60' : ''
        ]"
      >
        <!-- 顶部信息行: 复选框 + 邮箱 + 状态 -->
        <div>
          <div class="flex items-start justify-between gap-2 mb-2">
            <div class="flex items-start gap-2 overflow-hidden">
              <input
                type="checkbox"
                :value="acc.id"
                :checked="selectedAccountIds.includes(acc.id)"
                class="mt-1 rounded bg-slate-800 border-slate-700 text-indigo-600 focus:ring-0 cursor-pointer"
                @change="toggleSelectAccount(acc.id)"
              />
              <div class="overflow-hidden">
                <div class="font-bold text-white text-xs truncate" :title="acc.email">
                  {{ acc.email || acc.id }}
                </div>
                <div class="flex items-center gap-1.5 mt-1">
                  <span class="px-1.5 py-0.2 rounded text-[10px] font-bold bg-amber-500/10 text-amber-400 border border-amber-500/20">
                    {{ acc.groupName || acc.provider.toUpperCase() }}
                  </span>
                  <span v-if="acc.addedAt" class="text-[10px] text-slate-500 truncate">
                    {{ formatAddedAt(acc.addedAt) }}
                  </span>
                </div>
              </div>
            </div>

            <!-- 状态 Badge -->
            <span
              v-if="acc.cooldownUntil && acc.cooldownUntil > Date.now()"
              class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-sky-500/10 text-sky-400 border border-sky-500/20 whitespace-nowrap"
            >
              冷静中
            </span>
            <span
              v-else-if="acc.enabled"
              class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 whitespace-nowrap"
            >
              活跃
            </span>
            <span
              v-else
              class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-slate-800 text-slate-500 whitespace-nowrap"
            >
              已停用
            </span>
          </div>

          <!-- 配额/模型说明栏 -->
          <div class="bg-slate-800/40 border border-slate-800 rounded-xl p-2.5 mt-2.5 text-[11px] space-y-1">
            <div class="text-slate-400 flex items-center justify-between">
              <span>配额与凭据:</span>
              <span class="font-mono text-slate-300">{{ acc.maskedKey || '免 Key / 内部凭证' }}</span>
            </div>
            <div v-if="acc.defaultModel || acc.tier" class="text-slate-400 flex items-center justify-between">
              <span>关联模型/Tier:</span>
              <span class="text-indigo-300 font-medium truncate max-w-[150px]">{{ acc.defaultModel || acc.tier }}</span>
            </div>
          </div>
        </div>

        <!-- 底部操作与开关行 -->
        <div class="flex items-center justify-between pt-3 mt-3 border-t border-slate-800/80 text-xs">
          <!-- Toggle 启停 -->
          <div
            class="flex items-center gap-2 cursor-pointer select-none group/toggle"
            @click="handleToggleAccount(acc)"
          >
            <div
              class="w-9 h-5 rounded-full transition-colors duration-200 relative flex items-center px-0.5"
              :class="acc.enabled ? 'bg-indigo-600' : 'bg-slate-700'"
            >
              <div
                class="w-4 h-4 bg-white rounded-full shadow-md transform transition-transform duration-200 ease-in-out"
                :class="acc.enabled ? 'translate-x-4' : 'translate-x-0'"
              ></div>
            </div>
            <span
              class="text-[11px] font-bold tracking-wide transition-colors"
              :class="acc.enabled ? 'text-emerald-400' : 'text-slate-500'"
            >
              {{ acc.enabled ? '已启用' : '已停用' }}
            </span>
          </div>

          <!-- 操作按钮组 -->
          <div class="flex items-center gap-1">
            <button
              type="button"
              class="p-1.5 rounded-lg text-slate-400 hover:text-indigo-400 hover:bg-slate-800 transition-colors"
              title="导出此账号"
              @click="exportSingleAccount(acc)"
            >
              <span class="material-symbols-outlined text-16px">download</span>
            </button>
            <button
              type="button"
              class="p-1.5 rounded-lg text-slate-400 hover:text-indigo-400 hover:bg-slate-800 transition-colors"
              title="编辑账号"
              @click="openEditModal(acc)"
            >
              <span class="material-symbols-outlined text-16px">edit</span>
            </button>
            <button
              type="button"
              class="p-1.5 rounded-lg text-slate-400 hover:text-rose-400 hover:bg-slate-800 transition-colors"
              title="删除账号"
              @click="handleDeleteAccount(acc)"
            >
              <span class="material-symbols-outlined text-16px">delete</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 视图 2: 列表表格模式 (List) -->
    <div v-else class="overflow-x-auto bg-slate-900/80 border border-slate-800/80 rounded-2xl shadow-xl">
      <table class="w-full text-left text-xs text-slate-300">
        <thead class="bg-slate-800/50 text-slate-400 font-semibold border-b border-slate-800">
          <tr>
            <th class="p-3 w-10">
              <input
                type="checkbox"
                :checked="allPageSelected"
                class="rounded bg-slate-800 border-slate-700 text-indigo-600 focus:ring-0 cursor-pointer"
                @change="toggleSelectAllPage"
              />
            </th>
            <th class="p-3">账号标识 / 邮箱</th>
            <th class="p-3">通道 / 渠道组</th>
            <th class="p-3">API Key 凭证</th>
            <th class="p-3">默认模型 / 端点</th>
            <th class="p-3">状态</th>
            <th class="p-3">启停</th>
            <th class="p-3 text-right">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-800">
          <tr
            v-for="acc in paginatedAccounts"
            :key="acc.id"
            class="hover:bg-slate-800/40 transition-colors"
            :class="[
              selectedAccountIds.includes(acc.id) ? 'bg-indigo-950/20' : '',
              !acc.enabled ? 'opacity-60' : ''
            ]"
          >
            <td class="p-3">
              <input
                type="checkbox"
                :value="acc.id"
                :checked="selectedAccountIds.includes(acc.id)"
                class="rounded bg-slate-800 border-slate-700 text-indigo-600 focus:ring-0 cursor-pointer"
                @change="toggleSelectAccount(acc.id)"
              />
            </td>
            <td class="p-3 font-bold text-white">
              {{ acc.email || acc.id }}
              <div v-if="acc.addedAt" class="text-[10px] text-slate-500 font-normal">
                {{ formatAddedAt(acc.addedAt) }}
              </div>
            </td>
            <td class="p-3">
              <span class="px-2 py-0.5 rounded text-[11px] font-bold bg-slate-800 text-slate-300 border border-slate-700">
                {{ acc.groupName || acc.provider.toUpperCase() }}
              </span>
            </td>
            <td class="p-3 font-mono text-slate-400">
              {{ acc.maskedKey || '—' }}
            </td>
            <td class="p-3">
              <div class="text-indigo-300 font-medium truncate max-w-[200px]">{{ acc.defaultModel || '默认路由' }}</div>
              <div class="text-[10px] text-slate-500 font-mono truncate max-w-[200px]">{{ acc.baseUrl || '官方默认端点' }}</div>
            </td>
            <td class="p-3">
              <span
                v-if="acc.cooldownUntil && acc.cooldownUntil > Date.now()"
                class="px-2 py-0.5 rounded text-[10px] font-semibold bg-sky-500/10 text-sky-400 border border-sky-500/20"
              >
                冷静中
              </span>
              <span
                v-else-if="acc.enabled"
                class="px-2 py-0.5 rounded text-[10px] font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20"
              >
                活跃
              </span>
              <span
                v-else
                class="px-2 py-0.5 rounded text-[10px] font-semibold bg-slate-800 text-slate-500"
              >
                已停用
              </span>
            </td>
            <td class="p-3">
              <div
                class="inline-flex items-center gap-2 cursor-pointer select-none"
                @click="handleToggleAccount(acc)"
              >
                <div
                  class="w-9 h-5 rounded-full transition-colors duration-200 relative flex items-center px-0.5"
                  :class="acc.enabled ? 'bg-indigo-600' : 'bg-slate-700'"
                >
                  <div
                    class="w-4 h-4 bg-white rounded-full shadow-md transform transition-transform duration-200 ease-in-out"
                    :class="acc.enabled ? 'translate-x-4' : 'translate-x-0'"
                  ></div>
                </div>
                <span class="text-[11px] font-bold" :class="acc.enabled ? 'text-emerald-400' : 'text-slate-500'">
                  {{ acc.enabled ? '已启用' : '已停用' }}
                </span>
              </div>
            </td>
            <td class="p-3 text-right">
              <div class="flex items-center justify-end gap-1.5">
                <button
                  type="button"
                  class="p-1 rounded text-slate-400 hover:text-indigo-400 transition-colors"
                  title="导出此账号"
                  @click="exportSingleAccount(acc)"
                >
                  <span class="material-symbols-outlined text-16px">download</span>
                </button>
                <button
                  type="button"
                  class="p-1 rounded text-slate-400 hover:text-indigo-400 transition-colors"
                  title="编辑"
                  @click="openEditModal(acc)"
                >
                  <span class="material-symbols-outlined text-16px">edit</span>
                </button>
                <button
                  type="button"
                  class="p-1 rounded text-slate-400 hover:text-rose-400 transition-colors"
                  title="删除"
                  @click="handleDeleteAccount(acc)"
                >
                  <span class="material-symbols-outlined text-16px">delete</span>
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 分页控制栏 -->
    <div v-if="filteredAccounts.length > 0" class="flex flex-wrap items-center justify-between pt-3 border-t border-slate-800/80 text-xs text-slate-400">
      <div>
        显示第 {{ paginationStart }} 到 {{ paginationEnd }} 个账号，共 {{ filteredAccounts.length }} 条
      </div>
      <div class="flex items-center gap-1.5">
        <button
          type="button"
          :disabled="currentPage === 1"
          class="px-2.5 py-1 rounded-lg border border-slate-800 hover:bg-slate-800 text-slate-300 disabled:opacity-40 disabled:pointer-events-none transition-colors flex items-center gap-0.5"
          @click="currentPage--"
        >
          <span class="material-symbols-outlined text-14px">chevron_left</span>
          <span>上一页</span>
        </button>
        <span class="px-2 font-bold text-white">{{ currentPage }} / {{ totalPages }}</span>
        <button
          type="button"
          :disabled="currentPage === totalPages"
          class="px-2.5 py-1 rounded-lg border border-slate-800 hover:bg-slate-800 text-slate-300 disabled:opacity-40 disabled:pointer-events-none transition-colors flex items-center gap-0.5"
          @click="currentPage++"
        >
          <span>下一页</span>
          <span class="material-symbols-outlined text-14px">chevron_right</span>
        </button>
      </div>
    </div>

    <!-- 弹窗组件挂载 -->
    <AccountModal
      :show="showAccountModal"
      :edit-account="editingAccount"
      :current-channel="activeChannel"
      :existing-other-groups="otherGroups"
      @close="showAccountModal = false"
      @saved="loadAccounts"
    />

    <AccountImportModal
      :show="showImportModal"
      :current-channel="activeChannel"
      @close="showImportModal = false"
      @imported="onAccountsImported"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { accountApi, type AccountItem, type PoolConfig } from '../../../api/client'
import AccountModal from './AccountModal.vue'
import AccountImportModal from './AccountImportModal.vue'

const channels = [
  { id: 'antigravity', name: 'Antigravity 官方账号', icon: 'extension' },
  { id: 'project', name: '谷歌云项目 API', icon: 'cloud' },
  { id: 'nvidia', name: 'NVIDIA 号池', icon: 'bolt' },
  { id: 'grok', name: 'Grok 号池', icon: 'smart_toy' },
  { id: 'other', name: 'Other 号池', icon: 'hub' },
]

const activeChannel = ref('nvidia')
const selectedOtherGroup = ref('')
const layoutMode = ref<'grid' | 'list'>('grid')
const gridColumns = ref(5)
const searchQuery = ref('')
const statusFilter = ref('all')
const currentPage = ref(1)
const pageSize = 10

const loading = ref(false)
const batchDeleting = ref(false)
const allAccounts = ref<AccountItem[]>([])
const otherGroups = ref<any[]>([])
const selectedAccountIds = ref<string[]>([])

const showAccountModal = ref(false)
const editingAccount = ref<AccountItem | null>(null)
const showImportModal = ref(false)

const poolConfig = ref<PoolConfig>({
  poolMode: true,
  projectPoolMode: false,
  geminiCliPoolMode: false,
  activeChannel: 'nvidia',
  otherLbModes: {},
  nvidiaLbMode: 'round-robin',
  grokLbMode: 'round-robin',
  nvidiaMaxConcurrency: 40,
  antigravityMaxConcurrency: 10,
  antigravityCliVersion: '2.3.1',
  projectMaxConcurrency: 10,
  otherMaxConcurrency: {},
  grokMaxConcurrency: 10,
  grokCliVersion: '1.0.0',
  grokQuotaCooldownHours: 24,
})

const gridColsClass = computed(() => {
  if (gridColumns.value === 3) return 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3'
  if (gridColumns.value === 4) return 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-4'
  return 'grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5'
})

const otherAccountsTotalCount = computed(() => {
  return allAccounts.value.filter((a) => a.provider.toLowerCase() === 'other').length
})

const currentOtherGroupLbMode = computed({
  get: () => {
    if (!selectedOtherGroup.value) return 'round-robin'
    return poolConfig.value.otherLbModes[selectedOtherGroup.value] || 'round-robin'
  },
  set: (val: string) => {
    if (selectedOtherGroup.value) {
      poolConfig.value.otherLbModes[selectedOtherGroup.value] = val
    }
  },
})

const currentOtherGroupConcurrency = computed({
  get: () => {
    if (!selectedOtherGroup.value) return 10
    return poolConfig.value.otherMaxConcurrency[selectedOtherGroup.value] || 10
  },
  set: (val: number) => {
    if (selectedOtherGroup.value) {
      poolConfig.value.otherMaxConcurrency[selectedOtherGroup.value] = val
    }
  },
})

const filteredAccounts = computed(() => {
  let list = allAccounts.value.filter((a) => a.provider.toLowerCase() === activeChannel.value.toLowerCase())

  if (activeChannel.value === 'other' && selectedOtherGroup.value) {
    list = list.filter((a) => a.groupId === selectedOtherGroup.value)
  }

  if (statusFilter.value === 'enabled') {
    list = list.filter((a) => a.enabled)
  } else if (statusFilter.value === 'disabled') {
    list = list.filter((a) => !a.enabled)
  } else if (statusFilter.value === 'cooling') {
    list = list.filter((a) => a.cooldownUntil && a.cooldownUntil > Date.now())
  }

  const q = searchQuery.value.trim().toLowerCase()
  if (q) {
    list = list.filter(
      (a) =>
        (a.email && a.email.toLowerCase().includes(q)) ||
        (a.projectId && a.projectId.toLowerCase().includes(q)) ||
        (a.groupName && a.groupName.toLowerCase().includes(q)) ||
        (a.defaultModel && a.defaultModel.toLowerCase().includes(q))
    )
  }

  return list
})

const totalPages = computed(() => Math.max(1, Math.ceil(filteredAccounts.value.length / pageSize)))
const paginationStart = computed(() => (currentPage.value - 1) * pageSize + 1)
const paginationEnd = computed(() => Math.min(currentPage.value * pageSize, filteredAccounts.value.length))

const paginatedAccounts = computed(() => {
  const start = (currentPage.value - 1) * pageSize
  return filteredAccounts.value.slice(start, start + pageSize)
})

const allPageSelected = computed(() => {
  if (paginatedAccounts.value.length === 0) return false
  return paginatedAccounts.value.every((a) => selectedAccountIds.value.includes(a.id))
})

onMounted(async () => {
  await loadAccounts()
})

async function loadAccounts() {
  loading.value = true
  try {
    const res = await accountApi.getAccounts()
    if (res) {
      allAccounts.value = res.accounts || []
      if (res.config) {
        poolConfig.value = {
          ...poolConfig.value,
          ...res.config,
          otherLbModes: res.config.otherLbModes || {},
          otherMaxConcurrency: res.config.otherMaxConcurrency || {},
        }
      }
      otherGroups.value = res.otherGroups || []
    }
  } catch (err) {
    console.error('加载账号池数据失败:', err)
  } finally {
    loading.value = false
  }
}

function getChannelCount(channelId: string): number {
  return allAccounts.value.filter((a) => a.provider.toLowerCase() === channelId.toLowerCase()).length
}

function switchChannel(channelId: string) {
  activeChannel.value = channelId
  selectedOtherGroup.value = ''
  currentPage.value = 1
  selectedAccountIds.value = []
}

function formatAddedAt(str: string): string {
  if (!str) return ''
  try {
    const d = new Date(str)
    return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
  } catch {
    return str
  }
}

async function saveConfig() {
  try {
    await accountApi.savePoolConfig(poolConfig.value)
  } catch (err) {
    console.error('保存号池配置失败:', err)
  }
}

async function saveOtherGroupConfig() {
  await saveConfig()
}

async function togglePoolMode() {
  poolConfig.value.poolMode = !poolConfig.value.poolMode
  await saveConfig()
}

function toggleSelectAccount(id: string) {
  const idx = selectedAccountIds.value.indexOf(id)
  if (idx >= 0) {
    selectedAccountIds.value.splice(idx, 1)
  } else {
    selectedAccountIds.value.push(id)
  }
}

function toggleSelectAllPage() {
  if (allPageSelected.value) {
    const pageIds = paginatedAccounts.value.map((a) => a.id)
    selectedAccountIds.value = selectedAccountIds.value.filter((id) => !pageIds.includes(id))
  } else {
    const pageIds = paginatedAccounts.value.map((a) => a.id)
    for (const id of pageIds) {
      if (!selectedAccountIds.value.includes(id)) {
        selectedAccountIds.value.push(id)
      }
    }
  }
}

async function handleToggleAccount(acc: AccountItem) {
  const target = !acc.enabled
  try {
    await accountApi.toggleAccount(acc.id, target)
    acc.enabled = target
  } catch (err) {
    console.error('切换账号状态失败:', err)
  }
}

function openAddModal() {
  editingAccount.value = null
  showAccountModal.value = true
}

function openEditModal(acc: AccountItem) {
  editingAccount.value = acc
  showAccountModal.value = true
}

async function handleDeleteAccount(acc: AccountItem) {
  if (!confirm(`确认删除账号 ${acc.email || acc.id} 吗？`)) return
  try {
    await accountApi.deleteAccount(acc.id)
    await loadAccounts()
  } catch (err) {
    console.error('删除失败:', err)
  }
}

async function handleBatchDelete() {
  if (!confirm(`确认批量删除选中的 ${selectedAccountIds.value.length} 个账号吗？`)) return
  batchDeleting.value = true
  try {
    await accountApi.batchDeleteAccounts(selectedAccountIds.value)
    selectedAccountIds.value = []
    await loadAccounts()
  } catch (err) {
    console.error('批量删除失败:', err)
  } finally {
    batchDeleting.value = false
  }
}

function exportCurrentAccounts() {
  const ch = activeChannel.value
  const list = filteredAccounts.value
  const blob = new Blob([JSON.stringify({ accounts: list }, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `accounts_${ch}_export.json`
  a.click()
  URL.revokeObjectURL(url)
}

function exportSingleAccount(acc: AccountItem) {
  const blob = new Blob([JSON.stringify({ accounts: [acc] }, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `account_${acc.email || acc.id}.json`
  a.click()
  URL.revokeObjectURL(url)
}

function onAccountsImported(count: number) {
  loadAccounts()
}
</script>
