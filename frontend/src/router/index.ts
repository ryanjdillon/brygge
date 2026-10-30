import { createRouter, createWebHistory, type RouteLocationNormalized, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { canOpenAdminRoute } from '@/lib/adminAccess'

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    requiresAdmin?: boolean
    /** Developer and Site pages: site admins only, not the wider board. */
    adminOnly?: boolean
    /** Renders without the navbar and footer, e.g. print sheets. */
    bare?: boolean
  }
}

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
    path: '/faq',
    component: () => import('@/views/FaqView.vue'),
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
    // More specific than /betal/:token, so it must be registered first: the
    // durable per-member payables page (BRY-275).
    path: '/betal/bruker/:token',
    component: () => import('@/views/PayablesView.vue'),
  },
  {
    // Where a payment provider returns the payer: polls the order until the
    // webhook has settled it. Listed before /betal/:token, like the payables
    // page above.
    path: '/betal/:token/kvittering',
    component: () => import('@/views/PaymentReceiptView.vue'),
  },
  {
    path: '/betal/:token',
    component: () => import('@/views/PayInvoiceView.vue'),
  },
  {
    path: '/qr/:token',
    component: () => import('@/views/QrCheckoutView.vue'),
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
        path: 'invoices',
        name: 'portal-invoices',
        component: () => import('@/views/portal/InvoicesView.vue'),
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
    // Print sheet of harbor QR cards (BRY-271). Outside AdminLayout and
    // `bare`, so the sidebar, navbar and footer stay off the printed page.
    path: '/admin/harbor/payment-targets/print',
    name: 'admin-harbor-targets-print',
    component: () => import('@/views/admin/HarborQrPrintView.vue'),
    meta: { requiresAuth: true, requiresAdmin: true, bare: true },
  },
  {
    path: '/admin',
    component: () => import('@/views/admin/AdminLayout.vue'),
    meta: { requiresAuth: true, requiresAdmin: true },
    children: [
      {
        path: '',
        redirect: '/admin/inbox',
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
        path: 'harbor/recent-payments',
        name: 'admin-recent-payments',
        component: () => import('@/views/admin/RecentPaymentsView.vue'),
      },
      {
        // Reached from the Payments page; no sidebar item, and outside the
        // accounting and economy prefixes, so no fresh-TOTP navigation gate.
        path: 'harbor/recent-payments/filters',
        name: 'admin-payment-filters',
        component: () => import('@/views/admin/PaymentFiltersView.vue'),
      },
      {
        path: 'harbor/payment-targets',
        name: 'admin-harbor-targets',
        component: () => import('@/views/admin/HarborTargetsView.vue'),
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
        // Standalone broadcast page retired — member announcements now live
        // in the inbox compose flow + Broadcasts tab (BRY-168). Old bookmarks
        // land on the inbox.
        path: 'communication',
        redirect: '/admin/inbox',
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
        meta: { adminOnly: true },
      },
      {
        path: 'gdpr',
        name: 'admin-gdpr',
        component: () => import('@/views/admin/GDPRAdminView.vue'),
        meta: { adminOnly: true },
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
        meta: { adminOnly: true },
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
        meta: { adminOnly: true },
      },
      {
        path: 'settings/security',
        name: 'admin-security-settings',
        component: () => import('@/views/admin/SecuritySettingsView.vue'),
        meta: { adminOnly: true },
      },
      {
        path: 'settings/feedback',
        redirect: '/admin/settings/site',
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
        meta: { adminOnly: true },
      },
      {
        path: 'settings/faq',
        name: 'admin-faq-settings',
        component: () => import('@/views/admin/FaqSettingsView.vue'),
        meta: { adminOnly: true },
      },
      {
        // Legacy URL — older bookmarks land here. Redirects to the
        // canonical /admin/settings/site path. Safe to delete after
        // a release or two. See DIL-358.
        path: 'accounting/settings',
        redirect: '/admin/settings/site',
      },
      {
        path: 'accounting/reports',
        name: 'admin-accounting-reports',
        component: () => import('@/views/admin/AccountingReportsView.vue'),
      },
      {
        path: 'accounting/bank-imports',
        name: 'admin-bank-imports',
        component: () => import('@/views/admin/BankImportsView.vue'),
      },
      {
        path: 'accounting/vipps-queue',
        name: 'admin-vipps-queue',
        component: () => import('@/views/admin/VippsQueueView.vue'),
      },
      {
        path: 'dev/query',
        name: 'admin-dev-query',
        component: () => import('@/views/admin/DevQueryView.vue'),
        meta: { adminOnly: true },
      },
      {
        path: 'dev/tokens',
        name: 'admin-dev-tokens',
        component: () => import('@/views/admin/DevTokensView.vue'),
        meta: { adminOnly: true },
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
        redirect: { name: 'admin-harbor-settings' },
      },
    ],
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

export async function guardRoute(to: RouteLocationNormalized) {
  const auth = useAuthStore()
  await auth.ready

  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { path: '/login' }
  }

  // Board roles may view the admin area; Developer and Site pages
  // (meta.adminOnly) stay with site admins (BRY-372). Matched-route meta
  // is merged, so a child inherits requiresAdmin from /admin.
  if (!canOpenAdminRoute(to.meta, auth.user?.roles ?? [])) {
    return { path: auth.hasAdminRole ? '/admin' : '/' }
  }
}

router.beforeEach(guardRoute)

// A deploy replaces the hashed route chunks, so a tab opened before the
// deploy fails to lazy-load a route ("Failed to fetch dynamically imported
// module"). Reload once to pull the fresh index.html with the new hashes.
// The sessionStorage flag prevents a reload loop if the chunk is genuinely
// gone; it's cleared on the next successful navigation.
const CHUNK_RELOAD_KEY = 'chunk-reload'

function reloadOnStaleChunk(error: unknown) {
  const message = error instanceof Error ? error.message : String(error)
  const isChunkError =
    /failed to fetch dynamically imported module|error loading dynamically imported module|importing a module script failed/i.test(
      message,
    )
  if (!isChunkError) return
  if (sessionStorage.getItem(CHUNK_RELOAD_KEY)) return
  sessionStorage.setItem(CHUNK_RELOAD_KEY, '1')
  window.location.reload()
}

router.onError(reloadOnStaleChunk)
router.afterEach(() => sessionStorage.removeItem(CHUNK_RELOAD_KEY))
window.addEventListener('vite:preloadError', (event) => reloadOnStaleChunk(event))
