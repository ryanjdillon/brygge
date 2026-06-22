<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useAuthStore } from '@/stores/auth'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useFeatures } from '@/composables/useFeatures'
import { useMyInvoices, isOverdue, type MemberInvoice } from '@/composables/useMyInvoices'
import { usePaymentDataUpdatedAt } from '@/composables/usePaymentDataUpdatedAt'
import LastUpdated from '@/components/ui/LastUpdated.vue'
import { formatSlip } from '@/lib/slipSort'
import { formatNOK, formatDate } from '@/lib/format'
import { Download, FileText, FileType, X, ChevronUp, ChevronDown, Search } from 'lucide-vue-next'
import BoatCard from '@/components/boats/BoatCard.vue'

const { t } = useI18n()
const auth = useAuthStore()
const client = useApiClient()
const { isEnabled } = useFeatures()

const { data: dashboard, isLoading } = useQuery({
  queryKey: ['portal', 'dashboard'],
  queryFn: async () => unwrap(await client.GET('/api/v1/members/me/dashboard')),
})

const { data: boats, isLoading: boatsLoading } = useQuery({
  queryKey: ['portal', 'boats'],
  queryFn: async () => unwrap(await client.GET('/api/v1/members/me/boats')) ?? [],
})

const showInvoices = computed(() => isEnabled('accounting'))
const { unpaid, paid, isLoading: invoicesLoading } = useMyInvoices()
const { updatedAt: paymentsUpdatedAt } = usePaymentDataUpdatedAt()
const recentPaid = computed(() => paid.value.slice(0, 3))

const userName = computed(() => auth.user?.name ?? '')

const slipLabel = computed(() => {
  const slip = dashboard.value?.slip
  if (!slip) return t('portal.dashboard.noSlip')
  return formatSlip({ section: slip.location, number: slip.number })
})

const displayRole = computed(() => {
  const roles = auth.user?.roles ?? []
  if (roles.includes('admin')) return t('portal.dashboard.role.admin')
  if (roles.includes('board')) return t('portal.dashboard.role.board')
  if (roles.includes('slip_holder')) return t('portal.dashboard.role.slip_holder')
  if (roles.includes('member')) return t('portal.dashboard.role.member')
  if (roles.includes('applicant')) return t('portal.dashboard.role.applicant')
  return ''
})

function chipBorderClass(inv: MemberInvoice): string {
  if (inv.paid) return 'border-l-green-400'
  if (isOverdue(inv)) return 'border-l-red-400'
  return 'border-l-amber-400'
}

function chipBadgeClass(inv: MemberInvoice): string {
  if (inv.paid) return 'bg-green-100 text-green-800'
  if (isOverdue(inv)) return 'bg-red-100 text-red-800'
  return 'bg-amber-100 text-amber-800'
}

function chipBadgeLabel(inv: MemberInvoice): string {
  if (inv.paid) return t('portal.invoices.status.paid')
  if (isOverdue(inv)) return t('portal.invoices.status.overdue')
  return t('portal.invoices.status.unpaid')
}

// ── Documents section ─────────────────────────────────────────────────────────

interface FileDoc {
  id: string
  kind: 'file'
  title: string
  filename: string
  content_type: string
  size_bytes: number
  visibility: string
  created_at: string
}

interface AuthoredDoc {
  id: string
  kind: 'authored'
  title: string
  body_html: string
  visibility: string
  revision: number
  published_at: string | null
  created_at: string
  updated_at: string
}

const { data: docsData, isLoading: docsLoading } = useQuery({
  queryKey: ['portal', 'documents-section'],
  queryFn: async () => {
    const res = await fetch('/api/v1/portal/documents', { credentials: 'include' })
    if (!res.ok) throw new Error('failed')
    return res.json() as Promise<{ files: FileDoc[]; authored: AuthoredDoc[] }>
  },
})

const fileDocs = computed(() => docsData.value?.files ?? [])
const authoredDocs = computed(() => docsData.value?.authored ?? [])
const hasAnyDoc = computed(() => fileDocs.value.length > 0 || authoredDocs.value.length > 0)

