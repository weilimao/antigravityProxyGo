// DOM Cache
let otpTable: HTMLElement | null = null;
let otpTableBody: HTMLElement | null = null;
let otpEmptyState: HTMLElement | null = null;
let otpCountBadge: HTMLElement | null = null;
let otpCountdown: HTMLElement | null = null;

let otpPaginationContainer: HTMLElement | null = null;
let otpPaginationInfo: HTMLElement | null = null;
let btnOtpPrevPage: HTMLButtonElement | null = null;
let btnOtpNextPage: HTMLButtonElement | null = null;
let otpPageIndicator: HTMLElement | null = null;

let lastSignature = '';

export function initRendererElements() {
    otpTable = document.getElementById('otpTable');
    otpTableBody = document.getElementById('otpTableBody');
    otpEmptyState = document.getElementById('otpEmptyState');
    otpCountBadge = document.getElementById('otpCountBadge');
    otpCountdown = document.getElementById('otpCountdown');

    otpPaginationContainer = document.getElementById('otpPaginationContainer');
    otpPaginationInfo = document.getElementById('otpPaginationInfo');
    btnOtpPrevPage = document.getElementById('btnOtpPrevPage') as HTMLButtonElement;
    btnOtpNextPage = document.getElementById('btnOtpNextPage') as HTMLButtonElement;
    otpPageIndicator = document.getElementById('otpPageIndicator');
}

