<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import { formatNOK, formatDate } from '@/lib/format'
import { useToast } from '@/composables/useToast'
import {
  useUnclearedStripePayouts,
  useClearStripePayout,
  stripeClearErrorCode,
  refusalCountOf,
  type UnclearedStripePayout,
  type StripePayoutClearResult,
} from '@/composables/useStripePayouts'

// Picks the Stripe payout a bank deposit carries and clears it (DR bank /
// CR the Stripe clearing account). The caller prompts for a fresh TOTP
// before opening it; the server re-checks everything shown here.
const props = defineProps<{
  open: boolean
  /** The deposit being cleared. */
  row: { id: string; amount: number; row_date: string } | null
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'cleared', result: StripePayoutClearResult): void
}>()

const { t, locale } = useI18n()
const toast = useToast()

const enabled = computed(() => props.open)
const { data: payouts, isLoading } = useUnclearedStripePayouts(enabled)
const clear = useClearStripePayout()

const rowOre = computed(() => (props.row ? Math.round(props.row.amount * 100) : 0))

// A payout of the deposit's amount first; the server refuses any other.
const sorted = computed<UnclearedStripePayout[]>(() => {
  const list = [...(payouts.value ?? [])]
  return list.sort((a, b) => Number(b.amount_ore === rowOre.value) - Number(a.amount_ore === rowOre.value))
})
const matches = (p: UnclearedStripePayout) => p.amount_ore === rowOre.value
// An automatic payout Stripe has not listed would look fully booked with
// no members; a manual one needs what could have funded it booked.
const notListed = (p: UnclearedStripePayout) => p.automatic && !p.members_listed
const selectable = (p: UnclearedStripePayout) =>
  matches(p) && p.open_members === 0 && p.open_balance === 0 && !notListed(p)

const selectedId = ref<string | null>(null)
const errorText = ref('')

// Preselects the payout when exactly one can take the deposit. Another
// deposit starts from no selection, so a pick never carries over to it.
watch(
  [() => props.open, sorted, () => props.row?.id],
  ([open, , rowId], [, , prevRowId]) => {
    if (rowId !== prevRowId) selectedId.value = null
    if (!open) return
    if (selectedId.value && sorted.value.some((p) => p.id === selectedId.value && selectable(p))) return
    const fits = sorted.value.filter(selectable)
    selectedId.value = fits.length === 1 ? fits[0].id : null
  },
  { immediate: true },
)
watch(
  () => props.open,
  (open) => {
    if (open) errorText.value = ''
  },
)

function close() {
  emit('update:open', false)
}

function refusalText(e: unknown): string {
  const code = stripeClearErrorCode(e)
  if (!code) return t('admin.bankReconcile.stripePayoutPicker.errors.generic')
  const count = refusalCountOf(e)
  return t(`admin.bankReconcile.stripePayoutPicker.errors.${code}`, { count }, count)
}

async function confirm() {
  if (!props.row || !selectedId.value) return
  errorText.value = ''
  try {
    const res = await clear.mutateAsync({ payoutId: selectedId.value, bankRowId: props.row.id })
    toast.success(t('admin.bankReconcile.stripePayoutPicker.success'))
    const residue = res.clearing_balance_ore ?? 0
    if (residue !== 0) {
      toast.info(t('admin.bankReconcile.stripePayoutPicker.residue', { amount: formatNOK(residue / 100) }), 15000)
    } else if (res.members_count === 0) {
      toast.info(t('admin.bankReconcile.stripePayoutPicker.residueManual'), 10000)
    }
    emit('cleared', res)
    close()
  } catch (e) {
    errorText.value = refusalText(e)
  }
}
</script>

