// Custom dialog system to replace native alert and confirm with polished frosted glass UI

import state from '../ui/dashboardState';
import i18n from './i18n';

declare global {
    interface Window {
        $confirm: (message: string) => Promise<boolean>;
    }
    // Declare global $confirm for direct calling
    const $confirm: (message: string) => Promise<boolean>;
}

export function initCustomDialog() {
    // Inject dynamic CSS keyframes and classes for smooth animation
    if (!document.getElementById('custom-dialog-styles')) {
        const css = `
            @keyframes customDialogFadeIn {
                from { opacity: 0; }
                to { opacity: 1; }
            }
            @keyframes customDialogFadeOut {
                from { opacity: 1; }
                to { opacity: 0; }
            }
            @keyframes customDialogSlideIn {
                from {
                    transform: translate3d(-50%, -14px, 0) scale(0.97);
                    opacity: 0;
                }
                to {
                    transform: translate3d(-50%, 0, 0) scale(1);
                    opacity: 1;
                }
            }
            @keyframes customDialogSlideOut {
                from {
                    transform: translate3d(-50%, 0, 0) scale(1);
                    opacity: 1;
                }
                to {
                    transform: translate3d(-50%, -10px, 0) scale(0.97);
                    opacity: 0;
                }
            }
            
            .dialog-overlay-in {
                animation: customDialogFadeIn 0.14s cubic-bezier(0.16, 1, 0.3, 1) forwards;
                will-change: opacity;
            }
            .dialog-overlay-out {
                animation: customDialogFadeOut 0.1s cubic-bezier(0.16, 1, 0.3, 1) forwards;
                will-change: opacity;
            }
            .dialog-box-in {
                animation: customDialogSlideIn 0.14s cubic-bezier(0.16, 1, 0.3, 1) forwards;
                will-change: transform, opacity;
                contain: layout style paint;
            }
            .dialog-box-out {
                animation: customDialogSlideOut 0.1s cubic-bezier(0.16, 1, 0.3, 1) forwards;
                will-change: transform, opacity;
                contain: layout style paint;
            }
        `;
        const styleEl = document.createElement('style');
        styleEl.id = 'custom-dialog-styles';
        styleEl.textContent = css;
        document.head.appendChild(styleEl);
    }

    // Rewrite global alert
    (window as any).alert = function (message: string): void {
        showDialog({ message, type: 'alert' });
    };

    // Bind custom global $confirm
    (window as any).$confirm = function (message: string): Promise<boolean> {
        return showDialog({ message, type: 'confirm' });
    };
}

interface DialogOptions {
    message: string;
    type: 'alert' | 'confirm';
}

function showDialog(options: DialogOptions): Promise<boolean> {
    return new Promise((resolve) => {
        // 1. 纯轻量半透明遮罩：禁用全屏 backdrop-blur 与 transition-all，避免 WebView2 整屏模糊卡顿
        const overlay = document.createElement('div');
        overlay.className = 'fixed inset-0 z-[9999] bg-slate-950/35 dark:bg-black/55 dialog-overlay-in';

        // 2. 高对比纯净实体卡片：纯 GPU transform/opacity 合成动画，零模糊采样开销
        const dialog = document.createElement('div');
        dialog.className = 'fixed top-14 left-1/2 z-[10000] w-[90vw] max-w-[420px] rounded-2xl border border-slate-200/80 dark:border-white/10 bg-white dark:bg-[#1a1f30] shadow-2xl p-6 text-slate-800 dark:text-slate-100 flex flex-col gap-4 dialog-box-in';

        const isConfirm = options.type === 'confirm';
        const icon = isConfirm ? 'help' : 'info';
        const iconColorClass = isConfirm ? 'text-primary dark:text-primary-fixed-dim' : 'text-amber-500';

        const dict = i18n[state.currentLanguage] || i18n.zh;
        const confirmTitle = isConfirm 
            ? (dict.otp_dialogConfirmTitle || '确认提示') 
            : (dict.otp_dialogSystemTitle || '系统提示');
        const dialogOkText = dict.confirm || '确定';
        const dialogCancelText = dict.cancel || '取消';

        dialog.innerHTML = `
            <div class="flex items-start gap-4">
                <span class="material-symbols-outlined text-[24px] ${iconColorClass} shrink-0 mt-0.5 select-none">${icon}</span>
                <div class="flex flex-col gap-1.5 min-w-0 flex-1">
                    <h3 class="text-[15px] font-bold tracking-wide select-none">${confirmTitle}</h3>
                    <p id="custom-dialog-message" class="text-[13.5px] leading-relaxed text-slate-600 dark:text-slate-300 break-words whitespace-pre-wrap select-text"></p>
                </div>
            </div>
            <div class="flex justify-end gap-3.5 mt-2">
                ${isConfirm ? `
                    <button id="custom-dialog-cancel" class="px-4 py-2 rounded-xl text-slate-600 dark:text-slate-400 bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 border border-slate-200/60 dark:border-white/5 transition-colors duration-100 text-[13px] font-medium select-none cursor-pointer">
                        ${dialogCancelText}
                    </button>
                ` : ''}
                <button id="custom-dialog-ok" class="px-5 py-2 rounded-xl bg-primary text-white hover:bg-primary/90 active:bg-primary/95 shadow-md shadow-primary/10 transition-colors duration-100 text-[13px] font-medium select-none cursor-pointer">
                  ${dialogOkText}
                </button>
            </div>
        `;

        const msgEl = dialog.querySelector('#custom-dialog-message');
        if (msgEl) {
            msgEl.textContent = options.message;
        }

        document.body.appendChild(overlay);
        document.body.appendChild(dialog);

        const btnOk = dialog.querySelector('#custom-dialog-ok') as HTMLButtonElement;
        const btnCancel = dialog.querySelector('#custom-dialog-cancel') as HTMLButtonElement | null;

        if (btnOk) {
            requestAnimationFrame(() => btnOk.focus({ preventScroll: true }));
        }

        let isClosing = false;
        const close = (result: boolean) => {
            if (isClosing) return;
            isClosing = true;
            cleanupEvents();

            overlay.classList.remove('dialog-overlay-in');
            overlay.classList.add('dialog-overlay-out');
            dialog.classList.remove('dialog-box-in');
            dialog.classList.add('dialog-box-out');

            setTimeout(() => {
                overlay.remove();
                dialog.remove();
                resolve(result);
            }, 100);
        };

        const handleOk = () => close(true);
        const handleCancel = () => close(false);

        const handleKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Enter') {
                e.preventDefault();
                handleOk();
            } else if (e.key === 'Escape') {
                e.preventDefault();
                handleCancel();
            }
        };

        btnOk.addEventListener('click', handleOk);
        if (btnCancel) {
            btnCancel.addEventListener('click', handleCancel);
        }
        // Click backdrop also cancels confirm or acknowledges alert
        overlay.addEventListener('click', isConfirm ? handleCancel : handleOk);
        document.addEventListener('keydown', handleKeyDown);

        function cleanupEvents() {
            btnOk.removeEventListener('click', handleOk);
            if (btnCancel) {
                btnCancel.removeEventListener('click', handleCancel);
            }
            overlay.removeEventListener('click', isConfirm ? handleCancel : handleOk);
            document.removeEventListener('keydown', handleKeyDown);
        }
    });
}
