import { ipcRenderer, shell } from '../shared/ipc';

export function showOneStopAuthModal(): Promise<{ success: boolean; email: string; projectId: string } | null> {
    return new Promise((resolve) => {
        const overlay = document.createElement('div');
        overlay.className = 'fixed inset-0 bg-slate-950/60 z-[100] flex items-center justify-center transition-opacity duration-200';
        overlay.style.opacity = '0';

        const card = document.createElement('div');
        card.className = 'bg-white dark:bg-[#1e2538] w-[460px] max-w-[90vw] rounded-2xl border border-outline-variant/60 shadow-2xl p-6 flex flex-col gap-4 transform scale-95 transition-transform duration-200';

        let cachedAuthData: any = null;

        const escListener = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                cleanup(null);
            }
        };

        function cleanup(result: { success: boolean; email: string; projectId: string } | null) {
            document.removeEventListener('keydown', escListener);
            overlay.style.opacity = '0';
            card.classList.add('scale-95');
            setTimeout(() => {
                overlay.remove();
            }, 200);
            resolve(result);
        }

        function showStep1And2() {
            card.innerHTML = `
                <div class="flex items-center gap-2 text-primary">
                    <span class="material-symbols-outlined text-[20px]">vpn_key</span>
                    <h3 class="text-base font-bold text-on-surface dark:text-white">Google Cloud 账号授权</h3>
                </div>
                
                <div class="flex flex-col gap-3.5 my-1 text-[13px] text-on-surface dark:text-white">
                    <div class="text-[12px] text-outline leading-relaxed bg-slate-50 dark:bg-white/5 p-3.5 rounded-xl border border-outline-variant/20 flex flex-col gap-2.5">
                        <p>1. 点击下方按钮复制链接并在浏览器中打开，完成 Google 账户授权：</p>
                        <button id="flowOpenAuthLink" type="button" disabled class="w-full py-2.5 bg-slate-100 dark:bg-white/5 text-outline rounded-lg transition-all font-semibold text-[12px] border border-outline-variant/20 flex items-center justify-center gap-1.5 opacity-50 cursor-not-allowed">
                            <span class="material-symbols-outlined text-[14px] animate-spin">refresh</span>
                            正在获取官方授权链接...
                        </button>
                    </div>

                    <div class="flex flex-col gap-1.5">
                        <label class="text-[11px] text-outline font-medium">2. 将网页上重定向或显示的“授权码 (Authorization Code)”粘贴在下方：</label>
                        <input type="text" id="flowAuthCodeInput" placeholder="输入以 4/ 开头的授权码..." class="w-full px-3 py-2 text-[13px] bg-slate-50 dark:bg-white/5 border border-outline-variant/30 rounded-xl focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white placeholder-outline/60" autofocus />
                    </div>
                    
                    <div id="flowError" class="text-[11px] text-red-500 bg-red-500/10 p-2.5 rounded-lg border border-red-500/20 hidden break-all leading-normal"></div>
                </div>
                
                <div class="flex justify-end gap-2 mt-2">
                    <button id="flowCancel" type="button" class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40">取消</button>
                    <button id="flowConfirm" type="button" class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm flex items-center justify-center gap-1">开始登录</button>
                </div>
            `;

            const btnOpen = card.querySelector('#flowOpenAuthLink') as HTMLButtonElement;
            const inputAuthCode = card.querySelector('#flowAuthCodeInput') as HTMLInputElement;
            const btnCancel = card.querySelector('#flowCancel') as HTMLButtonElement;
            const btnConfirm = card.querySelector('#flowConfirm') as HTMLButtonElement;
            const divError = card.querySelector('#flowError') as HTMLDivElement;

            setTimeout(() => inputAuthCode.focus(), 250);

            ipcRenderer.invoke('auth:get-manual-oauth-url').then((authData: any) => {
                if (authData && authData.url) {
                    cachedAuthData = authData;
                    btnOpen.disabled = false;
                    btnOpen.className = 'w-full py-2.5 bg-primary/10 hover:bg-primary/20 text-primary hover:text-primary rounded-lg transition-all font-semibold text-[12px] border border-primary/20 flex items-center justify-center gap-1.5 cursor-pointer';
                    btnOpen.innerHTML = '<span class="material-symbols-outlined text-[14px]">open_in_new</span> 复制链接并打开浏览器';
                } else {
                    btnOpen.innerHTML = '获取授权链接失败，请重试';
                }
            }).catch((err: any) => {
                btnOpen.innerHTML = '获取授权链接错误: ' + err.message;
            });

            btnOpen.addEventListener('click', () => {
                if (!cachedAuthData) return;
                navigator.clipboard.writeText(cachedAuthData.url).then(() => {
                    const origText = btnOpen.innerHTML;
                    btnOpen.innerHTML = '<span class="material-symbols-outlined text-[14px]">done</span> 已复制链接并打开浏览器';
                    setTimeout(() => {
                        btnOpen.innerHTML = origText;
                    }, 2000);
                }).catch(() => {});
                shell.openExternal(cachedAuthData.url);
            });

            btnCancel.addEventListener('click', () => cleanup(null));
            btnConfirm.addEventListener('click', handleConfirm);
            inputAuthCode.addEventListener('keydown', (e: KeyboardEvent) => {
                if (e.key === 'Enter') handleConfirm();
            });

            async function handleConfirm() {
                const code = inputAuthCode.value.trim();
                if (!code) {
                    alert('请输入授权码 (Authorization Code)');
                    inputAuthCode.focus();
                    return;
                }
                if (!cachedAuthData) {
                    alert('授权链接尚未准备好，请稍候');
                    return;
                }

                btnConfirm.disabled = true;
                btnConfirm.innerHTML = '<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span> 校验中...';
                inputAuthCode.disabled = true;
                btnOpen.disabled = true;
                divError.classList.add('hidden');

                try {
                    const res = await ipcRenderer.invoke('auth:exchange-manual-code', {
                        code: code,
                        code_verifier: cachedAuthData.code_verifier
                    });

                    if (res.success) {
                        showStep3(res);
                    } else {
                        throw new Error(res.error || '未知错误');
                    }
                } catch (err: any) {
                    btnConfirm.disabled = false;
                    btnConfirm.innerHTML = '开始登录';
                    inputAuthCode.disabled = false;
                    btnOpen.disabled = false;
                    divError.innerHTML = '校验失败: ' + err.message;
                    divError.classList.remove('hidden');
                }
            }
        }

        function showStep3(exchangeRes: any) {
            const { email, access_token, refresh_token, activeProjectId, projects, listError } = exchangeRes;

            let projectUIHtml = '';
            if (projects && projects.length > 0) {
                projectUIHtml = `
                    <div class="flex flex-col gap-1.5">
                        <label class="text-[11px] text-outline font-bold uppercase">选择要绑定的 Google Cloud 项目：</label>
                        <select id="flowProjectSelect" class="w-full px-3 py-2 text-[13px] bg-slate-50 dark:bg-white/5 border border-outline-variant/30 rounded-xl focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white">
                            ${projects.map((p: any) => `<option value="${p.projectId}" ${p.projectId === activeProjectId ? 'selected' : ''}>${p.name || p.projectId} (${p.projectId})</option>`).join('')}
                        </select>
                        <p class="text-[10px] text-outline leading-relaxed mt-0.5">已从您的云端账户成功获取项目列表。请选择一个启用了 Gemini/Cloud AI Companion 的项目。</p>
                    </div>
                `;
            } else {
                const displayError = listError ? ` (原因: ${listError})` : '';
                projectUIHtml = `
                    <div class="flex flex-col gap-2.5">
                        <div class="text-[11px] bg-amber-500/10 text-amber-600 dark:text-amber-400 p-3 rounded-xl border border-amber-500/20 leading-relaxed flex items-start gap-1.5">
                            <span class="material-symbols-outlined text-[16px] shrink-0 mt-0.5">warning</span>
                            <div class="break-all">
                                自动从云端获取项目列表失败${displayError}。<br/>
                                由于谷歌官方 Client ID 的 API 限制，无法直接从云端列出您的项目。请在下方手动输入您的项目 ID。
                            </div>
                        </div>
                        <div class="flex flex-col gap-1.5">
                            <label class="text-[11px] text-outline font-bold uppercase">请输入您的 Google Cloud 项目 ID (Project ID)：</label>
                            <input type="text" id="flowProjectInput" value="${activeProjectId || ''}" placeholder="例如: my-api-495823" class="w-full px-3 py-2 text-[13px] bg-slate-50 dark:bg-white/5 border border-outline-variant/30 rounded-xl focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white placeholder-outline/60" autofocus />
                            <p class="text-[10px] text-outline leading-relaxed mt-0.5">项目 ID 可以从 <a href="https://console.cloud.google.com" target="_blank" class="text-primary hover:underline">Google Cloud 控制台</a> 首页的“项目信息”中复制。请输入精确的 Project ID，否则拦截请求时会报错。</p>
                        </div>
                    </div>
                `;
            }

            card.innerHTML = `
                <div class="flex items-center gap-2 text-primary">
                    <span class="material-symbols-outlined text-[20px]">cloud_sync</span>
                    <h3 class="text-base font-bold text-on-surface dark:text-white">绑定 GCP 项目</h3>
                </div>
                
                <div class="flex flex-col gap-3.5 my-1 text-[13px] text-on-surface dark:text-white">
                    <div class="text-[12px] text-outline leading-relaxed bg-slate-50 dark:bg-white/5 p-3.5 rounded-xl border border-outline-variant/20 flex flex-col gap-1.5">
                        <div class="flex justify-between">
                            <span class="text-outline">授权邮箱：</span>
                            <span class="font-bold font-data-mono text-on-surface dark:text-white">${email}</span>
                        </div>
                    </div>

                    ${projectUIHtml}
                    
                    <div id="flowSubmitError" class="text-[11px] text-red-500 bg-red-500/10 p-2.5 rounded-lg border border-red-500/20 hidden break-all leading-normal"></div>
                </div>
                
                <div class="flex justify-end gap-2 mt-2">
                    <button id="flowCancel" type="button" class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40">取消</button>
                    <button id="flowSubmit" type="button" class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm flex items-center justify-center gap-1">确认绑定并登录</button>
                </div>
            `;

            const btnCancel = card.querySelector('#flowCancel') as HTMLButtonElement;
            const btnSubmit = card.querySelector('#flowSubmit') as HTMLButtonElement;
            const divSubmitError = card.querySelector('#flowSubmitError') as HTMLDivElement;
            const selectProject = card.querySelector('#flowProjectSelect') as HTMLSelectElement | null;
            const inputProject = card.querySelector('#flowProjectInput') as HTMLInputElement | null;

            if (inputProject) setTimeout(() => inputProject.focus(), 250);

            btnCancel.addEventListener('click', () => cleanup(null));
            btnSubmit.addEventListener('click', handleSubmit);
            if (inputProject) {
                inputProject.addEventListener('keydown', (e: KeyboardEvent) => {
                    if (e.key === 'Enter') handleSubmit();
                });
            }

            async function handleSubmit() {
                let projectId = '';
                if (selectProject) {
                    projectId = selectProject.value.trim();
                } else if (inputProject) {
                    projectId = inputProject.value.trim();
                }

                if (!projectId) {
                    alert('请输入或选择项目 ID');
                    if (inputProject) inputProject.focus();
                    return;
                }

                btnSubmit.disabled = true;
                btnSubmit.innerHTML = '<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span> 绑定中...';
                if (inputProject) inputProject.disabled = true;
                if (selectProject) selectProject.disabled = true;
                divSubmitError.classList.add('hidden');

                try {
                    const res = await ipcRenderer.invoke('auth:add-manual-account', {
                        email,
                        access_token,
                        refresh_token,
                        projectId
                    });

                    if (res.success) {
                        cleanup({ success: true, email, projectId });
                    } else {
                        throw new Error(res.error || '未知错误');
                    }
                } catch (err: any) {
                    btnSubmit.disabled = false;
                    btnSubmit.innerHTML = '确认绑定并登录';
                    if (inputProject) inputProject.disabled = false;
                    if (selectProject) selectProject.disabled = false;
                    divSubmitError.innerHTML = '保存失败: ' + err.message;
                    divSubmitError.classList.remove('hidden');
                }
            }
        }

        overlay.appendChild(card);
        document.body.appendChild(overlay);

        requestAnimationFrame(() => {
            overlay.style.opacity = '1';
            card.classList.remove('scale-95');
        });

        showStep1And2();

        overlay.addEventListener('click', (e) => {
            if (e.target === overlay) cleanup(null);
        });
        
        document.addEventListener('keydown', escListener);
    });
}
