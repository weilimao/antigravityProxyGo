import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

// DOM Elements
let pricingTableBody: HTMLElement | null;
let pricingModal: HTMLElement | null;
let pricingModalContainer: HTMLElement | null;
let pricingModalTitle: HTMLElement | null;
let pricingModalCloseBtn: HTMLElement | null;
let pricingModalCancelBtn: HTMLElement | null;
let pricingModalSaveBtn: HTMLElement | null;

let pricingModelName: HTMLInputElement | null;
let pricingInputVal: HTMLInputElement | null;
let pricingOutputVal: HTMLInputElement | null;
let pricingCachedVal: HTMLInputElement | null;
let pricingOrigKey: HTMLInputElement | null;

export function fetchPricing() {
    ipcRenderer.send('get-pricing');
}

export function renderPricingTable() {
    pricingTableBody = document.querySelector('#pricingTable tbody');
    if (!pricingTableBody) return;
    pricingTableBody.innerHTML = '';
    
    const list = Object.entries(state.pricingConfig);
    
    if (list.length === 0) {
        pricingTableBody.innerHTML = `<tr><td colspan="5" class="p-6 text-center text-outline font-bold">暂无模型配置</td></tr>`;
        return;
    }
    
    list.forEach(([key, val]) => {
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50/50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';
        
        const isUnknown = key === 'unknown';
        const modelLabel = isUnknown ? '<span class="text-outline">默认回退模型 (unknown)</span>' : key;
        
        tr.innerHTML = `
            <td class="p-3 pl-5 font-bold text-on-surface dark:text-white flex items-center gap-2 h-12">
                <span class="material-symbols-outlined text-[16px] text-primary">analytics</span>
                <span>${modelLabel}</span>
            </td>
            <td class="p-3 text-right text-slate-600 dark:text-slate-300 font-data-mono">$${val.input.toFixed(6)}</td>
            <td class="p-3 text-right text-slate-600 dark:text-slate-300 font-data-mono">$${val.output.toFixed(6)}</td>
            <td class="p-3 text-right text-slate-600 dark:text-slate-300 font-data-mono">$${val.cached.toFixed(6)}</td>
            <td class="p-3 text-center">
                <div class="flex justify-center gap-2">
                    <button class="btn-edit-pricing text-primary hover:underline text-[12px] font-bold" data-key="${key}">编辑</button>
                    ${isUnknown ? '' : `<button class="btn-delete-pricing text-red-500 hover:underline text-[12px] font-bold" data-key="${key}">删除</button>`}
                </div>
            </td>
        `;
        pricingTableBody!.appendChild(tr);
    });
    
    // Bind buttons in table
    pricingTableBody.querySelectorAll('.btn-edit-pricing').forEach(btn => {
        btn.addEventListener('click', () => {
            const key = btn.getAttribute('data-key');
            if (key) {
                showPricingModal(key, state.pricingConfig[key]);
            }
        });
    });
    
    pricingTableBody.querySelectorAll('.btn-delete-pricing').forEach(btn => {
        btn.addEventListener('click', () => {
            const key = btn.getAttribute('data-key');
            if (key) {
                if (confirm(`确定要删除模型 "${key}" 的自定义计费配置吗？`)) {
                    ipcRenderer.send('delete-pricing', key);
                }
            }
        });
    });
}

export function showPricingModal(modelKey = '', pricingData: any = null) {
    pricingModal = document.getElementById('pricingModal');
    pricingModalContainer = document.getElementById('pricingModalContainer');
    pricingModalTitle = document.getElementById('pricingModalTitle');
    pricingOrigKey = document.getElementById('pricingOrigKey') as HTMLInputElement | null;
    pricingModelName = document.getElementById('pricingModelName') as HTMLInputElement | null;
    pricingInputVal = document.getElementById('pricingInputVal') as HTMLInputElement | null;
    pricingOutputVal = document.getElementById('pricingOutputVal') as HTMLInputElement | null;
    pricingCachedVal = document.getElementById('pricingCachedVal') as HTMLInputElement | null;

    if (!pricingModal || !pricingModalContainer || !pricingModalTitle || !pricingOrigKey || !pricingModelName || !pricingInputVal || !pricingOutputVal || !pricingCachedVal) return;

    if (modelKey) {
        pricingModalTitle.textContent = '编辑模型计费配置';
        pricingOrigKey.value = modelKey;
        pricingModelName.value = modelKey;
        if (modelKey === 'unknown') {
            pricingModelName.disabled = true;
        } else {
            pricingModelName.disabled = false;
        }
        
        pricingInputVal.value = pricingData.input;
        pricingOutputVal.value = pricingData.output;
        pricingCachedVal.value = pricingData.cached;
    } else {
        pricingModalTitle.textContent = '新增模型计费配置';
        pricingOrigKey.value = '';
        pricingModelName.value = '';
        pricingModelName.disabled = false;
        
        pricingInputVal.value = '0.0';
        pricingOutputVal.value = '0.0';
        pricingCachedVal.value = '0.0';
    }
    
    pricingModal.classList.remove('opacity-0', 'pointer-events-none');
    pricingModalContainer.classList.remove('scale-95');
    pricingModalContainer.classList.add('scale-100');
}

