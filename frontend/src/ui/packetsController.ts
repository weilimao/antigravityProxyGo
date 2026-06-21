import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

let packetsList: any[] = [];
let selectedPacket: any = null;
let generatedDocContent = '';

// DOM Elements
let packetListContainer: HTMLElement | null;
let packetCountBadge: HTMLElement | null;
let packetDetailsPlaceholder: HTMLElement | null;
let packetDetailsContainer: HTMLElement | null;
let btnClearPackets: HTMLButtonElement | null;

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

// Clipboard helper
export function copyElementText(elementId: string) {
    const el = document.getElementById(elementId) as any;
    if (!el) return;
    const text = el.textContent || el.value;
    if (!text) {
        alert('没有可以复制的内容');
        return;
    }
    navigator.clipboard.writeText(text).then(() => {
        alert('复制成功！');
    }).catch(() => {
        try {
            el.select();
            document.execCommand('copy');
            alert('复制成功！');
        } catch (e) {
            alert('复制失败，请手动选择复制。');
        }
    });
}

// JSON styling helper
export function formatJsonText(text: any): string {
    if (!text) return '';
    if (typeof text === 'object') return JSON.stringify(text, null, 2);
    try {
        return JSON.stringify(JSON.parse(text), null, 2);
    } catch (e) {
        return text;
    }
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
    } catch (e) {
        console.error('Failed to get packets:', e);
        packetsList = [];
    }

    if (packetCountBadge) {
        packetCountBadge.textContent = `${packetsList.length} 个接口`;
    }

    if (packetsList.length === 0) {
        packetListContainer.innerHTML = `<div class="text-center py-12 text-outline text-[13px]">暂无已抓取的接口包</div>`;
        if (packetDetailsPlaceholder) packetDetailsPlaceholder.classList.remove('hidden');
        if (packetDetailsContainer) packetDetailsContainer.classList.add('hidden');
        selectedPacket = null;
        return;
    }

    // Render list items
    packetListContainer.innerHTML = packetsList.map(p => {
        const isSelected = selectedPacket && selectedPacket.id === p.id;
        const methodColor = p.method === 'POST' ? 'text-primary' : 'text-emerald-600';
        const selectedClass = isSelected ? 'bg-primary/10 border-primary/50 dark:bg-primary/20 dark:border-primary' : 'border-outline-variant/20 hover:bg-slate-50 dark:hover:bg-white/5';
        
        return `
            <div class="p-3 border rounded-lg cursor-pointer transition-all flex flex-col gap-1.5 ${selectedClass}" onclick="window.selectPacketItem('${p.id}')">
                <div class="flex justify-between items-center">
                    <span class="font-data-mono font-bold text-[12px] ${methodColor}">${p.method}</span>
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
        return;
    }

    if (packetDetailsPlaceholder) packetDetailsPlaceholder.classList.add('hidden');
    if (packetDetailsContainer) packetDetailsContainer.classList.remove('hidden');

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
    const placeholder = `<option value="">请选择分析账号...</option>`;
    
    if (enabledAccounts.length === 0) {
        packetAnalyzeAccountSelect.innerHTML = placeholder + `<option value="" disabled>无可用账号 (请先在账号池登录/启用账号)</option>`;
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

    if (btnCopyGeneratedDoc) {
        btnCopyGeneratedDoc.addEventListener('click', () => {
            if (generatedDocContent) {
                navigator.clipboard.writeText(generatedDocContent).then(() => {
                    alert('文档内容已复制到剪贴板！');
                }).catch(() => {
                    alert('复制失败，请在文本框内手动全选复制。');
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
        btnClearPackets.addEventListener('click', () => {
            if (confirm('确定要清空所有已抓取的包吗？这不可恢复！')) {
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
            if (packetsList.length === 0) {
                alert('当前没有已抓取的接口包！请先让 IDE 发起请求拦截。');
                return;
            }

            if (!packetAnalyzeAccountSelect) return;
            const accId = packetAnalyzeAccountSelect.value;
            if (!accId) {
                alert('请先选择一个用于分析的 AI 账号！');
                return;
            }

            if (packetAnalyzeLoading) packetAnalyzeLoading.classList.remove('hidden');
            if (packetAnalyzeProgressMsg) packetAnalyzeProgressMsg.textContent = '正在连接 Gemini API 服务端...';

            try {
                if (packetAnalyzeProgressMsg) packetAnalyzeProgressMsg.textContent = '正在组织报文并调用 Gemini-2.5-Flash-Lite...';
                
                const markdown = await ipcRenderer.invoke('packet:analyze', accId);
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
                alert(`分析失败: ${err.message}`);
            }
        });
    }

    if (btnDownloadPacketDoc) {
        btnDownloadPacketDoc.addEventListener('click', async () => {
            if (!generatedDocContent) {
                alert('没有生成的文档可供下载');
                return;
            }

            const success = await ipcRenderer.invoke('packet:download', generatedDocContent);
            if (success) {
                alert('接口文档成功保存！');
            }
        });
    }
}

// Register global hooks
(window as any).copyElementText = copyElementText;
(window as any).refreshPacketsList = refreshPacketsList;
(window as any).selectPacketItem = selectPacketItem;
(window as any).updateAnalyzeAccountSelect = updateAnalyzeAccountSelect;

// Register shared callbacks
state.callbacks.refreshPacketsList = refreshPacketsList;
state.callbacks.updateAnalyzeAccountSelect = updateAnalyzeAccountSelect;
