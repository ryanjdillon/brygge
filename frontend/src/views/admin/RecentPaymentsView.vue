<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useApi } from '@/composables/useApi'
import { formatNOK as nok } from '@/lib/format'
import { RefreshCw, Inbox, Search, Sailboat, Van, Forklift, FileText, Store, CircleDollarSign, TriangleAlert, ExternalLink } from 'lucide-vue-next'
import PaymentMethodChip, { type PaymentMethod } from '@/components/admin/PaymentMethodChip.vue'
import RefundStateChip from '@/components/admin/RefundStateChip.vue'
import { disputeChipKind, refundChipKind } from '@/lib/refundState'
import RefundOrderButton from '@/components/admin/RefundOrderButton.vue'
import { useCan } from '@/composables/useCan'

const { t } = useI18n()
const { can } = useCan()
const canRefund = can('money')
const { fetchApi } = useApi()
const route = useRoute()
const router = useRouter()

interface Payment {
  id: string
  paid_at: string
  amount: number
  payer: string
  summary: string
  kind: 'guest' | 'motorhome' | 'slipping' | 'faktura' | 'merch' | 'other'
  channel: string
  method: PaymentMethod
  card_brand: string | null
  // Set by the server for a treasurer or admin, for a card payment with
  // money left to refund.
  can_refund?: boolean
  // What refunds the provider confirmed took back (none | partial | full),
  // and the latest dispute in the provider's words.
  refund_state?: string
  refunded_amount?: number
  dispute_status?: string | null
  dispute_due_by?: string | null
  // Stripe Dashboard pages, sent to a treasurer or admin only.
  payment_url?: string
  dispute_url?: string
}

const payments = ref<Payment[]>([])
// Card disputes waiting for the club's answer in Stripe, with the Dashboard
// list of them; sent to a treasurer or admin only.
const openDisputes = ref(0)
const openDisputesUrl = ref('')
// The Stripe and Vipps back offices, as the server built them; a provider the
// club has not configured has no entry, and the server sends none to a role
// that cannot use them.
const providerLinks = ref<{ stripe?: string; vipps?: string }>({})
const providerLinkList = computed(() =>
  (['stripe', 'vipps'] as const).flatMap((id) => (providerLinks.value[id] ? [{ id, href: providerLinks.value[id]! }] : [])),
)
const loading = ref(false)
// The last load failed; set instead of an empty list, which would read as "no
// payments" for a list that is merely unavailable.
const loadFailed = ref(false)
// The server cut the list at the limit: older payments match but are not shown.
const hasMore = ref(false)
const since = ref('')
const until = ref('')
const search = ref('')

// Filters the harbor master picks from, additive: every chip pressed adds its
// kinds to the list, and "all" (no kind filter) is what is left when none is.
// "harbor" is what the harbor is paid for on the spot (guest berths and
// motorhomes); each id maps to the API's kind list ('' for every kind).
const filters = {
  all: '',
  harbor: 'guest,motorhome',
  guest: 'guest',
  motorhome: 'motorhome',
  slipping: 'slipping',
  faktura: 'faktura',
  merch: 'merch',
  other: 'other',
} as const
type FilterId = keyof typeof filters
const filterIds = Object.keys(filters) as FilterId[]
const DEFAULT_KINDS: FilterId[] = ['all']
// A key of its own: a single choice stored under the previous key does not
// apply to additive filters, so such a device starts on "all".
const STORAGE_KEY = 'recentPayments.kinds'

// A membership test on the id list, not `in`: `?kind=constructor` must not
// pass as a filter.
function isFilterId(v: unknown): v is FilterId {
  return typeof v === 'string' && (filterIds as string[]).includes(v)
}

// "guest,motorhome" to its valid, de-duplicated ids; null when none is valid.
// "all" excludes every other id, since it is the absence of a filter.
function parseKinds(v: unknown): FilterId[] | null {
  if (typeof v !== 'string') return null
  const ids = [...new Set(v.split(',').filter(isFilterId))]
  if (ids.length === 0) return null
  return ids.includes('all') ? ['all'] : ids
}

function storedKinds(): FilterId[] | null {
  try {
    return parseKinds(localStorage.getItem(STORAGE_KEY))
  } catch {
    return null
  }
}