export function renderOtpTable(
    paginatedList: any[],
    totalFilteredCount: number,
    currentPage: number,
    pageSize: number,
    onEditSecret: (accountId: string, email: string, currentSecret: string) => void,
    onClearSecret: (accountId: string, email: string) => void,
    onCopyCode: (code: string, btnEl: HTMLElement) => void
) {
    const body = otpTableBody;
    const table = otpTable;
    const empty = otpEmptyState;
    const badge = otpCountBadge;

    if (!body || !table || !empty || !badge) return;

    badge.textContent = `共 ${totalFilteredCount} 个账号`;

    // Calculate total pages
    const totalPages = Math.max(1, Math.ceil(totalFilteredCount / pageSize));

    // Update pagination controls
    if (otpPaginationContainer && otpPaginationInfo && btnOtpPrevPage && btnOtpNextPage && otpPageIndicator) {
        if (totalFilteredCount === 0) {
            otpPaginationContainer.classList.add('hidden');
            otpPaginationInfo.textContent = '显示第 0-0 条，共 0 条';
            btnOtpPrevPage.disabled = true;
            btnOtpNextPage.disabled = true;
            otpPageIndicator.textContent = '1 / 1';
        } else {
            otpPaginationContainer.classList.remove('hidden');
            const startItem = (currentPage - 1) * pageSize + 1;
            const endItem = Math.min(currentPage * pageSize, totalFilteredCount);
            otpPaginationInfo.textContent = `显示第 ${startItem}-${endItem} 条，共 ${totalFilteredCount} 条`;
            btnOtpPrevPage.disabled = currentPage === 1;
            btnOtpNextPage.disabled = currentPage === totalPages;
            otpPageIndicator.textContent = `${currentPage} / ${totalPages}`;
        }
    }

    if (!paginatedList || paginatedList.length === 0) {
        table.classList.add('hidden');
        empty.classList.remove('hidden');
        empty.classList.add('flex');
        lastSignature = '';
        return;
    }

    const currentSignature = paginatedList.map(item => `${item.accountId}:${item.email}:${item.hasSecret}:${!!item.error}`).join('|');

    if (currentSignature === lastSignature) {
        // High performance update: update both dynamic code value and countdown text in place
        paginatedList.forEach((item) => {
            const tr = document.getElementById(`otp-row-${item.accountId}`);
            if (tr) {
                const codeSpan = tr.querySelector('.otp-code-text');
                if (codeSpan) {
                    codeSpan.textContent = item.code || '------';
                }
                const remainingSpan = tr.querySelector('.otp-code-remaining');
                if (remainingSpan && typeof item.remaining === 'number') {
                    remainingSpan.textContent = `(${item.remaining}s)`;
                    if (item.remaining <= 5) {
                        remainingSpan.className = 'otp-code-remaining text-[11px] text-red-500 font-bold font-mono animate-pulse';
                    } else if (item.remaining <= 10) {
                        remainingSpan.className = 'otp-code-remaining text-[11px] text-amber-500 font-medium font-mono';
                    } else {
                        remainingSpan.className = 'otp-code-remaining text-[11px] text-primary/60 dark:text-primary-fixed-dim/60 font-mono';
                    }
                }
            }
        });
        return;
    }

    lastSignature = currentSignature;
    table.classList.remove('hidden');
    empty.classList.add('hidden');
    empty.classList.remove('flex');

    body.innerHTML = '';

    paginatedList.forEach((item) => {
        const tr = document.createElement('tr');
        tr.id = `otp-row-${item.accountId}`;
        tr.className = 'hover:bg-slate-50/50 dark:hover:bg-white/[0.02] transition-colors border-b border-outline-variant/10';

        // 1. Account Column
        const tdAccount = document.createElement('td');
        tdAccount.className = 'p-4 flex items-center gap-2.5 font-medium';
        tdAccount.innerHTML = `
            <span class="material-symbols-outlined text-[18px] text-primary/80 dark:text-primary-fixed-dim/80">account_circle</span>
            <span class="truncate max-w-[240px] text-slate-800 dark:text-slate-200">${item.email}</span>
        `;
        tr.appendChild(tdAccount);

        // 2. Secret status column
        const tdStatus = document.createElement('td');
        tdStatus.className = 'p-4 text-slate-600 dark:text-slate-400';
        if (item.hasSecret) {
            tdStatus.innerHTML = `
                <div class="flex items-center gap-1.5 text-emerald-600 dark:text-emerald-400 font-medium">
                    <span class="material-symbols-outlined text-[16px]">check_circle</span>
                    <span>已启用 2FA 保护</span>
                </div>
            `;
        } else {
            tdStatus.innerHTML = `
                <div class="flex items-center gap-1.5 text-outline dark:text-outline-variant">
                    <span class="material-symbols-outlined text-[16px]">info</span>
                    <span>未配置密钥</span>
                </div>
            `;
        }
        tr.appendChild(tdStatus);

        // 3. Verification code column
        const tdCode = document.createElement('td');
        tdCode.className = 'p-4 text-center';
        if (item.hasSecret) {
            const displayCode = item.code || '------';
            const displayErr = item.error;

            if (displayErr) {
                tdCode.innerHTML = `<span class="text-red-500 text-[11px]" title="${displayErr}">密钥无效/错误</span>`;
            } else {
                const codeWrapper = document.createElement('div');
                codeWrapper.className = 'inline-flex items-center justify-center gap-2 bg-primary/5 dark:bg-primary-fixed-dim/5 border border-primary/20 dark:border-primary-fixed-dim/20 rounded-xl px-4 py-1.5 hover:bg-primary/10 dark:hover:bg-primary-fixed-dim/10 transition-all cursor-pointer select-none group';
                codeWrapper.title = '点击直接复制验证码';
                
                // Color countdown dynamic initial state
                let countdownClass = 'otp-code-remaining text-[11px] text-primary/60 dark:text-primary-fixed-dim/60 font-mono';
                if (item.remaining <= 5) {
                    countdownClass = 'otp-code-remaining text-[11px] text-red-500 font-bold font-mono animate-pulse';
                } else if (item.remaining <= 10) {
                    countdownClass = 'otp-code-remaining text-[11px] text-amber-500 font-medium font-mono';
                }

                codeWrapper.innerHTML = `
                    <span class="otp-code-text text-[16px] font-bold tracking-widest font-mono text-primary dark:text-primary-fixed-dim">${displayCode}</span>
                    <span class="${countdownClass}">(${item.remaining}s)</span>
                    <span class="material-symbols-outlined text-[14px] text-primary/60 dark:text-primary-fixed-dim/60 group-hover:text-primary dark:group-hover:text-primary-fixed-dim transition-colors">content_copy</span>
                `;
                
                codeWrapper.addEventListener('click', () => {
                    const latestCode = codeWrapper.querySelector('.otp-code-text')?.textContent || displayCode;
                    onCopyCode(latestCode, codeWrapper);
                });

                tdCode.appendChild(codeWrapper);
            }
        } else {
            tdCode.innerHTML = '<span class="text-outline/40 font-mono">------</span>';
        }
        tr.appendChild(tdCode);

        // 4. Action buttons column
        const tdAction = document.createElement('td');
        tdAction.className = 'p-4 text-right flex justify-end gap-2 items-center';

        if (item.hasSecret) {
            const btnEdit = document.createElement('button');
            btnEdit.className = 'px-3 py-1 bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg text-[12px] font-medium border border-outline-variant/30 transition-colors cursor-pointer';
            btnEdit.textContent = '修改';
            btnEdit.addEventListener('click', () => {
                onEditSecret(item.accountId, item.email, item.accountId);
            });

            const btnClear = document.createElement('button');
            btnClear.className = 'px-3 py-1 bg-red-500/10 hover:bg-red-500/20 text-red-500 rounded-lg text-[12px] font-medium border border-red-500/20 hover:border-red-500/30 transition-colors cursor-pointer';
            btnClear.textContent = '清除';
            btnClear.addEventListener('click', () => {
                onClearSecret(item.accountId, item.email);
            });

            tdAction.appendChild(btnEdit);
            tdAction.appendChild(btnClear);
        } else {
            const btnConfig = document.createElement('button');
            btnConfig.className = 'px-4 py-1 bg-primary text-white hover:bg-primary/95 rounded-lg text-[12px] font-bold shadow-sm transition-colors cursor-pointer';
            btnConfig.textContent = '配置';
            btnConfig.addEventListener('click', () => {
                onEditSecret(item.accountId, item.email, '');
            });
            tdAction.appendChild(btnConfig);
        }

        tr.appendChild(tdAction);
        body.appendChild(tr);
    });
}

