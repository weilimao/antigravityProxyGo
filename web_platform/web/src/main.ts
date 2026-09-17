import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { pinia } from './stores'
import './style.css'

const app = createApp(App)
app.use(pinia)
app.use(router)
app.mount('#app')
// Version: 1.0.1-b2026091801

