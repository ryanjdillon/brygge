<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useApi } from '@/composables/useApi'
import { formatNOK as nok } from '@/lib/format'
import { RefreshCw, Inbox, Search, SlidersHorizontal, TriangleAlert, ExternalLink } from 'lucide-vue-next'
import PaymentMethodChip, { type PaymentMethod } from '@/components/admin/PaymentMethodChip.vue'
import RefundStateChip from '@/components/admin/RefundStateChip.vue'
import { disputeChipKind, refundChipKind } from '@/lib/refundState'
import RefundOrderButton from '@/components/admin/RefundOrderButton.vue'
import { useCan } from '@/composables/useCan'
import PaymentGroupIcon from '@/components/admin/PaymentGroupIcon.vue'
import { groupLabel, type FilterGroupRef } from '@/lib/paymentFilterIcons'

const { t } = useI18n()
const { can } = useCan()
const canRefund = can('money')
// Who may change the filter groups; everyone else reads the page and the groups.
const canEditFilters = can('paymentFilters')
// The group settings, reached from here (no sidebar item).
const FILTERS_PATH = '/admin/harbor/recent-payments/filters'
const { fetchApi } = useApi()
const route = useRoute()
const router = useRouter()

interface Payment {
  id: string
  paid_at: string
  amount: number
  payer: string
  summary: string
  // The slug of the filter group the payment is in.
  kind: string
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

// The chips are the club's filter groups (BRY-522), as the server sends them
// with the payments. Pressing a chip adds its group to the filter, and "all"
// (no kind filter) is what is left when none is pressed. The ids are group
// slugs; the seeded groups keep the old kind ids, so links and the choice
// stored on a device keep working.
const ALL = 'all'
const DEFAULT_KINDS = [ALL]
// The same key as before the groups: a stored slug that still exists keeps
// filtering, and one that does not is dropped after the first answer.
const STORAGE_KEY = 'recentPayments.kinds'
// What a slug looks like (paymentfilters.Slugify); anything else in a link or
// in storage is dropped before it reaches the server.
const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,39}$/
// The "harbor" chip from before the groups: guest berths and motorhomes.
const LEGACY_HARBOR = ['guest', 'motorhome']

interface FilterGroup extends FilterGroupRef {
  builtin: string | null
}

// The chips, in the order the server sent; empty until the first answer.
const filterGroups = ref<FilterGroup[]>([])
// The first answer has arrived; before it the chips are placeholders.
const filterGroupsLoaded = ref(false)
// Active price items in no group, sent to those who may assign them.
const unassignedItems = ref(0)

// "guest,motorhome" to its slug-shaped, de-duplicated ids; null when none is.
// "all" excludes every other id, since it is the absence of a filter, and the
// legacy "harbor" expands to its two groups. Whether a slug is a group is
// decided against the server's list once it has answered.
function parseKinds(v: unknown): string[] | null {
  if (typeof v !== 'string') return null
  const ids = [
    ...new Set(
      v
        .split(',')
        .flatMap((id) => (id === 'harbor' ? LEGACY_HARBOR : [id]))
        .filter((id) => SLUG_RE.test(id)),
    ),
  ]
  if (ids.length === 0) return null
  return ids.includes(ALL) ? [ALL] : ids
}

