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
    path: '/merchandise',
    component: () => import('@/views/MerchandiseView.vue'),
  },
  {
    path: '/checkout',
    component: () => import('@/views/CheckoutView.vue'),
  },
  {
    path: '/checkout/confirm',
    component: () => import('@/views/CheckoutConfirmView.vue'),
  },
  {
    path: '/login',
    component: () => import('@/views/LoginView.vue'),
  },
  {
    path: '/auth/callback',
    component: () => import('@/views/AuthCallbackView.vue'),
  },
  {
    path: '/portal',
    component: () => import('@/views/PortalView.vue'),
    meta: { requiresAuth: true },
    children: [
      {
        path: '',
        name: 'portal-dashboard',
        component: () => import('@/views/portal/PortalDashboard.vue'),
      },
      {
        path: 'profile',
        name: 'portal-profile',
        component: () => import('@/views/portal/ProfileView.vue'),
      },
      {
        path: 'boats',
        name: 'portal-boats',
        component: () => import('@/views/portal/BoatsView.vue'),
      },
      {
        path: 'directory',
        name: 'portal-directory',
        component: () => import('@/views/portal/DirectoryView.vue'),
      },
      {
        path: 'documents',
        name: 'portal-documents',
        component: () => import('@/views/portal/DocumentsView.vue'),
      },
      {
        path: 'waiting-list',
        name: 'portal-waiting-list',
        component: () => import('@/views/portal/WaitingListView.vue'),
      },
      {
        path: 'slip',
        name: 'portal-slip',
        component: () => import('@/views/portal/SlipView.vue'),
      },
      {
        path: 'bookings',
        name: 'portal-bookings',
        component: () => import('@/views/portal/BookingsView.vue'),
      },
      {
        path: 'dugnad',
        name: 'portal-dugnad',
        component: () => import('@/views/portal/DugnadView.vue'),
      },
      {
        path: 'feature-requests',
        name: 'portal-feature-requests',
        component: () => import('@/views/portal/FeatureRequestsView.vue'),
      },
      {
        path: 'forum',
        name: 'forum',
        component: () => import('@/views/portal/ForumView.vue'),
        children: [
          {
            path: ':roomId',
            name: 'forum-room',
            component: () => import('@/views/portal/ForumRoomView.vue'),
          },
        ],
      },
    ],
  },
  {
    path: '/admin',
    component: () => import('@/views/admin/AdminLayout.vue'),
    meta: { requiresAuth: true, requiresAdmin: true },
    children: [
      {
        path: '',
        redirect: '/admin/events',
      },
      {
        path: 'users',
        name: 'admin-users',
        component: () => import('@/views/admin/UsersAdminView.vue'),
      },
      {
        path: 'slips',
        name: 'admin-slips',
        component: () => import('@/views/admin/SlipsAdminView.vue'),
      },
      {
        path: 'waiting-list',
        name: 'admin-waiting-list',
        component: () => import('@/views/admin/WaitingListAdminView.vue'),
      },
      {
        path: 'events',
        name: 'admin-events',
        component: () => import('@/views/admin/EventsAdminView.vue'),
      },
      {
        path: 'bookings',
        name: 'admin-bookings',
        component: () => import('@/views/admin/BookingsAdminView.vue'),
      },
      {
        path: 'documents',
        name: 'admin-documents',
        component: () => import('@/views/admin/DocumentsAdminView.vue'),
      },
      {
        path: 'pricing',
        name: 'admin-pricing',
        component: () => import('@/views/admin/PricingAdminView.vue'),
      },
      {
        path: 'products',
        name: 'admin-products',
        component: () => import('@/views/admin/ProductsAdminView.vue'),
      },
      {
        path: 'communication',
        name: 'admin-communication',
        component: () => import('@/views/admin/CommunicationView.vue'),
      },
      {
        path: 'projects',
        name: 'admin-projects',
        component: () => import('@/views/admin/ProjectsView.vue'),
      },
      {
        path: 'projects/:projectId',
        name: 'admin-kanban',
        component: () => import('@/views/admin/KanbanView.vue'),
      },
      {
        path: 'financials',
        name: 'admin-financials',
        component: () => import('@/views/admin/FinancialsView.vue'),
      },
      {
        path: 'financials/payments',
        name: 'admin-payments',
        component: () => import('@/views/admin/PaymentsListView.vue'),
      },
      {
        path: 'financials/overdue',
        name: 'admin-overdue',
        component: () => import('@/views/admin/OverdueView.vue'),
      },
      {
        path: 'financials/invoices/new',
        name: 'admin-invoice-create',
        component: () => import('@/views/admin/InvoiceCreateView.vue'),
      },
      {
        path: 'boats',
        name: 'admin-boats',
        component: () => import('@/views/admin/BoatsAdminView.vue'),
      },
      {
        path: 'dugnad',
        name: 'admin-dugnad',
        component: () => import('@/views/admin/DugnadAdminView.vue'),
      },
      {
        path: 'map',
        name: 'admin-map',
        component: () => import('@/views/admin/MapMarkersAdminView.vue'),
      },
    ],
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.ready

  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { path: '/login' }
  }

  if (to.meta.requiresAdmin && !auth.hasRole('admin') && !auth.hasRole('styre')) {
    return { path: '/' }
  }
})
