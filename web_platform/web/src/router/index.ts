import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import { getToken, authState, refreshCurrentUser } from '../api/client'
import LoginView from '../views/LoginView.vue'
import DashboardView from '../views/DashboardView.vue'
import PricingView from '../views/PricingView.vue'
import OrderCallbackView from '../views/OrderCallbackView.vue'
import OrderConfirmView from '../views/OrderConfirmView.vue'
import OrdersView from '../views/OrdersView.vue'
import AdminView from '../views/admin/AdminView.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: () => (getToken() ? '/dashboard' : '/pricing'),
  },
  {
    path: '/login',
    name: 'Login',
    component: LoginView,
  },
  {
    path: '/dashboard',
    name: 'Dashboard',
    component: DashboardView,
    meta: { requiresAuth: true },
  },
  {
    path: '/pricing',
    name: 'Pricing',
    component: PricingView,
  },
  {
    path: '/checkout/confirm',
    name: 'OrderConfirm',
    component: OrderConfirmView,
    meta: { requiresAuth: true },
  },
  {
    path: '/orders',
    name: 'Orders',
    component: OrdersView,
    meta: { requiresAuth: true },
  },
  {
    path: '/orders/callback',
    name: 'OrderCallbackAlt',
    component: OrderCallbackView,
  },
  {
    path: '/callback',
    name: 'Callback',
    component: OrderCallbackView,
  },
  {
    path: '/admin',
    name: 'Admin',
    component: AdminView,
    meta: { requiresAuth: true, requiresAdmin: true },
  },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

router.beforeEach(async (to, _from, next) => {
  const token = getToken()
  if (to.meta.requiresAuth && !token) {
    return next('/login')
  }

  if (to.meta.requiresAdmin) {
    if (!token) {
      return next('/login')
    }
    if (!authState.user) {
      await refreshCurrentUser()
    }
    if (authState.user?.role !== 'admin') {
      // 非管理员直接重定向至用户控制台
      return next('/dashboard')
    }
  }

  next()
})

export default router
