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
    path: '/harbor',
    component: () => import('@/views/HarborView.vue'),
  },
  {
    path: '/motorhome',
    component: () => import('@/views/MotorhomeView.vue'),
  },
  {
    path: '/directions',
    component: () => import('@/views/DirectionsView.vue'),
  },
  {
    path: '/book',
    component: () => import('@/views/BookView.vue'),
  },
  {
    path: '/contact',
    component: () => import('@/views/ContactView.vue'),
  },
  {
    // Norwegian sales-terms page required by Vipps (and good practice
    // for any club taking online payments). Linked from the footer
    // and from every checkout step. See DIL-346.
    path: '/salgsvilkar',
    component: () => import('@/views/SalgsvilkarView.vue'),
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
        path: 'harbor-map',
        name: 'portal-harbor-map',
        component: () => import('@/views/portal/HarborMapView.vue'),
      },
      {
        path: 'bookings',
        name: 'portal-bookings',
        component: () => import('@/views/portal/BookingsView.vue'),
      },
      {
        path: 'volunteer',
        name: 'portal-volunteer',
        component: () => import('@/views/portal/VolunteerView.vue'),
      },
      {
        path: 'slip-sharing',
        name: 'portal-slip-sharing',
        component: () => import('@/views/portal/SlipSharingView.vue'),
      },
      {
        path: 'hoist',
        name: 'portal-hoist',
        component: () => import('@/views/portal/HoistBookingView.vue'),
      },
      {
        path: 'feature-requests',
        name: 'portal-feature-requests',
        component: () => import('@/views/portal/FeatureRequestsView.vue'),
      },
      {
        path: 'notifications',
        name: 'portal-notifications',
        component: () => import('@/views/portal/NotificationsView.vue'),
      },
      {
        path: 'privacy',
        name: 'portal-privacy',
        component: () => import('@/views/portal/PrivacyView.vue'),
      },
      {
        path: 'privacy-policy',
        name: 'portal-privacy-policy',
        component: () => import('@/views/portal/PrivacyPolicyView.vue'),
      },
      {
        path: 'security',
        name: 'portal-security',
        component: () => import('@/views/portal/SecurityView.vue'),
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
    // Stand-alone step-up verify page reached via 403 totp_required
    // redirects from useApi. Must NOT live under /admin/* so it
    // doesn't itself require step-up.
    path: '/admin/verify-totp',
    name: 'admin-verify-totp',
    component: () => import('@/views/auth/VerifyTotpView.vue'),
    meta: { requiresAuth: true },
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
        path: 'harbor-map',
        name: 'admin-harbor-map',
        component: () => import('@/views/admin/HarborMapAdminView.vue'),
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
        // Three-pane layout. Mailbox + thread selection is carried
        // in the query string (?address=…&thread=…) so the URL is
        // shareable and back/forward inside the view doesn't remount
        // the component tree.
        path: 'inbox',
        name: 'admin-inbox',
        component: () => import('@/views/admin/InboxView.vue'),
      },
      {
        path: 'notifications',
        name: 'admin-notifications',
        component: () => import('@/views/admin/NotificationsAdminView.vue'),
      },
      {
        path: 'gdpr',
        name: 'admin-gdpr',
        component: () => import('@/views/admin/GDPRAdminView.vue'),
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
        path: 'volunteer',
        name: 'admin-volunteer',
        component: () => import('@/views/admin/VolunteerAdminView.vue'),
      },
      {
        path: 'map',
        name: 'admin-map',
        component: () => import('@/views/admin/MapMarkersAdminView.vue'),
      },
      {
        path: 'slip-shares',
        name: 'admin-slip-shares',
        component: () => import('@/views/admin/SlipSharesAdminView.vue'),
      },
      {
        path: 'settings/booking',
        name: 'admin-booking-settings',
        component: () => import('@/views/admin/BookingSettingsView.vue'),
      },
      {
        path: 'settings/general',
        name: 'admin-general-settings',
        component: () => import('@/views/admin/GeneralSettingsView.vue'),
      },
      {
        path: 'accounting',
        name: 'admin-accounting',
        component: () => import('@/views/admin/AccountingDashboard.vue'),
      },
      {
        path: 'accounting/accounts',
        name: 'admin-accounts',
        component: () => import('@/views/admin/ChartOfAccountsView.vue'),
      },
      {
        path: 'accounting/journal',
        name: 'admin-journal',
        component: () => import('@/views/admin/JournalEntriesView.vue'),
      },
      {
        path: 'accounting/journal/new',
        name: 'admin-journal-new',
        component: () => import('@/views/admin/JournalEntryForm.vue'),
      },
      {
        path: 'accounting/faktura',
        name: 'admin-faktura',
        component: () => import('@/views/admin/FakturaView.vue'),
      },
      {
        path: 'faktura',
        redirect: (to) => ({ path: '/admin/accounting/faktura', query: to.query }),
      },
      {
        path: 'accounting/invoice-drafts',
        name: 'admin-invoice-drafts',
        redirect: () => ({ path: '/admin/accounting/faktura', query: { tab: 'drafts' } }),
      },
      {
        path: 'settings/site',
        name: 'admin-site-settings',
        component: () => import('@/views/admin/SiteSettingsView.vue'),
      },
      {
        // Legacy URL — older bookmarks land here. Redirects to the
        // canonical /admin/settings/site path. Safe to delete after
        // a release or two. See DIL-358.
        path: 'accounting/settings',
        redirect: '/admin/settings/site',
      },
      {
        path: 'accounting/bank-imports',
        name: 'admin-bank-imports',
        component: () => import('@/views/admin/BankImportsView.vue'),
      },
      {
        path: 'accounting/pdf-archive',
        name: 'admin-pdf-archive',
        redirect: '/admin/accounting/faktura?tab=archive',
      },
      {
        path: 'accounting/bank-accounts',
        name: 'admin-bank-accounts',
        component: () => import('@/views/admin/BankAccountsView.vue'),
      },
      {
        path: 'economy/settings',
        name: 'admin-economy-settings',
        component: () => import('@/views/admin/EconomySettingsView.vue'),
      },
      {
        path: 'harbor/settings',
        name: 'admin-harbor-settings',
        component: () => import('@/views/admin/HarborSettingsView.vue'),
      },
      {
        path: 'motorhome/settings',
        name: 'admin-motorhome-settings',
        component: () => import('@/views/admin/MotorhomeSettingsView.vue'),
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

  if (to.meta.requiresAdmin && !auth.hasRole('admin') && !auth.hasRole('board')) {
    return { path: '/' }
  }
})