// The URL is the source of truth, so a shared or reloaded link shows the
// same list; the last choice on this device fills in when the URL has none.
const kindKey = computed(() => {
  const q = route.query.kind
  return (parseKinds(Array.isArray(q) ? q[0] : q) ?? storedKinds() ?? DEFAULT_KINDS).join(',')
})
const kinds = computed(() => kindKey.value.split(',') as FilterId[])

// Pressing a pressed chip removes it; "all" clears the rest, and removing the
// last one falls back to "all".
function nextKinds(current: FilterId[], id: FilterId): FilterId[] {
  if (id === 'all') return ['all']
  const rest = current.filter((k) => k !== 'all')
  const next = rest.includes(id) ? rest.filter((k) => k !== id) : [...rest, id]
  return next.length ? next : ['all']
}

function selectFilter(id: FilterId) {
  const next = nextKinds(kinds.value, id).join(',')
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // Private browsing can refuse storage; the URL still carries the choice.
  }
  router.replace({ query: { ...route.query, kind: next } })
}

// The API's kind list for the pressed chips; empty means every kind.
function apiKinds(): string[] {
  if (kinds.value.includes('all')) return []
  return [...new Set(kinds.value.flatMap((id) => filters[id].split(',')))]
}

const kindMeta: Record<string, { icon: any; classes: string }> = {
  guest: { icon: Sailboat, classes: 'bg-blue-50 text-blue-700 ring-blue-200' },
  motorhome: { icon: Van, classes: 'bg-teal-50 text-teal-700 ring-teal-200' },
  slipping: { icon: Forklift, classes: 'bg-indigo-50 text-indigo-700 ring-indigo-200' },
  faktura: { icon: FileText, classes: 'bg-slate-100 text-slate-700 ring-slate-200' },
  merch: { icon: Store, classes: 'bg-amber-50 text-amber-700 ring-amber-200' },
  other: { icon: CircleDollarSign, classes: 'bg-gray-50 text-gray-600 ring-gray-200' },
}

// Without a chosen range the list is the last 7 days, split into today and
// the days before; a range from the date fields replaces that.
const RECENT_DAYS = 7
// The most the server returns for one request; used for the recent view and
// for a chosen range alike.
const LIST_LIMIT = 200
const customRange = computed(() => !!since.value || !!until.value)

function isoDate(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function startOfToday(): Date {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  return d
}

interface PaymentGroup {
  id: 'today' | 'last7' | 'range'
  items: Payment[]
}

const groups = computed<PaymentGroup[]>(() => {
  if (customRange.value) return payments.value.length ? [{ id: 'range', items: payments.value }] : []
  const midnight = startOfToday().getTime()
  const today = payments.value.filter((p) => new Date(p.paid_at).getTime() >= midnight)
  const earlier = payments.value.filter((p) => new Date(p.paid_at).getTime() < midnight)
  return [
    { id: 'today' as const, items: today },
    { id: 'last7' as const, items: earlier },
  ].filter((g) => g.items.length > 0)
})

function clearRange() {
  since.value = ''
  until.value = ''
  load()
}

let searchTimer: ReturnType<typeof setTimeout> | undefined

// Only the newest request may fill the list: on a slow connection an answer
// for a filter tapped earlier can arrive after the current one.
let loadSeq = 0

async function load() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const p = new URLSearchParams()
    const kindList = apiKinds()
    if (kindList.length) p.set('kind', kindList.join(','))
    p.set('limit', String(LIST_LIMIT))
    if (customRange.value) {
      if (since.value) p.set('since', since.value)
      if (until.value) p.set('until', until.value)
    } else {
      const from = startOfToday()
      from.setDate(from.getDate() - RECENT_DAYS)
      p.set('since', isoDate(from))
    }
    if (search.value.trim()) p.set('q', search.value.trim())
    const res = await fetchApi<{
      payments: Payment[]
      has_more?: boolean
      open_disputes?: number
      open_disputes_url?: string
      provider_links?: { stripe?: string; vipps?: string }
    }>(
      `/api/v1/harbor/recent-payments?${p.toString()}`,
    )
    if (seq === loadSeq) {
      payments.value = res?.payments ?? []
      hasMore.value = res?.has_more ?? false
      loadFailed.value = false
      openDisputes.value = res?.open_disputes ?? 0
      openDisputesUrl.value = res?.open_disputes_url ?? ''
      providerLinks.value = res?.provider_links ?? {}
    }
  } catch {
    // Rows from an earlier load stay on screen, with the failure above them.
    if (seq === loadSeq) loadFailed.value = true
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function onSearchInput() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(load, 300)
}

