import { createRouter, createWebHashHistory } from 'vue-router';
import Dashboard from '../views/Dashboard.vue';
import Accounts from '../views/Accounts.vue';
import OTP from '../views/OTP.vue';
import Packets from '../views/Packets.vue';
import Settings from '../views/Settings.vue';
import UsageDetails from '../views/UsageDetails.vue';

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      redirect: '/dashboard'
    },
    {
      path: '/dashboard',
      name: 'dashboard',
      component: Dashboard
    },
    {
      path: '/accounts',
      name: 'accounts',
      component: Accounts
    },
    {
      path: '/usage',
      name: 'usage',
      component: UsageDetails
    },
    {
      path: '/otp',
      name: 'otp',
      component: OTP
    },
    {
      path: '/packets',
      name: 'packets',
      component: Packets
    },
    {
      path: '/settings',
      name: 'settings',
      component: Settings
    }
  ]
});

export default router;
