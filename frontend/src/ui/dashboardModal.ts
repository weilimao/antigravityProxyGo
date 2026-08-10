/**
 * dashboardModal.ts: 请求详情弹窗(自包含 18 个 modal* let + modalDetailsToken)。
 *
 * 从 dashboard.ts 抽离:声明段(原 L61-81)+ initDashboardEvents 体内 modal 取值/复制监听段(原 L490-536,deindent 4→0,
 * 包进 initModalDom)+ hideModal/formatRequestBody/formatRequestHeaders/showModal 函数段(原 L1220-1337)。
 * 18 modal* let + modalDetailsToken 仅在 initModalDom / showModal(懒取)/ hideModal / ++modalDetailsToken 再赋值,全随簇迁出,自包含。
 * hub initDashboardEvents 调 initModalDom();点击委托 + (window).showModal/hideModal 赋值留 hub,经 import 解析。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { formatDuration } from './dashboardUtils';

// Details Modal Elements
let detailsModal: HTMLElement | null = null;
let modalContainer: HTMLElement | null = null;
let modalCloseBtn: HTMLElement | null = null;
let modalCloseBtnSecondary: HTMLElement | null = null;
let modalCopyBtn: HTMLElement | null = null;
let modalCopyHeadersBtn: HTMLElement | null = null;

let modalTime: HTMLElement | null = null;
let modalSession: HTMLElement | null = null;
let modalModel: HTMLElement | null = null;
let modalPath: HTMLElement | null = null;
let modalTokens: HTMLElement | null = null;
let modalStatus: HTMLElement | null = null;
let modalCost: HTMLElement | null = null;
let modalAccount: HTMLElement | null = null;
let modalAccountWrapper: HTMLElement | null = null;
let modalFirstByte: HTMLElement | null = null;
let modalDuration: HTMLElement | null = null;
let modalJsonArea: HTMLElement | null = null;
let modalHeaderArea: HTMLElement | null = null;

export function initModalDom() {
detailsModal = document.getElementById('detailsModal');
modalContainer = document.getElementById('modalContainer');
modalCloseBtn = document.getElementById('modalCloseBtn');
modalCloseBtnSecondary = document.getElementById('modalCloseBtnSecondary');
modalCopyBtn = document.getElementById('modalCopyBtn');
modalCopyHeadersBtn = document.getElementById('modalCopyHeadersBtn');

modalTime = document.getElementById('modalTime');
modalSession = document.getElementById('modalSession');
modalModel = document.getElementById('modalModel');
modalPath = document.getElementById('modalPath');
modalTokens = document.getElementById('modalTokens');
modalStatus = document.getElementById('modalStatus');
modalCost = document.getElementById('modalCost');
modalAccount = document.getElementById('modalAccount');
modalAccountWrapper = document.getElementById('modalAccountWrapper');
modalFirstByte = document.getElementById('modalFirstByte');
modalDuration = document.getElementById('modalDuration');
modalJsonArea = document.getElementById('modalJsonArea');
modalHeaderArea = document.getElementById('modalHeaderArea');

if (modalCloseBtn) modalCloseBtn.addEventListener('click', hideModal);
if (modalCloseBtnSecondary) modalCloseBtnSecondary.addEventListener('click', hideModal);
if (modalCopyHeadersBtn) {
    modalCopyHeadersBtn.addEventListener('click', () => {
        const textToCopy = modalHeaderArea?.textContent || '';
        navigator.clipboard.writeText(textToCopy).then(() => {
            const span = modalCopyHeadersBtn!.querySelector('span:not(.material-symbols-outlined)');
            if (span) {
                span.textContent = state.currentLanguage === 'zh' ? '已复制！' : 'Copied!';
                setTimeout(() => { span.textContent = state.currentLanguage === 'zh' ? '复制' : 'Copy'; }, 1500);
            }
        });
    });
}

if (modalCopyBtn) {
    modalCopyBtn.addEventListener('click', () => {
        const textToCopy = modalJsonArea?.textContent || '';
        navigator.clipboard.writeText(textToCopy).then(() => {
            const span = modalCopyBtn!.querySelector('span:not(.material-symbols-outlined)');
            if (span) {
                span.textContent = state.currentLanguage === 'zh' ? '已复制！' : 'Copied!';
                setTimeout(() => { span.textContent = state.currentLanguage === 'zh' ? '复制 JSON' : 'Copy JSON'; }, 1500);
            }
        });
    });
}}

export function hideModal() {
    if (!detailsModal || !modalContainer) return;
    detailsModal.classList.add('opacity-0', 'pointer-events-none');
    modalContainer.classList.add('scale-95');
    modalContainer.classList.remove('scale-100');

    // Invalidate any in-flight on-demand details fetch and release the large
    // API request/response text from the DOM tree immediately.
    modalDetailsToken++;
    if (modalJsonArea) modalJsonArea.textContent = '';
    if (modalHeaderArea) modalHeaderArea.textContent = '';
}

// Bumped on every showModal/hideModal so stale on-demand details fetches
// (for a previous entry, or after close) can be discarded.
let modalDetailsToken = 0;

function formatRequestBody(body: any): string {
    if (!body) {
        return state.currentLanguage === 'zh' ? '{\n  "message": "无请求参数"\n}' : '{\n  "message": "No request parameters"\n}';
    }
    try {
        if (typeof body === 'object') {
            return JSON.stringify(body, null, 2);
        }
        const parsed = JSON.parse(body);
        return JSON.stringify(parsed, null, 2);
    } catch (e) {
        return String(body);
    }
}

function formatRequestHeaders(headers: any): string {
    if (!headers) {
        return state.currentLanguage === 'zh' ? '{\n  "message": "无请求头数据"\n}' : '{\n  "message": "No request headers"\n}';
    }
    try {
        return JSON.stringify(headers, null, 2);
    } catch (e) {
        return String(headers);
    }
}

export function showModal(log: any) {
    if (!detailsModal || !modalContainer) {
        detailsModal = document.getElementById('detailsModal');
        modalContainer = document.getElementById('modalContainer');
        modalCloseBtn = document.getElementById('modalCloseBtn');
        modalCloseBtnSecondary = document.getElementById('modalCloseBtnSecondary');
        modalCopyBtn = document.getElementById('modalCopyBtn');
        modalCopyHeadersBtn = document.getElementById('modalCopyHeadersBtn');

        modalTime = document.getElementById('modalTime');
        modalSession = document.getElementById('modalSession');
        modalModel = document.getElementById('modalModel');
        modalPath = document.getElementById('modalPath');
        modalTokens = document.getElementById('modalTokens');
        modalStatus = document.getElementById('modalStatus');
        modalCost = document.getElementById('modalCost');
        modalAccount = document.getElementById('modalAccount');
        modalAccountWrapper = document.getElementById('modalAccountWrapper');
        modalFirstByte = document.getElementById('modalFirstByte');
        modalDuration = document.getElementById('modalDuration');
        modalJsonArea = document.getElementById('modalJsonArea');
        modalHeaderArea = document.getElementById('modalHeaderArea');
    }
    if (!detailsModal || !modalContainer) return;

    // Header fields come from the lite log metadata (always present on the
    // stats-updated hot path).
    if (modalTime) modalTime.textContent = log.timestamp || '-';
    if (modalSession) modalSession.textContent = log.sessionId || '-';
    if (modalModel) modalModel.textContent = log.model || '-';
    if (modalPath) modalPath.textContent = `${log.method || 'POST'} ${log.host || ''}${log.path || ''}`;
    if (modalFirstByte) modalFirstByte.textContent = formatDuration(log.firstByteMs);
    if (modalDuration) modalDuration.textContent = formatDuration(log.durationMs);
    if (modalCost) modalCost.textContent = `$${(log.cost || 0).toFixed(6)}`;

    if (log.account) {
        if (modalAccountWrapper) modalAccountWrapper.classList.remove('hidden');
        if (modalAccount) modalAccount.textContent = log.account;
    } else {
        if (modalAccountWrapper) modalAccountWrapper.classList.add('hidden');
    }

    const inT = log.inTokens || 0;
    const outT = log.outTokens || 0;
    const cachedT = log.cachedTokens || 0;
    if (modalTokens) modalTokens.textContent = `In: ${inT.toLocaleString()} | Out: ${outT.toLocaleString()} | Cache: ${cachedT.toLocaleString()}`;

    const cacheBadge = log.cacheStatus || 'NONE';
    const statusColor = log.statusCode >= 400 ? 'text-rose-500' : 'text-emerald-500';
    if (modalStatus) modalStatus.innerHTML = `<span class="text-primary dark:text-primary-fixed-dim mr-2">${cacheBadge}</span><span class="${statusColor}">HTTP ${log.statusCode}</span>`;

    // Show the modal immediately with a loading placeholder; the heavy
    // requestBody / requestHeaders are fetched on demand so they are not
    // carried on every stats-updated tick.
    const loadingText = state.currentLanguage === 'zh' ? '加载中…' : 'Loading…';
    if (modalJsonArea) modalJsonArea.textContent = loadingText;
    if (modalHeaderArea) modalHeaderArea.textContent = loadingText;

    detailsModal.classList.remove('opacity-0', 'pointer-events-none');
    modalContainer.classList.remove('scale-95');
    modalContainer.classList.add('scale-100');

    const token = ++modalDetailsToken;
    ipcRenderer.invoke('request:get-details', log.id).then((details: any) => {
        if (token !== modalDetailsToken) return; // superseded by a newer open/close
        const body = details ? details.requestBody : null;
        const headers = details ? details.requestHeaders : null;
        if (modalJsonArea) modalJsonArea.textContent = formatRequestBody(body);
        if (modalHeaderArea) modalHeaderArea.textContent = formatRequestHeaders(headers);
    }).catch(() => {
        if (token !== modalDetailsToken) return;
        if (modalJsonArea) modalJsonArea.textContent = formatRequestBody(null);
        if (modalHeaderArea) modalHeaderArea.textContent = formatRequestHeaders(null);
    });
}
