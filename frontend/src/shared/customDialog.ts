// Custom dialog system to replace native alert and confirm with polished frosted glass UI

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
                    transform: translate(-50%, -24px) scale(0.95);
                    opacity: 0;
                }
                to {
                    transform: translate(-50%, 0) scale(1);
                    opacity: 1;
                }
            }
            @keyframes customDialogSlideOut {
                from {
                    transform: translate(-50%, 0) scale(1);
                    opacity: 1;
                }
                to {
                    transform: translate(-50%, -16px) scale(0.95);
                    opacity: 0;
                }
            }
            
            .dialog-overlay-in {
                animation: customDialogFadeIn 0.22s cubic-bezier(0.16, 1, 0.3, 1) forwards;
            }
            .dialog-overlay-out {
                animation: customDialogFadeOut 0.18s cubic-bezier(0.16, 1, 0.3, 1) forwards;
            }
            .dialog-box-in {
                animation: customDialogSlideIn 0.28s cubic-bezier(0.34, 1.56, 0.64, 1) forwards;
            }
            .dialog-box-out {
                animation: customDialogSlideOut 0.2s cubic-bezier(0.16, 1, 0.3, 1) forwards;
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
        // 1. Create Overlay (No blur initially)
        const overlay = document.createElement('div');
        overlay.className = 'fixed inset-0 z-[9999] bg-slate-950/20 dark:bg-black/40 dialog-overlay-in transition-all duration-300';

        // 2. Create Dialog Container (Use higher opacity backgrounds initially, no backdrop-blur)
        const dialog = document.createElement('div');
        dialog.className = 'fixed top-14 left-1/2 -translate-x-1/2 z-[10000] w-[90vw] max-w-[420px] rounded-2xl border border-white/50 dark:border-white/10 bg-white/95 dark:bg-[#1a1f30]/98 shadow-2xl p-6 text-slate-800 dark:text-slate-100 flex flex-col gap-4 dialog-box-in transition-all duration-300';

        const isConfirm = options.type === 'confirm';
        const icon = isConfirm ? 'help' : 'info';
        const iconColorClass = isConfirm ? 'text-primary dark:text-primary-fixed-dim' : 'text-amber-500';

        dialog.innerHTML = `
            <div class="flex items-start gap-4">
                <span class="material-symbols-outlined text-[24px] ${iconColorClass} shrink-0 mt-0.5 select-none">${icon}</span>
                <div class="flex flex-col gap-1.5 min-w-0 flex-1">
                    <h3 class="text-[15px] font-bold tracking-wide select-none">${isConfirm ? '确认提示' : '系统提示'}</h3>
                    <p class="text-[13.5px] leading-relaxed text-slate-600 dark:text-slate-300 break-words whitespace-pre-wrap select-text">${options.message}</p>
                </div>
            </div>
            <div class="flex justify-end gap-3.5 mt-2">
                ${isConfirm ? `
                    <button id="custom-dialog-cancel" class="px-4 py-2 rounded-xl text-slate-600 dark:text-slate-400 bg-slate-100/80 hover:bg-slate-200/80 dark:bg-white/5 dark:hover:bg-white/10 border border-slate-200/60 dark:border-white/5 active:scale-[0.98] transition-all text-[13px] font-medium select-none">
                        取消
                    </button>
                ` : ''}
                <button id="custom-dialog-ok" class="px-5 py-2 rounded-xl bg-primary text-white hover:bg-primary/90 active:bg-primary/95 active:scale-[0.98] shadow-md shadow-primary/10 transition-all text-[13px] font-medium select-none">
                  确定
                </button>
            </div>
        `;

        document.body.appendChild(overlay);
        document.body.appendChild(dialog);

        const btnOk = dialog.querySelector('#custom-dialog-ok') as HTMLButtonElement;
        const btnCancel = dialog.querySelector('#custom-dialog-cancel') as HTMLButtonElement | null;

        // Auto focus OK button
        if (btnOk) {
            btnOk.focus();
        }

        // Delay backdrop-blur activation until animation completes
        const handleAnimationEnd = (e: AnimationEvent) => {
            if (e.animationName === 'customDialogSlideIn') {
                dialog.classList.remove('bg-white/95', 'dark:bg-[#1a1f30]/98');
                dialog.classList.add('bg-white/75', 'dark:bg-[#1a1f30]/80', 'backdrop-blur-xl');
                overlay.classList.add('backdrop-blur-[2px]');
            }
        };
        dialog.addEventListener('animationend', handleAnimationEnd);

        const close = (result: boolean) => {
            cleanupEvents();

            // Force instant removal of all heavy filters and transitions to avoid animation stutter
            dialog.style.backdropFilter = 'none';
            (dialog.style as any).webkitBackdropFilter = 'none';
            overlay.style.backdropFilter = 'none';
            (overlay.style as any).webkitBackdropFilter = 'none';
            dialog.style.transition = 'none';
            overlay.style.transition = 'none';

            // Strip classes
            dialog.classList.remove('backdrop-blur-xl', 'bg-white/75', 'dark:bg-[#1a1f30]/80');
            dialog.classList.add('bg-white/95', 'dark:bg-[#1a1f30]/98');
            overlay.classList.remove('backdrop-blur-[2px]');

            overlay.classList.remove('dialog-overlay-in');
            overlay.classList.add('dialog-overlay-out');
            dialog.classList.remove('dialog-box-in');
            dialog.classList.add('dialog-box-out');

            setTimeout(() => {
                overlay.remove();
                dialog.remove();
                resolve(result);
            }, 200);
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
            dialog.removeEventListener('animationend', handleAnimationEnd);
            btnOk.removeEventListener('click', handleOk);
            if (btnCancel) {
                btnCancel.removeEventListener('click', handleCancel);
            }
            overlay.removeEventListener('click', isConfirm ? handleCancel : handleOk);
            document.removeEventListener('keydown', handleKeyDown);
        }
    });
}