<template>
  <Modal
    :open="open"
    size="lg"
    full-screen-on-mobile
    :title="t('admin.bankReconcile.stripePayoutPicker.title')"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <div data-testid="stripe-payout-picker">
      <p v-if="row" class="text-sm text-gray-700">
        {{ t('admin.bankReconcile.stripePayoutPicker.intro', { amount: formatNOK(row.amount), date: formatDate(row.row_date, locale) }) }}
      </p>
      <p class="mt-1 text-xs text-gray-500">{{ t('admin.bankReconcile.stripePayoutPicker.posting') }}</p>

      <p v-if="isLoading" class="mt-4 text-sm text-gray-500" data-testid="stripe-payout-picker-loading">
        {{ t('common.loading') }}…
      </p>
      <p
        v-else-if="!sorted.length"
        class="mt-4 rounded-md border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500"
        data-testid="stripe-payout-picker-empty"
      >
        {{ t('admin.bankReconcile.stripePayoutPicker.empty') }}
      </p>
      <ul v-else class="mt-4 space-y-2" role="radiogroup" :aria-label="t('admin.bankReconcile.stripePayoutPicker.title')">
        <li v-for="p in sorted" :key="p.id">
          <button
            type="button"
            role="radio"
            :aria-checked="selectedId === p.id"
            :disabled="!selectable(p)"
            class="flex min-h-11 w-full flex-col items-start gap-1 rounded-md border px-3 py-2 text-left text-sm disabled:cursor-not-allowed disabled:opacity-60"
            :class="
              selectedId === p.id
                ? 'border-brand-500 bg-brand-50 ring-1 ring-brand-400'
                : matches(p)
                  ? 'border-emerald-300 bg-emerald-50'
                  : 'border-gray-200 bg-white'
            "
            :data-testid="`stripe-payout-${p.payout_id}`"
            @click="selectedId = p.id"
          >
            <span class="flex w-full flex-wrap items-center gap-x-3 gap-y-1">
              <span class="font-semibold tabular-nums">{{ formatNOK(p.amount) }}</span>
              <span class="text-gray-600">
                {{ t('admin.bankReconcile.stripePayoutPicker.arrival', { date: formatDate(p.arrival_date, locale) }) }}
              </span>
              <span
                class="rounded-full px-2 py-0.5 text-xs font-semibold"
                :class="p.automatic ? 'bg-violet-100 text-violet-700' : 'bg-gray-100 text-gray-700'"
              >
                {{ p.automatic ? t('admin.bankReconcile.stripePayoutPicker.automatic') : t('admin.bankReconcile.stripePayoutPicker.manual') }}
              </span>
              <span v-if="matches(p)" class="text-xs font-semibold text-emerald-700">
                {{ t('admin.bankReconcile.stripePayoutPicker.matchesRow') }}
              </span>
            </span>
            <span class="break-all font-mono text-xs text-gray-500">{{ p.payout_id }}</span>
            <span v-if="!matches(p)" class="text-xs text-gray-600">
              {{ t('admin.bankReconcile.stripePayoutPicker.amountDiffers') }}
            </span>
            <span v-if="notListed(p)" class="text-xs text-amber-700" data-testid="stripe-payout-not-listed">
              {{ t('admin.bankReconcile.stripePayoutPicker.notListedBlocked') }}
            </span>
            <span v-else-if="p.open_members > 0" class="text-xs text-amber-700" data-testid="stripe-payout-open-members">
              {{ t('admin.bankReconcile.stripePayoutPicker.openMembers', { count: p.open_members }, p.open_members) }}
            </span>
            <span v-else-if="p.open_balance > 0" class="text-xs text-amber-700" data-testid="stripe-payout-open-balance">
              {{ t('admin.bankReconcile.stripePayoutPicker.openBalance', { count: p.open_balance }, p.open_balance) }}
            </span>
            <span v-else-if="!p.members_listed" class="text-xs text-gray-600">
              {{ t('admin.bankReconcile.stripePayoutPicker.membersNotListed') }}
            </span>
          </button>
        </li>
      </ul>

      <p v-if="errorText" class="mt-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-800" role="alert" data-testid="stripe-payout-picker-error">
        {{ errorText }}
      </p>

      <div class="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <button
          type="button"
          class="min-h-11 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50"
          @click="close"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="min-h-11 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="!selectedId || clear.isPending.value"
          data-testid="stripe-payout-picker-confirm"
          @click="confirm"
        >
          {{ t('admin.bankReconcile.stripePayoutPicker.confirm') }}
        </button>
      </div>
    </div>
  </Modal>
</template>
