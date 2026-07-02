import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { generateSinglePacketMarkdown, formatJsonText } from './packetFormatter';
import i18n from '../shared/i18n';


let packetsList: any[] = [];
let selectedPacket: any = null;
let generatedDocContent = '';
let currentFilter = 'ALL';

// DOM Elements
let packetListContainer: HTMLElement | null;
let packetCountBadge: HTMLElement | null;
let packetDetailsPlaceholder: HTMLElement | null;
let packetDetailsContainer: HTMLElement | null;
let btnClearPackets: HTMLButtonElement | null;
let btnExportPacketLog: HTMLButtonElement | null;
let exportPacketsModal: HTMLElement | null;
let exportPacketsModalCloseBtn: HTMLButtonElement | null;
let btnExportPacketsCancel: HTMLButtonElement | null;
let btnExportPacketsConfirm: HTMLButtonElement | null;
let exportPacketsTypeSelect: HTMLSelectElement | null;

let selectedPacketMethod: HTMLElement | null;
let selectedPacketStatusCode: HTMLElement | null;
let selectedPacketUrl: HTMLElement | null;
let selectedPacketReqHeaders: HTMLElement | null;
let selectedPacketReqBody: HTMLElement | null;
let selectedPacketResHeaders: HTMLElement | null;
let selectedPacketResBody: HTMLElement | null;

let packetAnalyzeAccountSelect: HTMLSelectElement | null;
let btnStartPacketAnalyze: HTMLButtonElement | null;
let btnDownloadPacketDoc: HTMLButtonElement | null;
let packetDocPreviewContainer: HTMLElement | null;
let packetDocPreviewText: HTMLTextAreaElement | null;
let btnCopyGeneratedDoc: HTMLButtonElement | null;

let packetAnalyzeLoading: HTMLElement | null;
let packetAnalyzeProgressMsg: HTMLElement | null;

let btnCopyReqBody: HTMLButtonElement | null;
let btnCopyResBody: HTMLButtonElement | null;
let btnExportSinglePacket: HTMLButtonElement | null;

// Clipboard helper
export function copyElementText(elementId: string) {
    const el = document.getElementById(elementId) as any;
    if (!el) return;
    const text = el.textContent || el.value;
    if (!text) {
        alert(state.currentLanguage === 'zh' ? '没有可以复制的内容' : 'No content to copy');
        return;
    }
    navigator.clipboard.writeText(text).then(() => {
        alert(state.currentLanguage === 'zh' ? '复制成功！' : 'Copied successfully!');
    }).catch(() => {
        try {
            el.select();
            document.execCommand('copy');
            alert(state.currentLanguage === 'zh' ? '复制成功！' : 'Copied successfully!');
        } catch (err) {
            alert(state.currentLanguage === 'zh' ? '复制失败，请手动选择复制。' : 'Copy failed, please copy manually.');
        }
    });
}