export function hidePricingModal() {
    pricingModal = document.getElementById('pricingModal');
    pricingModalContainer = document.getElementById('pricingModalContainer');
    if (!pricingModal || !pricingModalContainer) return;
    pricingModal.classList.add('opacity-0', 'pointer-events-none');
    pricingModalContainer.classList.add('scale-95');
    pricingModalContainer.classList.remove('scale-100');
}

export function initPricingEvents() {
    const btnResetPricing = document.getElementById('btnResetPricing');
    const btnAddPricing = document.getElementById('btnAddPricing');
    pricingModalCloseBtn = document.getElementById('pricingModalCloseBtn');
    pricingModalCancelBtn = document.getElementById('pricingModalCancelBtn');
    pricingModalSaveBtn = document.getElementById('pricingModalSaveBtn');

    pricingModelName = document.getElementById('pricingModelName') as HTMLInputElement | null;
    pricingInputVal = document.getElementById('pricingInputVal') as HTMLInputElement | null;
    pricingOutputVal = document.getElementById('pricingOutputVal') as HTMLInputElement | null;
    pricingCachedVal = document.getElementById('pricingCachedVal') as HTMLInputElement | null;
    pricingOrigKey = document.getElementById('pricingOrigKey') as HTMLInputElement | null;
    
    if (btnAddPricing) {
        btnAddPricing.addEventListener('click', () => showPricingModal());
    }
    
    if (btnResetPricing) {
        btnResetPricing.addEventListener('click', () => {
            if (confirm('确定要恢复默认的模型计费配置吗？这会清除所有自定义修改！')) {
                ipcRenderer.send('reset-pricing');
            }
        });
    }
    
    // Modal buttons
    if (pricingModalCloseBtn) pricingModalCloseBtn.addEventListener('click', hidePricingModal);
    if (pricingModalCancelBtn) pricingModalCancelBtn.addEventListener('click', hidePricingModal);
    
    if (pricingModalSaveBtn) {
        pricingModalSaveBtn.addEventListener('click', (e) => {
            e.preventDefault();
            if (!pricingModelName || !pricingInputVal || !pricingOutputVal || !pricingCachedVal || !pricingOrigKey) return;
            
            const modelName = pricingModelName.value.trim().toLowerCase();
            if (!modelName) {
                alert('请输入模型匹配名称');
                return;
            }
            
            const inputVal = parseFloat(pricingInputVal.value);
            const outputVal = parseFloat(pricingOutputVal.value);
            const cachedVal = parseFloat(pricingCachedVal.value);
            
            if (isNaN(inputVal) || isNaN(outputVal) || isNaN(cachedVal) || inputVal < 0 || outputVal < 0 || cachedVal < 0) {
                alert('请输入有效的正数价格');
                return;
            }
            
            const origKey = pricingOrigKey.value;
            if (origKey && origKey !== modelName) {
                ipcRenderer.send('delete-pricing', origKey);
            }
            
            ipcRenderer.send('update-pricing', modelName, {
                input: inputVal,
                output: outputVal,
                cached: cachedVal
            });
            
            hidePricingModal();
        });
    }
    
    // Register IPC reply handler
    ipcRenderer.on('get-pricing-res', (event: any, pricing: any) => {
        state.pricingConfig = pricing;
        renderPricingTable();
    });
    
    // Initial fetch
    fetchPricing();
}

// Register shared callbacks
state.callbacks.fetchPricing = fetchPricing;