function storedKinds(): string[] | null {
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
const kinds = computed(() => kindKey.value.split(','))

// Pressing a pressed chip removes it; "all" clears the rest, and removing the
// last one falls back to "all".
function nextKinds(current: string[], id: string): string[] {
  if (id === ALL) return [ALL]
  const rest = current.filter((k) => k !== ALL)
  const next = rest.includes(id) ? rest.filter((k) => k !== id) : [...rest, id]
  return next.length ? next : [ALL]
}

function writeKinds(next: string[]) {
  const key = next.join(',')
  try {
    localStorage.setItem(STORAGE_KEY, key)
  } catch {
    // Private browsing can refuse storage; the URL still carries the choice.
  }
  router.replace({ query: { ...route.query, kind: key } })
}

function selectFilter(id: string) {
  writeKinds(nextKinds(kinds.value, id))
}

// The API's kind list for the pressed chips; empty means every kind.
function apiKinds(): string[] {
  return kinds.value.includes(ALL) ? [] : kinds.value
}

// Drops the requested slugs that are not groups (a deleted or disabled group,
// a stale link) and rewrites the URL and storage, so the page does not stay
// filtered on a chip it cannot show. Only once the server has listed the
// groups.
function pruneKinds() {
  if (!filterGroupsLoaded.value || kinds.value.includes(ALL)) return
  const known = new Set(filterGroups.value.map((g) => g.slug))
  const kept = kinds.value.filter((k) => known.has(k))
  if (kept.length !== kinds.value.length) writeKinds(kept.length ? kept : [ALL])
}

// The group a payment is in; a slug the list does not have reads as Annet.
function groupOf(kind: string): FilterGroup | undefined {
  return filterGroups.value.find((g) => g.slug === kind)
}
// The fallback group as the club has it, renamed or re-iconed if it was.
const fallbackGroup = computed(() => filterGroups.value.find((g) => g.builtin === 'other'))
function kindIcon(kind: string): string {
  return (groupOf(kind) ?? fallbackGroup.value)?.icon ?? 'circle-dollar-sign'
}
function kindLabel(kind: string): string {
  const g = groupOf(kind) ?? fallbackGroup.value
  return g ? groupLabel(g, t) : t('recentPayments.kind.other')
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
      groups?: FilterGroup[]
      unassigned_items?: number
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
      unassignedItems.value = res?.unassigned_items ?? 0
      if (res?.groups) {
        filterGroups.value = res.groups
        filterGroupsLoaded.value = true
        pruneKinds()
      }
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
      <div class="flex shrink-0 gap-2">
        <!-- Every admin viewer may open the groups; only writers can change them -->
        <RouterLink
          :to="FILTERS_PATH"
          class="flex min-h-11 min-w-11 items-center justify-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
          :aria-label="t('recentPayments.filters.link')"
          data-testid="filters-link"
        >
          <SlidersHorizontal class="h-4 w-4" aria-hidden="true" />
          <span class="hidden sm:inline">{{ t('recentPayments.filters.link') }}</span>
        </RouterLink>
        <button
          class="flex min-h-11 min-w-11 items-center justify-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
          :disabled="loading"
          :aria-label="t('recentPayments.refresh')"
          @click="load"
        >
          <RefreshCw class="h-4 w-4" :class="loading ? 'animate-spin' : ''" />
          <span class="hidden sm:inline">{{ t('recentPayments.refresh') }}</span>
        </button>
      </div>
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

    <!-- Price items that fall under Annet for want of a group, for those who can assign them -->
    <RouterLink
      v-if="canEditFilters && unassignedItems > 0"
      :to="FILTERS_PATH"
      class="mt-4 flex min-h-11 items-center rounded-md bg-slate-50 px-3 py-2 text-sm text-slate-700 hover:bg-slate-100"
      data-testid="unassigned-hint"
    >
      {{ t('recentPayments.filters.unassignedHint', { count: unassignedItems }) }}
    </RouterLink>

    <!-- Filters -->
    <div class="mt-4 space-y-3">
      <div class="flex flex-wrap gap-2" data-testid="kind-filters">
        <button
          type="button"
          class="min-h-11 max-w-full truncate rounded-full px-4 text-sm font-medium ring-1 ring-inset"
          :class="kinds.includes(ALL) ? 'bg-slate-900 text-white ring-slate-900' : 'bg-white text-slate-600 ring-slate-200 hover:bg-slate-50'"
          :aria-pressed="kinds.includes(ALL)"
          :data-filter="ALL"
          @click="selectFilter(ALL)"
        >
          {{ t('recentPayments.kind.all') }}
        </button>
        <button
          v-for="g in filterGroups"
          :key="g.slug"
          type="button"
          class="min-h-11 max-w-full truncate rounded-full px-4 text-sm font-medium ring-1 ring-inset"
          :class="kinds.includes(g.slug) ? 'bg-slate-900 text-white ring-slate-900' : 'bg-white text-slate-600 ring-slate-200 hover:bg-slate-50'"
          :aria-pressed="kinds.includes(g.slug)"
          :data-filter="g.slug"
          @click="selectFilter(g.slug)"
        >
          {{ groupLabel(g, t) }}
        </button>
        <!-- Before the first answer the club's chips are not known: keep the row's height -->
        <template v-if="!filterGroupsLoaded">
          <span
            v-for="n in 3"
            :key="n"
            class="h-11 w-24 animate-pulse rounded-full bg-slate-100"
            aria-hidden="true"
            data-testid="kind-filter-placeholder"
          />
        </template>
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
          <PaymentGroupIcon :icon="kindIcon(p.kind)" />
          <div class="min-w-0 flex-1">
            <div class="flex items-baseline justify-between gap-2">
              <p class="truncate font-medium text-slate-900">{{ p.payer || t('recentPayments.anonymous') }}</p>
              <p class="shrink-0 font-semibold text-slate-900">{{ nok(p.amount) }}</p>
            </div>
            <div class="mt-0.5 flex items-center justify-between gap-2 text-xs text-slate-500">
              <span class="truncate">{{ p.summary || kindLabel(p.kind) }}</span>
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