// Render intercepted packages list
export async function refreshPacketsList() {
    packetListContainer = document.getElementById('packetListContainer');
    packetCountBadge = document.getElementById('packetCountBadge');
    packetDetailsPlaceholder = document.getElementById('packetDetailsPlaceholder');
    packetDetailsContainer = document.getElementById('packetDetailsContainer');

    if (!packetListContainer) return;
    
    try {
        packetsList = await ipcRenderer.invoke('packet:get-all');
        // Sort packets by timestamp descending (newest first)
        packetsList.sort((a, b) => b.timestamp.localeCompare(a.timestamp));
    } catch (e) {
        console.error('Failed to get packets:', e);
        packetsList = [];
    }

    // Resolve sources for all packets (with fallback for historical packets)
    packetsList.forEach(p => {
        let source = p.source;
        if (!source) {
            let ua = '';
            if (p.reqHeaders) {
                for (const key of Object.keys(p.reqHeaders)) {
                    if (key.toLowerCase() === 'user-agent') {
                        ua = p.reqHeaders[key];
                        break;
                    }
                }
            }
            const uaLower = ua.toLowerCase();
            if (uaLower.includes('antigravity/cli') || uaLower.includes('aidev_client')) {
                source = 'CLI';
            } else if (uaLower.includes('antigravity/ide') || uaLower.includes('cloudaicompanion') || uaLower.includes('google-api-nodejs-client') || uaLower.includes('go-http-client')) {
                source = 'IDE';
            } else if (uaLower.includes('antigravity/hub') || uaLower.includes('antigravityproxy-')) {
                source = 'Agent';
            } else {
                source = '未知';
            }
        }
        // Normalise previous "客户端" string to "Agent" just in case
        if (source === '客户端') {
            source = 'Agent';
        }
        p._resolvedSource = source;
    });

    // Apply current classification filter
    let filteredList = packetsList;
    if (currentFilter !== 'ALL') {
        filteredList = packetsList.filter(p => {
            if (currentFilter === 'CLI') return p._resolvedSource === 'CLI';
            if (currentFilter === 'IDE') return p._resolvedSource === 'IDE';
            if (currentFilter === 'Agent') return p._resolvedSource === 'Agent';
            if (currentFilter === 'UNKNOWN') return p._resolvedSource === '未知';
            return true;
        });
    }

    if (packetCountBadge) {
        packetCountBadge.textContent = state.currentLanguage === 'zh' ? `${filteredList.length} 个接口` : `${filteredList.length} packets`;
    }

    if (filteredList.length === 0) {
        packetListContainer.innerHTML = `<div class="text-center py-12 text-outline text-[13px]">${state.currentLanguage === 'zh' ? '暂无符合过滤条件的接口包' : 'No packets matching the filter.'}</div>`;
        if (packetDetailsPlaceholder) packetDetailsPlaceholder.classList.remove('hidden');
        if (packetDetailsContainer) packetDetailsContainer.classList.add('hidden');
        if (btnExportSinglePacket) btnExportSinglePacket.classList.add('hidden');
        selectedPacket = null;
        return;
    }

    // Render list items
    packetListContainer.innerHTML = filteredList.map(p => {
        const isSelected = selectedPacket && selectedPacket.id === p.id;
        const methodColor = p.method === 'POST' ? 'text-primary' : 'text-emerald-600';
        const selectedClass = isSelected ? 'bg-primary/10 border-primary/50 dark:bg-primary/20 dark:border-primary' : 'border-outline-variant/20 hover:bg-slate-50 dark:hover:bg-white/5';
        
        // Client source badge
        let sourceBadge = '';
        const source = p._resolvedSource || '未知';
        const displaySource = source === '未知' ? (state.currentLanguage === 'zh' ? '未知' : 'Unknown') : source;
        if (source === 'CLI') {
            sourceBadge = `<span class="px-1 py-0.5 rounded bg-blue-500/10 text-blue-600 dark:bg-blue-400/10 dark:text-blue-400 text-[9px] font-bold border border-blue-500/20">CLI</span>`;
        } else if (source === 'IDE') {
            sourceBadge = `<span class="px-1 py-0.5 rounded bg-emerald-500/10 text-emerald-600 dark:bg-emerald-400/10 dark:text-emerald-400 text-[9px] font-bold border border-emerald-500/20">IDE</span>`;
        } else if (source === 'Agent') {
            sourceBadge = `<span class="px-1 py-0.5 rounded bg-purple-500/10 text-purple-600 dark:bg-purple-400/10 dark:text-purple-400 text-[9px] font-bold border border-purple-500/20">Agent</span>`;
        } else {
            sourceBadge = `<span class="px-1 py-0.5 rounded bg-slate-500/10 text-slate-600 dark:bg-slate-400/10 dark:text-slate-400 text-[9px] font-bold border border-slate-500/20">${displaySource}</span>`;
        }

        return `
            <div class="p-3 border rounded-lg cursor-pointer transition-all flex flex-col gap-1.5 ${selectedClass}" onclick="window.selectPacketItem('${p.id}')">
                <div class="flex justify-between items-center">
                    <div class="flex items-center gap-1.5">
                        <span class="font-data-mono font-bold text-[12px] ${methodColor}">${p.method}</span>
                        ${sourceBadge}
                    </div>
                    <span class="text-[10px] text-outline font-medium">${p.timestamp}</span>
                </div>
                <div class="text-[12px] font-semibold text-slate-700 dark:text-slate-200 truncate break-all" title="${p.host}${p.path}">
                    ${p.path}
                </div>
                <div class="text-[10px] text-outline truncate">
                    ${p.host}
                </div>
            </div>
        `;
    }).join('');
}

