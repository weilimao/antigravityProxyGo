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

app.mount('#app');