export function updateOtpCountdown(seconds: number) {
    if (!otpCountdown) return;
    otpCountdown.textContent = seconds >= 0 ? seconds.toString() : '-';

    if (seconds <= 5) {
        otpCountdown.className = 'font-bold text-red-500 w-6 text-center animate-pulse';
    } else if (seconds <= 10) {
        otpCountdown.className = 'font-bold text-amber-500 w-6 text-center';
    } else {
        otpCountdown.className = 'font-bold text-primary dark:text-primary-fixed-dim w-6 text-center';
    }
}

export function show2FAKeyModal(
    accountId: string,
    email: string,
    currentSecret: string,
    onSave: (secret: string) => Promise<{ success: boolean; error?: string }>
): Promise<boolean> {
    return new Promise((resolve) => {
        const overlay = document.createElement('div');
        // REMOVED backdrop-blur-sm for maximum CPU rendering performance ( butter-smooth input )
        overlay.className = 'fixed inset-0 bg-slate-950/70 z-[100] flex items-center justify-center transition-opacity duration-200';
        overlay.style.opacity = '0';

        const card = document.createElement('div');
        card.className = 'bg-white dark:bg-[#1e2538] w-[460px] max-w-[90vw] rounded-2xl border border-outline-variant/60 shadow-2xl p-6 flex flex-col gap-4 transform scale-95 transition-transform duration-200';

        card.innerHTML = `
            <div class="flex items-center gap-2 text-primary dark:text-primary-fixed-dim">
                <span class="material-symbols-outlined text-[20px]">vpn_key</span>
                <h3 class="text-base font-bold text-on-surface dark:text-white">${currentSecret ? '修改' : '配置'} 2FA 密钥</h3>
            </div>
            
            <div class="flex flex-col gap-3.5 my-1 text-[13px] text-on-surface dark:text-white">
                <div class="text-[11px] text-outline leading-relaxed bg-slate-50 dark:bg-white/5 p-3 rounded-xl border border-outline-variant/20">
                    账号: <strong class="text-slate-800 dark:text-slate-200">${email}</strong>
                </div>

                <div class="flex flex-col gap-1.5">
                    <label class="text-[11px] text-outline font-medium">输入 2FA 密钥 (Base32 编码，支持空格):</label>
                    <input type="text" id="otpSecretInput" placeholder="例如: JBSWY3DPEHPK3PXP" value="${currentSecret || ''}" class="w-full px-3 py-2 text-[13px] bg-slate-50 dark:bg-white/5 border border-outline-variant/30 rounded-xl focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white placeholder-outline/40 font-mono" autofocus />
                    <p class="text-[10px] text-outline leading-normal mt-0.5">提示: 谷歌两步验证密钥通常是由 16 位或 32 位英文字母和数字组成。</p>
                </div>
                
                <div id="otpError" class="text-[11px] text-red-500 bg-red-500/10 p-2.5 rounded-lg border border-red-500/20 hidden break-all leading-normal"></div>
            </div>
            
            <div class="flex justify-end gap-2 mt-2">
                <button id="otpCancel" type="button" class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer">取消</button>
                <button id="otpConfirm" type="button" class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm flex items-center justify-center gap-1 cursor-pointer">保存并验证</button>
            </div>
        `;

        document.body.appendChild(overlay);
        overlay.appendChild(card);

        setTimeout(() => {
            overlay.style.opacity = '1';
            card.classList.remove('scale-95');
        }, 50);

        const inputSecret = card.querySelector('#otpSecretInput') as HTMLInputElement;
        const btnCancel = card.querySelector('#otpCancel') as HTMLButtonElement;
        const btnConfirm = card.querySelector('#otpConfirm') as HTMLButtonElement;
        const divError = card.querySelector('#otpError') as HTMLDivElement;

        inputSecret.focus();
        if (currentSecret) {
            inputSecret.select();
        }

        function cleanup(success: boolean) {
            overlay.style.opacity = '0';
            card.classList.add('scale-95');
            setTimeout(() => {
                overlay.remove();
            }, 200);
            resolve(success);
        }

        btnCancel.addEventListener('click', () => cleanup(false));

        async function handleConfirm() {
            const secret = inputSecret.value.trim();
            btnConfirm.disabled = true;
            btnConfirm.classList.add('opacity-70', 'cursor-not-allowed');
            divError.classList.add('hidden');

            try {
                const res = await onSave(secret);
                if (res.success) {
                    cleanup(true);
                } else {
                    divError.textContent = res.error || '保存失败';
                    divError.classList.remove('hidden');
                }
            } catch (err: any) {
                divError.textContent = '请求出错: ' + err.message;
                divError.classList.remove('hidden');
            } finally {
                btnConfirm.disabled = false;
                btnConfirm.classList.remove('opacity-70', 'cursor-not-allowed');
            }
        }

        btnConfirm.addEventListener('click', handleConfirm);
        inputSecret.addEventListener('keydown', (e: KeyboardEvent) => {
            if (e.key === 'Enter') handleConfirm();
        });
    });
}