// Select packet item
export function selectPacketItem(id: string) {
    selectedPacket = packetsList.find(p => p.id === id);
    refreshPacketsList();

    packetDetailsPlaceholder = document.getElementById('packetDetailsPlaceholder');
    packetDetailsContainer = document.getElementById('packetDetailsContainer');

    if (!selectedPacket) {
        if (packetDetailsPlaceholder) packetDetailsPlaceholder.classList.remove('hidden');
        if (packetDetailsContainer) packetDetailsContainer.classList.add('hidden');
        if (btnExportSinglePacket) btnExportSinglePacket.classList.add('hidden');
        return;
    }

    if (packetDetailsPlaceholder) packetDetailsPlaceholder.classList.add('hidden');
    if (packetDetailsContainer) packetDetailsContainer.classList.remove('hidden');
    if (btnExportSinglePacket) btnExportSinglePacket.classList.remove('hidden');

    // Fill elements
    selectedPacketMethod = document.getElementById('selectedPacketMethod');
    selectedPacketStatusCode = document.getElementById('selectedPacketStatusCode');
    selectedPacketUrl = document.getElementById('selectedPacketUrl');
    selectedPacketReqHeaders = document.getElementById('selectedPacketReqHeaders');
    selectedPacketReqBody = document.getElementById('selectedPacketReqBody');
    selectedPacketResHeaders = document.getElementById('selectedPacketResHeaders');
    selectedPacketResBody = document.getElementById('selectedPacketResBody');

    if (selectedPacketMethod) {
        selectedPacketMethod.classList.remove('hidden');
        selectedPacketMethod.textContent = selectedPacket.method;
    }
    if (selectedPacketStatusCode) {
        selectedPacketStatusCode.classList.remove('hidden');
        selectedPacketStatusCode.textContent = selectedPacket.statusCode;
        if (selectedPacketStatusCode.textContent && selectedPacketStatusCode.textContent.startsWith('2')) {
            selectedPacketStatusCode.className = 'font-bold px-2 py-0.5 text-[11px] rounded bg-emerald-50 text-emerald-600 dark:bg-emerald-950/30 dark:text-emerald-400';
        } else {
            selectedPacketStatusCode.className = 'font-bold px-2 py-0.5 text-[11px] rounded bg-rose-50 text-rose-600 dark:bg-rose-950/30 dark:text-rose-400';
        }
    }
    if (selectedPacketUrl) selectedPacketUrl.textContent = selectedPacket.url;
    if (selectedPacketReqHeaders) selectedPacketReqHeaders.textContent = JSON.stringify(selectedPacket.reqHeaders, null, 2);
    if (selectedPacketReqBody) selectedPacketReqBody.textContent = formatJsonText(selectedPacket.reqBody);
    if (selectedPacketResHeaders) selectedPacketResHeaders.textContent = JSON.stringify(selectedPacket.resHeaders, null, 2);
    if (selectedPacketResBody) selectedPacketResBody.textContent = formatJsonText(selectedPacket.resBody);
}