function fmtWhen(s: string): string {
  if (!s) return '—'
  return new Date(s).toLocaleString('nb-NO', {
    day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit',
  })
}

watch(kindKey, load)

onMounted(async () => {
  await router.isReady()
  if (route.query.kind !== kindKey.value) {
    router.replace({ query: { ...route.query, kind: kindKey.value } })
  }
  load()
})
</script>

<template>
  <div class="mx-auto max-w-3xl px-3 py-6 sm:px-6">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h1 class="text-xl font-bold text-slate-900 sm:text-2xl">{{ t('recentPayments.title') }}</h1>
        <p class="mt-1 text-sm text-slate-500">{{ t('recentPayments.subtitle') }}</p>
      </div>
      <button
        class="flex min-h-11 min-w-11 shrink-0 items-center justify-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
        :disabled="loading"
        :aria-label="t('recentPayments.refresh')"
        @click="load"
      >
        <RefreshCw class="h-4 w-4" :class="loading ? 'animate-spin' : ''" />
        <span class="hidden sm:inline">{{ t('recentPayments.refresh') }}</span>
      </button>
    </div>

    <!-- Provider back offices, so they are one tap away without a workflow of their own -->
    <nav
      v-if="providerLinkList.length"
      class="mt-4 flex flex-wrap gap-2"
      :aria-label="t('recentPayments.providerLinks.label')"
      data-testid="provider-links"
    >
      <a
        v-for="link in providerLinkList"
        :key="link.id"
        :href="link.href"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex min-h-11 items-center gap-1.5 rounded-md border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
        :data-provider="link.id"
      >
        {{ t(`recentPayments.providerLinks.${link.id}`) }}
        <ExternalLink class="h-3.5 w-3.5" aria-hidden="true" />
      </a>
    </nav>

    <!-- Disputes waiting for an answer: the evidence is given in Stripe -->
    <a
      v-if="openDisputes > 0 && openDisputesUrl"
      :href="openDisputesUrl"
      target="_blank"
      rel="noopener noreferrer"
      class="mt-4 flex min-h-11 items-center gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm font-medium text-red-800 hover:bg-red-100"
      data-testid="open-disputes"
    >
      <TriangleAlert class="h-4 w-4 shrink-0" aria-hidden="true" />
      <span class="min-w-0 flex-1">{{ t('admin.refund.chip.disputesWaiting', { count: openDisputes }) }}</span>
      <span class="inline-flex shrink-0 items-center gap-1 font-semibold">
        {{ t('admin.refund.chip.answerInStripe') }}
        <ExternalLink class="h-3.5 w-3.5" aria-hidden="true" />
      </span>
    </a>

    <!-- Filters -->
    <div class="mt-4 space-y-3">
      <div class="flex flex-wrap gap-2" data-testid="kind-filters">
        <button
          v-for="id in filterIds"
          :key="id"
          type="button"
          class="min-h-11 rounded-full px-4 text-sm font-medium ring-1 ring-inset"
          :class="kinds.includes(id) ? 'bg-slate-900 text-white ring-slate-900' : 'bg-white text-slate-600 ring-slate-200 hover:bg-slate-50'"
          :aria-pressed="kinds.includes(id)"
          :data-filter="id"
          @click="selectFilter(id)"
        >
          {{ t('recentPayments.kind.' + id) }}
        </button>
      </div>

      <div class="relative">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
        <input
          v-model="search"
          type="search"
          :placeholder="t('recentPayments.searchPlaceholder')"
          class="min-h-11 w-full rounded-md border border-slate-300 py-2 pl-9 pr-3 text-sm"
          @input="onSearchInput"
        />
      </div>

      <div class="flex gap-2">
        <label class="flex-1 text-xs text-slate-500">
          {{ t('recentPayments.since') }}
          <input v-model="since" type="date" class="mt-1 min-h-11 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" @change="load" />
        </label>
        <label class="flex-1 text-xs text-slate-500">
          {{ t('recentPayments.until') }}
          <input v-model="until" type="date" class="mt-1 min-h-11 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" @change="load" />
        </label>
      </div>
      <button
        v-if="customRange"
        type="button"
        class="min-h-11 rounded-md border border-slate-300 bg-white px-4 text-sm font-medium text-slate-700 hover:bg-slate-50"
        data-testid="clear-range"
        @click="clearRange"
      >
        {{ t('recentPayments.clearRange') }}
      </button>
    </div>

    <!-- Load failed -->
    <div
      v-if="loadFailed"
      role="alert"
      class="mt-5 flex flex-wrap items-center gap-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800"
      data-testid="load-error"
    >
      <TriangleAlert class="h-4 w-4 shrink-0" aria-hidden="true" />
      <span class="min-w-0 flex-1">{{ t('recentPayments.loadError') }}</span>
      <button
        type="button"
        class="min-h-11 shrink-0 rounded-md border border-red-300 bg-white px-4 text-sm font-medium text-red-800 hover:bg-red-100"
        :disabled="loading"
        data-testid="load-retry"
        @click="load"
      >
        {{ t('recentPayments.retry') }}
      </button>
    </div>

    <!-- Empty -->
    <div v-if="!loading && !loadFailed && payments.length === 0" class="mt-10 flex flex-col items-center py-10 text-center">
      <Inbox class="h-12 w-12 text-slate-300" />
      <p class="mt-3 text-sm text-slate-500">{{ t(customRange ? 'recentPayments.empty' : 'recentPayments.emptyRecent') }}</p>
    </div>

    <!-- Payment cards (mobile-first, work on desktop too), grouped by day range -->
    <template v-else>
    <section
      v-for="group in groups"
      :key="group.id"
      class="mt-5"
      :data-testid="`payment-group-${group.id}`"
    >
      <h2 class="text-xs font-semibold uppercase tracking-wide text-slate-500">
        {{ t(`recentPayments.group.${group.id}`) }}
      </h2>
      <ul class="mt-2 space-y-2">
        <li
          v-for="p in group.items"
          :key="p.id"
          class="flex items-center gap-3 rounded-lg border border-slate-200 bg-white p-3"
        >
          <span
            class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full ring-1 ring-inset"
            :class="(kindMeta[p.kind] || kindMeta.other).classes"
          >
            <component :is="(kindMeta[p.kind] || kindMeta.other).icon" class="h-4 w-4" />
          </span>
          <div class="min-w-0 flex-1">
            <div class="flex items-baseline justify-between gap-2">
              <p class="truncate font-medium text-slate-900">{{ p.payer || t('recentPayments.anonymous') }}</p>
              <p class="shrink-0 font-semibold text-slate-900">{{ nok(p.amount) }}</p>
            </div>
            <div class="mt-0.5 flex items-center justify-between gap-2 text-xs text-slate-500">
              <span class="truncate">{{ p.summary || t('recentPayments.kind.' + p.kind) }}</span>
              <span class="shrink-0">{{ fmtWhen(p.paid_at) }}</span>
            </div>
            <div class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1">
              <PaymentMethodChip :method="p.method" :card-brand="p.card_brand" />
              <RefundStateChip
                v-if="refundChipKind(p.refund_state)"
                :kind="refundChipKind(p.refund_state)!"
                :amount="p.refunded_amount"
                :href="p.payment_url"
              />
              <RefundStateChip
                v-if="disputeChipKind(p.dispute_status)"
                :kind="disputeChipKind(p.dispute_status)!"
                :due-by="p.dispute_due_by"
                :href="p.dispute_url"
              />
              <RefundOrderButton v-if="canRefund && p.can_refund" class="ml-auto" :order-id="p.id" @refunded="load" />
            </div>
          </div>
        </li>
      </ul>
    </section>
    <p
      v-if="hasMore"
      class="mt-4 rounded-md bg-slate-50 px-3 py-2 text-sm text-slate-600"
      data-testid="truncated-note"
    >
      {{ t('recentPayments.truncated', { count: payments.length }) }}
    </p>
    </template>
  </div>
</template>