// ── File doc preview ──────────────────────────────────────────────────────────

const previewDoc = ref<FileDoc | null>(null)

function isPreviewable(doc: FileDoc): boolean {
  return doc.content_type.startsWith('image/') || doc.content_type === 'application/pdf'
}

function fileContentUrl(doc: FileDoc, download = false): string {
  return `/api/v1/documents/${doc.id}/content${download ? '?download=1' : ''}`
}

function openFileDoc(doc: FileDoc) {
  if (isPreviewable(doc)) {
    previewDoc.value = doc
  } else {
    window.open(fileContentUrl(doc, true), '_blank')
  }
}

function closePreview() {
  previewDoc.value = null
}

// ── Authored doc modal ────────────────────────────────────────────────────────

const openDoc = ref<AuthoredDoc | null>(null)
const docSearch = ref('')
const matchIndex = ref(0)
const matchCount = ref(0)
const docContentRef = ref<HTMLElement | null>(null)

function openAuthoredDoc(doc: AuthoredDoc) {
  openDoc.value = doc
  docSearch.value = ''
  matchIndex.value = 0
  matchCount.value = 0
  nextTick(() => applyHighlights())
}

function closeDoc() {
  openDoc.value = null
  docSearch.value = ''
}

function applyHighlights() {
  const el = docContentRef.value
  if (!el) return

  el.querySelectorAll('mark.doc-search-hit').forEach(m => {
    const parent = m.parentNode!
    parent.replaceChild(document.createTextNode(m.textContent ?? ''), m)
    parent.normalize()
  })

  const query = docSearch.value.trim().toLowerCase()
  if (!query) {
    matchCount.value = 0
    matchIndex.value = 0
    return
  }

  let count = 0
  highlightNode(el, query, () => count++)
  matchCount.value = count
  matchIndex.value = count > 0 ? 1 : 0
  scrollToMatch(0)
}

function highlightNode(node: Node, query: string, onMatch: () => void) {
  if (node.nodeType === Node.TEXT_NODE) {
    const text = node.textContent ?? ''
    const lower = text.toLowerCase()
    let pos = 0
    const frag = document.createDocumentFragment()
    let found = false
    while (true) {
      const idx = lower.indexOf(query, pos)
      if (idx === -1) {
        frag.appendChild(document.createTextNode(text.slice(pos)))
        break
      }
      frag.appendChild(document.createTextNode(text.slice(pos, idx)))
      const mark = document.createElement('mark')
      mark.className = 'doc-search-hit bg-yellow-200 rounded px-0.5'
      mark.textContent = text.slice(idx, idx + query.length)
      frag.appendChild(mark)
      onMatch()
      pos = idx + query.length
      found = true
    }
    if (found) node.parentNode!.replaceChild(frag, node)
  } else if (node.nodeType === Node.ELEMENT_NODE && node.nodeName !== 'MARK') {
    Array.from(node.childNodes).forEach(child => highlightNode(child, query, onMatch))
  }
}

function scrollToMatch(targetIndex: number) {
  const el = docContentRef.value
  if (!el) return
  const marks = el.querySelectorAll<HTMLElement>('mark.doc-search-hit')
  marks.forEach((m, i) => {
    m.className = `doc-search-hit rounded px-0.5 ${i === targetIndex ? 'bg-orange-300' : 'bg-yellow-200'}`
  })
  if (marks[targetIndex]) {
    marks[targetIndex].scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }
}

function nextMatch() {
  if (matchCount.value === 0) return
  matchIndex.value = (matchIndex.value % matchCount.value) + 1
  scrollToMatch(matchIndex.value - 1)
}

function prevMatch() {
  if (matchCount.value === 0) return
  matchIndex.value = matchIndex.value === 1 ? matchCount.value : matchIndex.value - 1
  scrollToMatch(matchIndex.value - 1)
}

watch(docSearch, () => nextTick(() => applyHighlights()))
</script>