export function showAdd2FAModal(
    onSave: (email: string, secret: string) => Promise<{ success: boolean; error?: string }>
): Promise<boolean> {
    return new Promise((resolve) => {
        const overlay = document.createElement('div');
        // REMOVED backdrop-blur-sm for maximum CPU rendering performance ( butter-smooth input )
        overlay.className = 'fixed inset-0 bg-slate-950/70 z-[100] flex items-center justify-center transition-opacity duration-200';
        overlay.style.opacity = '0';

        const card = document.createElement('div');
        card.className = 'bg-white dark:bg-[#1e2538] w-[460px] max-w-[90vw] rounded-2xl border border-outline-variant/60 shadow-2xl p-6 flex flex-col gap-4 transform scale-95 transition-transform duration-200';

        card.innerHTML = `
            <div class="flex items-center gap-2 text-primary dark:text-primary-fixed-dim">
                <span class="material-symbols-outlined text-[20px]">add_circle</span>
                <h3 class="text-base font-bold text-on-surface dark:text-white">新增 2FA 账号</h3>
            </div>
            
            <div class="flex flex-col gap-3.5 my-1 text-[13px] text-on-surface dark:text-white">
                <div class="flex flex-col gap-1.5">
                    <label class="text-[11px] text-outline font-medium">账号邮箱 / 名称:</label>
                    <input type="text" id="addOtpEmailInput" placeholder="例如: my-account@gmail.com" class="w-full px-3 py-2 text-[13px] bg-slate-50 dark:bg-white/5 border border-outline-variant/30 rounded-xl focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white placeholder-outline/60" autofocus />
                </div>

                <div class="flex flex-col gap-1.5">
                    <label class="text-[11px] text-outline font-medium">2FA 密钥 (Base32 编码，支持空格):</label>
                    <input type="text" id="addOtpSecretInput" placeholder="例如: JBSWY3DPEHPK3PXP" class="w-full px-3 py-2 text-[13px] bg-slate-50 dark:bg-white/5 border border-outline-variant/30 rounded-xl focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white placeholder-outline/40 font-mono" />
                </div>
                
                <div id="addOtpError" class="text-[11px] text-red-500 bg-red-500/10 p-2.5 rounded-lg border border-red-500/20 hidden break-all leading-normal"></div>
            </div>
            
            <div class="flex justify-end gap-2 mt-2">
                <button id="addOtpCancel" type="button" class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer">取消</button>
                <button id="addOtpConfirm" type="button" class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm flex items-center justify-center gap-1 cursor-pointer">添加</button>
            </div>
        `;

        document.body.appendChild(overlay);
        overlay.appendChild(card);

        setTimeout(() => {
            overlay.style.opacity = '1';
            card.classList.remove('scale-95');
        }, 50);

        const inputEmail = card.querySelector('#addOtpEmailInput') as HTMLInputElement;
        const inputSecret = card.querySelector('#addOtpSecretInput') as HTMLInputElement;
        const btnCancel = card.querySelector('#addOtpCancel') as HTMLButtonElement;
        const btnConfirm = card.querySelector('#addOtpConfirm') as HTMLButtonElement;
        const divError = card.querySelector('#addOtpError') as HTMLDivElement;

        inputEmail.focus();

        function cleanup(success: boolean) {
            overlay.style.opacity = '0';
            card.classList.add('scale-95');
            setTimeout(() => {
                overlay.remove();
            }, 200);
            resolve(success);
        }

        btnCancel.addEventListener('click', () => cleanup(false));

        async function handleConfirm() {
            const email = inputEmail.value.trim();
            const secret = inputSecret.value.trim();

            if (!email) {
                divError.textContent = '请输入账号邮箱 / 名称';
                divError.classList.remove('hidden');
                inputEmail.focus();
                return;
            }

            btnConfirm.disabled = true;
            btnConfirm.classList.add('opacity-70', 'cursor-not-allowed');
            divError.classList.add('hidden');

            try {
                const res = await onSave(email, secret);
                if (res.success) {
                    cleanup(true);
                } else {
                    divError.textContent = res.error || '添加失败';
                    divError.classList.remove('hidden');
                }
            } catch (err: any) {
                divError.textContent = '请求出错: ' + err.message;
                divError.classList.remove('hidden');
            } finally {
                btnConfirm.disabled = false;
                btnConfirm.classList.remove('opacity-70', 'cursor-not-allowed');
            }
        }

        btnConfirm.addEventListener('click', handleConfirm);
        inputSecret.addEventListener('keydown', (e: KeyboardEvent) => {
            if (e.key === 'Enter') handleConfirm();
        });
        inputEmail.addEventListener('keydown', (e: KeyboardEvent) => {
            if (e.key === 'Enter') inputSecret.focus();
        });
    });
}