// Refresh drop-down select option list of active accounts for analysis
export function updateAnalyzeAccountSelect() {
    packetAnalyzeAccountSelect = document.getElementById('packetAnalyzeAccountSelect') as HTMLSelectElement | null;
    if (!packetAnalyzeAccountSelect) return;
    
    const enabledAccounts = (state.currentAccountsList || []).filter(a => a.enabled);
    const placeholder = `<option value="" data-i18n="packetSelectAccountPlaceholder">${state.currentLanguage === 'zh' ? '请选择分析账号...' : 'Please select an account for analysis...'}</option>`;
    
    if (enabledAccounts.length === 0) {
        const noAccText = state.currentLanguage === 'zh' ? '无可用账号 (请先在账号池登录/启用账号)' : 'No accounts available (Please login/enable accounts in pool first)';
        packetAnalyzeAccountSelect.innerHTML = placeholder + `<option value="" disabled>${noAccText}</option>`;
        return;
    }

    packetAnalyzeAccountSelect.innerHTML = placeholder + enabledAccounts.map(a => {
        const tierStr = a.tier ? ` [${a.tier}]` : '';
        return `<option value="${a.id}">${a.email}${tierStr}</option>`;
    }).join('');
}

// Initialize packets page bindings
export function initPacketsEvents() {
    btnClearPackets = document.getElementById('btnClearPackets') as HTMLButtonElement | null;
    btnStartPacketAnalyze = document.getElementById('btnStartPacketAnalyze') as HTMLButtonElement | null;
    btnDownloadPacketDoc = document.getElementById('btnDownloadPacketDoc') as HTMLButtonElement | null;
    packetDocPreviewContainer = document.getElementById('packetDocPreviewContainer');
    packetDocPreviewText = document.getElementById('packetDocPreviewText') as HTMLTextAreaElement | null;
    btnCopyGeneratedDoc = document.getElementById('btnCopyGeneratedDoc') as HTMLButtonElement | null;
    packetAnalyzeLoading = document.getElementById('packetAnalyzeLoading');
    packetAnalyzeProgressMsg = document.getElementById('packetAnalyzeProgressMsg');
    btnCopyReqBody = document.getElementById('btnCopyReqBody') as HTMLButtonElement | null;
    btnCopyResBody = document.getElementById('btnCopyResBody') as HTMLButtonElement | null;
    packetAnalyzeAccountSelect = document.getElementById('packetAnalyzeAccountSelect') as HTMLSelectElement | null;
    btnExportSinglePacket = document.getElementById('btnExportSinglePacket') as HTMLButtonElement | null;

    if (btnExportSinglePacket) {
        btnExportSinglePacket.addEventListener('click', async () => {
            if (!selectedPacket) {
                alert(state.currentLanguage === 'zh' ? '请先选择一条接口数据包进行导出' : 'Please select a packet to export first.');
                return;
            }
            try {
                const md = generateSinglePacketMarkdown(selectedPacket);
                const success = await ipcRenderer.invoke('packet:export-single', md, selectedPacket.method, selectedPacket.path);
                if (success) {
                    alert(state.currentLanguage === 'zh' ? '接口数据包已成功导出为 Markdown 文件！' : 'Packet successfully exported as Markdown!');
                }
            } catch (err: any) {
                alert((state.currentLanguage === 'zh' ? '导出失败: ' : 'Export failed: ') + err.message);
            }
        });
    }

    if (btnCopyGeneratedDoc) {
        btnCopyGeneratedDoc.addEventListener('click', () => {
            if (generatedDocContent) {
                navigator.clipboard.writeText(generatedDocContent).then(() => {
                    alert(state.currentLanguage === 'zh' ? '文档内容已复制到剪贴板！' : 'Document copied to clipboard!');
                }).catch(() => {
                    alert(state.currentLanguage === 'zh' ? '复制失败，请在文本框内手动全选复制。' : 'Copy failed, please copy manually.');
                });
            }
        });
    }

    if (btnCopyReqBody) {
        btnCopyReqBody.addEventListener('click', () => copyElementText('selectedPacketReqBody'));
    }
    if (btnCopyResBody) {
        btnCopyResBody.addEventListener('click', () => copyElementText('selectedPacketResBody'));
    }

    if (btnClearPackets) {
        btnClearPackets.addEventListener('click', async () => {
            const confirmMsg = state.currentLanguage === 'zh' ? '确定要清空所有已抓取的包吗？这不可恢复！' : 'Are you sure you want to clear all captured packets? This cannot be undone!';
            if (await $confirm(confirmMsg)) {
                ipcRenderer.send('packet:clear');
                selectedPacket = null;
                generatedDocContent = '';
                if (packetDocPreviewContainer) packetDocPreviewContainer.classList.add('hidden');
                if (btnDownloadPacketDoc) {
                    btnDownloadPacketDoc.disabled = true;
                }
                refreshPacketsList();
            }
        });
    }

    if (btnStartPacketAnalyze) {
        btnStartPacketAnalyze.addEventListener('click', async () => {
            // Filter packets matching currentFilter
            let filteredList = packetsList;
            if (currentFilter !== 'ALL') {
                filteredList = packetsList.filter(p => {
                    let source = p._resolvedSource || p.source;
                    if (!source) {
                        let ua = '';
                        if (p.reqHeaders) {
                            for (const key of Object.keys(p.reqHeaders)) {
                                if (key.toLowerCase() === 'user-agent') {
                                    ua = p.reqHeaders[key];
                                    break;
                                }
                            }
                        }
                        const uaLower = ua.toLowerCase();
                        if (uaLower.includes('antigravity/cli') || uaLower.includes('aidev_client')) {
                            source = 'CLI';
                        } else if (uaLower.includes('antigravity/ide') || uaLower.includes('cloudaicompanion') || uaLower.includes('google-api-nodejs-client') || uaLower.includes('go-http-client')) {
                            source = 'IDE';
                        } else if (uaLower.includes('antigravity/hub') || uaLower.includes('antigravityproxy-')) {
                            source = 'Agent';
                        } else {
                            source = '未知';
                        }
                    }
                    if (source === '客户端') {
                        source = 'Agent';
                    }
                    p._resolvedSource = source;

                    if (currentFilter === 'CLI') return p._resolvedSource === 'CLI';
                    if (currentFilter === 'IDE') return p._resolvedSource === 'IDE';
                    if (currentFilter === 'Agent') return p._resolvedSource === 'Agent';
                    if (currentFilter === 'UNKNOWN') return p._resolvedSource === '未知';
                    return true;
                });
            }

            if (filteredList.length === 0) {
                const filterNames: Record<string, string> = state.currentLanguage === 'zh' ? {
                    'ALL': '全部',
                    'CLI': 'CLI',
                    'IDE': 'IDE',
                    'Agent': 'Agent',
                    'UNKNOWN': '未知'
                } : {
                    'ALL': 'All',
                    'CLI': 'CLI',
                    'IDE': 'IDE',
                    'Agent': 'Agent',
                    'UNKNOWN': 'Unknown'
                };
                const filterLabel = filterNames[currentFilter] || currentFilter;
                alert(state.currentLanguage === 'zh' ? `当前筛选的 [${filterLabel}] 分类下没有已抓取的接口包！` : `No captured packets found under the selected category [${filterLabel}]!`);
                return;
            }

            if (!packetAnalyzeAccountSelect) return;
            const accId = packetAnalyzeAccountSelect.value;
            if (!accId) {
                alert(state.currentLanguage === 'zh' ? '请先选择一个用于分析的 AI 账号！' : 'Please select an AI account for analysis first!');
                return;
            }

            if (packetAnalyzeLoading) packetAnalyzeLoading.classList.remove('hidden');
            if (packetAnalyzeProgressMsg) packetAnalyzeProgressMsg.textContent = state.currentLanguage === 'zh' ? '正在连接 Gemini API 服务端...' : 'Connecting to Gemini API server...';

            try {
                if (packetAnalyzeProgressMsg) packetAnalyzeProgressMsg.textContent = state.currentLanguage === 'zh' ? '正在组织报文并调用 Gemini-2.5-Flash-Lite...' : 'Organizing packets and calling Gemini-2.5-Flash-Lite...';
                
                const markdown = await ipcRenderer.invoke('packet:analyze', accId, currentFilter);
                generatedDocContent = markdown;

                if (packetDocPreviewText) {
                    packetDocPreviewText.value = markdown;
                }
                if (packetDocPreviewContainer) {
                    packetDocPreviewContainer.classList.remove('hidden');
                    packetDocPreviewContainer.scrollIntoView({ behavior: 'smooth' });
                }

                if (btnDownloadPacketDoc) {
                    btnDownloadPacketDoc.disabled = false;
                }
                
                setTimeout(() => {
                    if (packetAnalyzeLoading) packetAnalyzeLoading.classList.add('hidden');
                }, 500);

            } catch (err: any) {
                if (packetAnalyzeLoading) packetAnalyzeLoading.classList.add('hidden');
                alert((state.currentLanguage === 'zh' ? '分析失败: ' : 'Analysis failed: ') + err.message);
            }
        });
    }

    if (btnDownloadPacketDoc) {
        btnDownloadPacketDoc.addEventListener('click', async () => {
            if (!generatedDocContent) {
                alert(state.currentLanguage === 'zh' ? '没有生成的文档可供下载' : 'No generated documentation to download.');
                return;
            }

            const success = await ipcRenderer.invoke('packet:download', generatedDocContent);
            if (success) {
                alert(state.currentLanguage === 'zh' ? '接口文档成功保存！' : 'API documentation saved successfully!');
            }
        });
    }

    // Bind Export Packet Log elements
    btnExportPacketLog = document.getElementById('btnExportPacketLog') as HTMLButtonElement | null;
    exportPacketsModal = document.getElementById('exportPacketsModal');
    exportPacketsModalCloseBtn = document.getElementById('exportPacketsModalCloseBtn') as HTMLButtonElement | null;
    btnExportPacketsCancel = document.getElementById('btnExportPacketsCancel') as HTMLButtonElement | null;
    btnExportPacketsConfirm = document.getElementById('btnExportPacketsConfirm') as HTMLButtonElement | null;
    exportPacketsTypeSelect = document.getElementById('exportPacketsTypeSelect') as HTMLSelectElement | null;

    const showExportPacketsModal = () => {
        if (!exportPacketsModal) return;
        exportPacketsModal.classList.remove('pointer-events-none', 'opacity-0');
        exportPacketsModal.classList.add('opacity-100');
        const container = exportPacketsModal.querySelector('#exportPacketsModalContainer');
        if (container) {
            container.classList.remove('scale-95');
            container.classList.add('scale-100');
        }
        // Set type select default value matching current filter
        if (exportPacketsTypeSelect) {
            exportPacketsTypeSelect.value = currentFilter;
        }
    };

    const hideExportPacketsModal = () => {
        if (!exportPacketsModal) return;
        exportPacketsModal.classList.add('pointer-events-none', 'opacity-0');
        exportPacketsModal.classList.remove('opacity-100');
        const container = exportPacketsModal.querySelector('#exportPacketsModalContainer');
        if (container) {
            container.classList.add('scale-95');
            container.classList.remove('scale-100');
        }
    };

    if (btnExportPacketLog) {
        btnExportPacketLog.addEventListener('click', () => {
            if (packetsList.length === 0) {
                alert(state.currentLanguage === 'zh' ? '当前没有已抓取的接口包，无法导出！' : 'No captured packets available, cannot export!');
                return;
            }
            showExportPacketsModal();
        });
    }

    if (exportPacketsModalCloseBtn) {
        exportPacketsModalCloseBtn.addEventListener('click', hideExportPacketsModal);
    }
    if (btnExportPacketsCancel) {
        btnExportPacketsCancel.addEventListener('click', hideExportPacketsModal);
    }

    if (exportPacketsModal) {
        exportPacketsModal.addEventListener('click', (e: MouseEvent) => {
            if (e.target === exportPacketsModal) {
                hideExportPacketsModal();
            }
        });
    }

    if (btnExportPacketsConfirm) {
        btnExportPacketsConfirm.addEventListener('click', async () => {
            if (!exportPacketsTypeSelect) return;
            const exportType = exportPacketsTypeSelect.value;
            
            // Re-resolve and filter packet list
            packetsList.forEach(p => {
                let source = p.source;
                if (!source) {
                    let ua = '';
                    if (p.reqHeaders) {
                        for (const key of Object.keys(p.reqHeaders)) {
                            if (key.toLowerCase() === 'user-agent') {
                                ua = p.reqHeaders[key];
                                break;
                            }
                        }
                    }
                    const uaLower = ua.toLowerCase();
                    if (uaLower.includes('antigravity/cli') || uaLower.includes('aidev_client')) {
                        source = 'CLI';
                    } else if (uaLower.includes('antigravity/ide') || uaLower.includes('cloudaicompanion') || uaLower.includes('google-api-nodejs-client') || uaLower.includes('go-http-client')) {
                        source = 'IDE';
                    } else if (uaLower.includes('antigravity/hub') || uaLower.includes('antigravityproxy-')) {
                        source = 'Agent';
                    } else {
                        source = '未知';
                    }
                }
                if (source === '客户端') {
                    source = 'Agent';
                }
                p._resolvedSource = source;
            });

            let filtered = packetsList;
            if (exportType !== 'ALL') {
                filtered = packetsList.filter(p => {
                    if (exportType === 'CLI') return p._resolvedSource === 'CLI';
                    if (exportType === 'IDE') return p._resolvedSource === 'IDE';
                    if (exportType === 'Agent') return p._resolvedSource === 'Agent';
                    if (exportType === 'UNKNOWN') return p._resolvedSource === '未知';
                    return true;
                });
            }

            if (filtered.length === 0) {
                const typeNames: Record<string, string> = state.currentLanguage === 'zh' ? {
                    'ALL': '全部',
                    'CLI': 'CLI',
                    'IDE': 'IDE',
                    'Agent': 'Agent',
                    'UNKNOWN': '未知'
                } : {
                    'ALL': 'All',
                    'CLI': 'CLI',
                    'IDE': 'IDE',
                    'Agent': 'Agent',
                    'UNKNOWN': 'Unknown'
                };
                alert(state.currentLanguage === 'zh' ? `当前选择的 [${typeNames[exportType] || exportType}] 分类下暂无抓取的接口包！` : `No captured packets found under the selected category [${typeNames[exportType] || exportType}]!`);
                return;
            }

            const dict = i18n[state.currentLanguage] || {};
            const isZH = state.currentLanguage === 'zh';

            // Generate Markdown document
            let md = `# ${dict.packetLogDocTitle || 'Antigravity Proxy 接口抓包日志'} (${exportType})\n\n`;
            md += `> **${dict.packetLogDocTime || '导出时间'}**: ${new Date().toLocaleString()}\n`;
            md += `> **${dict.packetLogDocTotal || '数据包总数'}**: ${filtered.length} ${isZH ? '个' : ''}\n\n`;
            
            md += `## ${dict.packetLogDocOverview || '接口列表概览'}\n\n`;
            md += `| ${isZH ? '序号' : 'No.'} | ${dict.packetLogDocSource || '来源'} | ${isZH ? '方法' : 'Method'} | ${dict.packetLogDocStatus || '状态码'} | ${isZH ? '主机' : 'Host'} | ${isZH ? '路径' : 'Path'} | ${dict.packetLogDocCaptured || '捕获时间'} |\n`;
            md += `| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n`;
            filtered.forEach((p, idx) => {
                const src = p._resolvedSource === '未知' ? (isZH ? '未知' : 'Unknown') : p._resolvedSource;
                md += `| ${idx + 1} | \`${src}\` | **${p.method}** | ${p.statusCode} | \`${p.host}\` | \`${p.path}\` | *${p.timestamp}* |\n`;
            });
            md += `\n---\n\n`;
            
            md += `## ${dict.packetLogDocDetails || '详细报文日志'}\n\n`;
            filtered.forEach((p, idx) => {
                md += `### [${isZH ? '接口' : 'Packet'} #${idx + 1}] ${p.method} ${p.path}\n\n`;
                md += `- **URL**: ${p.url}\n`;
                md += `- **${dict.packetLogDocHost || '主机 (Host)'}**: \`${p.host}\`\n`;
                const src = p._resolvedSource === '未知' ? (isZH ? '未知' : 'Unknown') : p._resolvedSource;
                md += `- **${dict.packetLogDocSource || '来源 (Source)'}**: \`${src}\`\n`;
                md += `- **${dict.packetLogDocStatus || '状态码 (Status)'}**: \`${p.statusCode}\`\n`;
                md += `- **${dict.packetLogDocCaptured || '捕获时间'}**: *${p.timestamp}*\n\n`;
                
                md += `#### ${isZH ? '请求 Headers' : 'Request Headers'}\n`;
                md += `\`\`\`json\n${JSON.stringify(p.reqHeaders, null, 2)}\n\`\`\`\n\n`;
                
                md += `#### ${isZH ? '请求 Body' : 'Request Body'}\n`;
                if (p.reqBody) {
                    md += `\`\`\`json\n${formatJsonText(p.reqBody)}\n\`\`\`\n\n`;
                } else {
                    md += `*${dict.packetLogDocNoBody || '无请求 Body'}*\n\n`;
                }
                
                md += `#### ${isZH ? '响应 Headers' : 'Response Headers'}\n`;
                md += `\`\`\`json\n${JSON.stringify(p.resHeaders, null, 2)}\n\`\`\`\n\n`;
                
                md += `#### ${isZH ? '响应 Body' : 'Response Body'}\n`;
                if (p.resBody) {
                    md += `\`\`\`json\n${formatJsonText(p.resBody)}\n\`\`\`\n\n`;
                } else {
                    md += `*${dict.packetLogDocNoResBody || '无响应 Body'}*\n\n`;
                }
                md += `\n---\n\n`;
            });

            try {
                // Invoke backend download log
                const success = await ipcRenderer.invoke('packet:export-log', md, exportType);
                if (success) {
                    alert(state.currentLanguage === 'zh' ? '接口日志成功导出并保存！' : 'Interface logs exported and saved successfully!');
                    hideExportPacketsModal();
                }
            } catch (err: any) {
                alert((state.currentLanguage === 'zh' ? '导出失败: ' : 'Export failed: ') + err.message);
            }
        });
    }
}