<template>
  <div class="max-w-3xl space-y-5">
    <h1 class="text-2xl font-bold text-gray-900">
      {{ t('portal.dashboard.welcome', { name: userName }) }}
    </h1>

    <div v-if="isLoading" class="text-sm text-gray-500">{{ t('common.loading') }}...</div>

    <template v-else>
      <!-- Membership overview -->
      <section class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 class="text-xs font-semibold uppercase tracking-wide text-gray-400">
          {{ t('portal.dashboard.membershipStatus') }}
        </h2>
        <div class="mt-4 grid grid-cols-2 gap-y-4 sm:grid-cols-3">
          <div>
            <p class="text-xs text-gray-500">Status</p>
            <p class="mt-0.5 font-semibold text-gray-900">{{ displayRole }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500">{{ t('portal.dashboard.slipInfo') }}</p>
            <p class="mt-0.5 font-semibold text-gray-900">{{ slipLabel }}</p>
          </div>
          <RouterLink
            v-if="dashboard?.queuePosition"
            to="/portal/waiting-list"
            class="group"
          >
            <p class="text-xs text-gray-500">{{ t('portal.dashboard.queuePosition') }}</p>
            <p class="mt-0.5 font-semibold text-blue-600 group-hover:underline">
              {{ t('portal.waitingList.positionOf', { position: dashboard.queuePosition, total: dashboard.queueTotal }) }}
            </p>
          </RouterLink>
        </div>
      </section>

      <!-- Upcoming bookings -->
      <section class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-900">{{ t('portal.dashboard.upcomingBookings') }}</h2>
          <RouterLink to="/portal/bookings" class="text-sm font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.viewAll') }}
          </RouterLink>
        </div>
        <p class="mt-2 text-sm text-gray-500">
          {{ dashboard?.upcomingBookingsCount
            ? t('portal.dashboard.bookingsCount', { count: dashboard.upcomingBookingsCount }, dashboard.upcomingBookingsCount)
            : t('portal.dashboard.noBookings') }}
        </p>
      </section>

      <!-- Fakturaar -->
      <section v-if="showInvoices" class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-900">{{ t('portal.invoices.title') }}</h2>
          <RouterLink to="/portal/invoices" class="text-sm font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.viewAll') }}
          </RouterLink>
        </div>
        <LastUpdated :at="paymentsUpdatedAt" class="mt-0.5" />

        <div v-if="invoicesLoading" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}...</div>
        <template v-else>
          <div v-if="unpaid.length" class="mt-3 space-y-2">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">
              {{ t('portal.invoices.unpaidHeading') }}
            </p>
            <RouterLink
              v-for="inv in unpaid"
              :key="inv.id"
              to="/portal/invoices"
              :class="[
                'flex items-center gap-3 rounded-lg border border-l-4 border-gray-100 bg-gray-50 px-3 py-2.5 transition hover:bg-white hover:shadow-sm',
                chipBorderClass(inv),
              ]"
            >
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium text-gray-900">
                  #{{ inv.invoice_number }}
                  <span v-if="inv.price_item_name || inv.description" class="font-normal text-gray-500">
                    — {{ inv.price_item_name || inv.description }}
                  </span>
                </p>
                <p class="text-xs text-gray-500">{{ t('portal.invoices.due') }}: {{ formatDate(inv.due_date) }}</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <span class="text-sm font-semibold tabular-nums text-gray-900">
                  {{ formatNOK(inv.total_amount) }}
                </span>
                <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', chipBadgeClass(inv)]">
                  {{ chipBadgeLabel(inv) }}
                </span>
              </div>
            </RouterLink>
          </div>

          <div v-if="recentPaid.length" :class="['space-y-2', unpaid.length ? 'mt-4' : 'mt-3']">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">
              {{ t('portal.invoices.recentPaidHeading') }}
            </p>
            <RouterLink
              v-for="inv in recentPaid"
              :key="inv.id"
              to="/portal/invoices"
              :class="[
                'flex items-center gap-3 rounded-lg border border-l-4 border-gray-100 bg-gray-50 px-3 py-2.5 transition hover:bg-white hover:shadow-sm',
                chipBorderClass(inv),
              ]"
            >
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm text-gray-500">
                  #{{ inv.invoice_number }}
                  <span v-if="inv.price_item_name || inv.description">
                    — {{ inv.price_item_name || inv.description }}
                  </span>
                </p>
                <p class="text-xs text-gray-400">{{ t('portal.invoices.due') }}: {{ formatDate(inv.due_date) }}</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <span class="text-sm tabular-nums text-gray-500">{{ formatNOK(inv.total_amount) }}</span>
                <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', chipBadgeClass(inv)]">
                  {{ chipBadgeLabel(inv) }}
                </span>
              </div>
            </RouterLink>
          </div>

          <p v-if="!unpaid.length && !paid.length" class="mt-3 text-sm text-gray-500">
            {{ t('portal.invoices.none') }}
          </p>
        </template>
      </section>

      <!-- Båtane mine -->
      <section class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-900">{{ t('portal.dashboard.myBoats') }}</h2>
          <RouterLink to="/portal/boats" class="text-sm font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.manageBoats') }}
          </RouterLink>
        </div>
        <div v-if="boatsLoading" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}...</div>
        <div v-else-if="boats && boats.length" class="mt-3 space-y-3">
          <BoatCard v-for="boat in boats" :key="boat.id" :boat="boat" />
        </div>
        <p v-else class="mt-3 text-sm text-gray-500">
          {{ t('portal.dashboard.noBoats') }}
          <RouterLink to="/portal/boats" class="font-medium text-blue-600 hover:text-blue-700">
            {{ t('portal.dashboard.addBoat') }}
          </RouterLink>
        </p>
      </section>
      <!-- Documents -->
      <section v-if="hasAnyDoc || docsLoading" class="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 class="text-sm font-semibold text-gray-900">{{ t('portal.documents.title') }}</h2>
        <div v-if="docsLoading" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}...</div>
        <template v-else>
          <ul class="mt-3 divide-y divide-gray-100">
            <li
              v-for="doc in fileDocs"
              :key="doc.id"
              class="flex items-center justify-between py-2.5"
            >
              <div class="flex min-w-0 items-center gap-2">
                <FileType class="h-4 w-4 shrink-0 text-gray-400" />
                <span class="truncate text-sm font-medium text-gray-900">{{ doc.title }}</span>
                <span class="shrink-0 text-xs text-gray-400">{{ formatDate(doc.created_at) }}</span>
              </div>
              <div class="ml-3 flex shrink-0 items-center gap-2">
                <button
                  v-if="isPreviewable(doc)"
                  type="button"
                  class="rounded-md border border-gray-300 bg-white px-2.5 py-1 text-xs text-gray-700 hover:bg-gray-50"
                  @click="openFileDoc(doc)"
                >
                  {{ t('portal.documents.open') }}
                </button>
                <a
                  :href="fileContentUrl(doc, true)"
                  class="flex items-center gap-1 rounded-md border border-gray-300 bg-white px-2.5 py-1 text-xs text-gray-700 hover:bg-gray-50"
                >
                  <Download class="h-3.5 w-3.5" />
                  {{ t('portal.documents.download') }}
                </a>
              </div>
            </li>
            <li
              v-for="doc in authoredDocs"
              :key="doc.id"
              class="flex items-center justify-between py-2.5"
            >
              <div class="flex min-w-0 items-center gap-2">
                <FileText class="h-4 w-4 shrink-0 text-blue-400" />
                <span class="truncate text-sm font-medium text-gray-900">{{ doc.title }}</span>
                <span class="shrink-0 text-xs text-gray-400">{{ formatDate(doc.created_at) }}</span>
              </div>
              <button
                type="button"
                class="ml-3 shrink-0 rounded-md border border-gray-300 bg-white px-2.5 py-1 text-xs text-gray-700 hover:bg-gray-50"
                @click="openAuthoredDoc(doc)"
              >
                {{ t('portal.documents.open') }}
              </button>
            </li>
          </ul>
        </template>
      </section>
    </template>
  </div>

  <!-- Authored document modal -->
  <Teleport to="body">
    <div
      v-if="openDoc"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 sm:p-6"
      @click.self="closeDoc"
    >
      <div class="flex max-h-[85vh] w-full max-w-5xl flex-col overflow-hidden rounded-xl bg-white shadow-2xl">
        <!-- Header -->
        <div class="flex items-center gap-3 border-b border-gray-200 px-6 py-4">
          <div class="min-w-0 flex-1">
            <h2 class="text-lg font-semibold text-gray-900">{{ openDoc.title }}</h2>
            <p v-if="openDoc.revision > 0" class="mt-0.5 text-xs text-gray-400">
              {{ t('admin.documents.revisionN', { n: openDoc.revision }) }}
              <template v-if="openDoc.published_at">
                · {{ formatDate(openDoc.published_at) }}
              </template>
            </p>
          </div>
          <button type="button" class="shrink-0 rounded p-1 text-gray-400 hover:text-gray-700" @click="closeDoc">
            <X class="h-5 w-5" />
          </button>
        </div>
        <!-- Search bar -->
        <div class="flex items-center gap-2 border-b border-gray-100 bg-gray-50 px-6 py-2">
          <Search class="h-4 w-4 shrink-0 text-gray-400" />
          <input
            v-model="docSearch"
            type="search"
            :placeholder="t('portal.documents.searchPlaceholder')"
            class="min-w-0 flex-1 bg-transparent text-sm focus:outline-none"
            @keydown.enter.prevent="nextMatch"
            @keydown.shift.enter.prevent="prevMatch"
          />
          <span v-if="docSearch && matchCount > 0" class="shrink-0 text-xs text-gray-500">
            {{ matchIndex }} / {{ matchCount }}
          </span>
          <span v-else-if="docSearch && matchCount === 0" class="shrink-0 text-xs text-red-400">
            {{ t('portal.documents.noMatches') }}
          </span>
          <button
            v-if="docSearch"
            type="button"
            class="shrink-0 rounded p-0.5 text-gray-400 hover:text-gray-700"
            @click="prevMatch"
          >
            <ChevronUp class="h-4 w-4" />
          </button>
          <button
            v-if="docSearch"
            type="button"
            class="shrink-0 rounded p-0.5 text-gray-400 hover:text-gray-700"
            @click="nextMatch"
          >
            <ChevronDown class="h-4 w-4" />
          </button>
        </div>
        <!-- Content -->
        <div class="flex-1 overflow-y-auto px-6 py-5">
          <div
            ref="docContentRef"
            class="prose prose-sm max-w-none"
            v-html="openDoc.body_html"
          />
        </div>
      </div>
    </div>
  </Teleport>

  <!-- File document preview modal -->
  <Teleport to="body">
    <div
      v-if="previewDoc"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 sm:p-6"
      @click.self="closePreview"
    >
      <div class="flex max-h-[90vh] w-full max-w-5xl flex-col overflow-hidden rounded-xl bg-white shadow-2xl">
        <div class="flex items-center gap-3 border-b border-gray-200 px-5 py-3">
          <span class="min-w-0 flex-1 truncate text-sm font-semibold text-gray-900">{{ previewDoc.title }}</span>
          <a
            :href="fileContentUrl(previewDoc, true)"
            class="flex shrink-0 items-center gap-1 rounded-md border border-gray-300 bg-white px-2.5 py-1 text-xs text-gray-700 hover:bg-gray-50"
          >
            <Download class="h-3.5 w-3.5" />
            {{ t('portal.documents.download') }}
          </a>
          <button type="button" class="shrink-0 rounded p-1 text-gray-400 hover:text-gray-700" @click="closePreview">
            <X class="h-5 w-5" />
          </button>
        </div>
        <div class="flex flex-1 items-center justify-center overflow-auto bg-gray-50 p-4">
          <img
            v-if="previewDoc.content_type.startsWith('image/')"
            :src="fileContentUrl(previewDoc)"
            :alt="previewDoc.title"
            class="max-h-full max-w-full object-contain"
          />
          <iframe
            v-else
            :src="fileContentUrl(previewDoc)"
            :title="previewDoc.title"
            class="h-[80vh] w-full border-0"
          />
        </div>
      </div>
    </div>
  </Teleport>
</template>
