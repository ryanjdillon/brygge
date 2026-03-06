import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: () => import('@/views/HomeView.vue'),
  },
  {
    path: '/calendar',
    component: () => import('@/views/CalendarView.vue'),
  },
  {
    path: '/weather',
    component: () => import('@/views/WeatherView.vue'),
  },
  {
    path: '/directions',
    component: () => import('@/views/DirectionsView.vue'),
  },
  {
    path: '/contact',
    component: () => import('@/views/ContactView.vue'),
  },
  {
    path: '/pricing',
    component: () => import('@/views/PricingView.vue'),
  },
  {
    path: '/join',
    component: () => import('@/views/JoinView.vue'),
  },
  {
    path: '/login',
    component: () => import('@/views/LoginView.vue'),
  },
  {
    path: '/portal',
    component: () => import('@/views/PortalView.vue'),
    meta: { requiresAuth: true },
  },
  {
    path: '/admin',
    component: () => import('@/views/AdminView.vue'),
    meta: { requiresAuth: true, requiresAdmin: true },
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const auth = useAuthStore()

  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { path: '/login' }
  }

  if (to.meta.requiresAdmin && !auth.hasRole('admin')) {
    return { path: '/' }
  }
})