export function setPacketFilter(filter: string) {
    currentFilter = filter;
    
    // Update active filter button styles
    const filters = ['ALL', 'CLI', 'IDE', 'Agent', 'UNKNOWN'];
    const ids = {
        'ALL': 'btnFilterPacketAll',
        'CLI': 'btnFilterPacketCli',
        'IDE': 'btnFilterPacketIde',
        'Agent': 'btnFilterPacketAgent',
        'UNKNOWN': 'btnFilterPacketUnknown'
    };
    
    for (const f of filters) {
        const el = document.getElementById(ids[f as keyof typeof ids]);
        if (el) {
            if (f === filter) {
                el.className = 'px-2 py-0.5 font-bold rounded bg-primary text-white transition-colors cursor-pointer';
            } else {
                el.className = 'px-2 py-0.5 font-bold rounded bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-slate-600 dark:text-slate-300 transition-colors cursor-pointer';
            }
        }
    }
    
    refreshPacketsList();
}

// Register global hooks
(window as any).copyElementText = copyElementText;
(window as any).refreshPacketsList = refreshPacketsList;
(window as any).selectPacketItem = selectPacketItem;
(window as any).updateAnalyzeAccountSelect = updateAnalyzeAccountSelect;
(window as any).setPacketFilter = setPacketFilter;

// Register shared callbacks
state.callbacks.refreshPacketsList = refreshPacketsList;
state.callbacks.updateAnalyzeAccountSelect = updateAnalyzeAccountSelect;
