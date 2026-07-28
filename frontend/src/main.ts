import { createApp } from 'vue';
import { createPinia } from 'pinia';
import router from './router';
import App from './App.vue';
import { initCustomDialog } from './shared/customDialog';

import './style.css';
import './app.css';
import './ui/dashboard.css'; // We'll keep it for now until we migrate the components

initCustomDialog();

const app = createApp(App);

app.use(createPinia());
app.use(router);

// Support legacy global bindings if anything still relies on it temporarily
(window as any).switchView = (view: string) => {
    router.push({ name: view }).catch(() => {});
};
(window as any)._vueRouterPush = (view: string) => {
    router.push({ name: view }).catch(() => {});
};

// 注册全局通用的模态框控制逻辑，防止在 Settings 页面加载前调用出错
(window as any)._relayOpenModal = (id: string) => {
    const modal = document.getElementById(id);
    if (!modal) return;
    modal.classList.remove('hidden');
    void modal.offsetWidth;
    modal.classList.add('show');
};

(window as any)._relayCloseModal = (id: string) => {
    const modal = document.getElementById(id);
    if (!modal) return;
    modal.classList.remove('show');
    
    let isHidden = false;
    const hideModal = () => {
        if (!isHidden) {
            isHidden = true;
            if (!modal.classList.contains('show')) {
                modal.classList.add('hidden');
            }
        }
    };

    // 200ms 强制隐藏兜底，防止 CSS transitionend 事件未触发导致弹窗关不掉
    setTimeout(hideModal, 200);

    const onTransitionEnd = (e: TransitionEvent) => {
        if (e.propertyName === 'opacity' && !modal.classList.contains('show')) {
            hideModal();
            modal.removeEventListener('transitionend', onTransitionEnd);
        }
    };
    modal.addEventListener('transitionend', onTransitionEnd);
};

app.mount('#app');


