<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import {
  useMyMembership,
  useCancelMembership,
  useUndoCancelMembership,
} from '@/composables/useMyMembership'
import { useConfirm } from '@/stores/confirm'
import { formatDate } from '@/lib/format'
import { ApiError } from '@/lib/errors'

// "Meld deg ut" (BRY-633): the member cancels online, effective 31 December,
// with no refund; until then they can undo it. The server decides the end
// date; this only shows it.

const { t, locale } = useI18n()
const confirm = useConfirm()
const { data: membership, isLoading } = useMyMembership()
const { mutateAsync: cancelMembership, isPending: cancelling } = useCancelMembership()
const { mutateAsync: undoCancel, isPending: undoing } = useUndoCancelMembership()

const reason = ref('')
const error = ref('')

const cancelled = computed(() => !!membership.value?.cancelled_at)
const endDate = computed(() =>
  formatDate(membership.value?.ends_on ?? membership.value?.cancel_ends_on, locale.value),
)
const boardRoles = computed(() =>
  (membership.value?.board_roles ?? []).map((r) => t(`admin.users.role.${r}`)).join(', '),
)

async function onCancel() {
  error.value = ''
  const ok = await confirm({
    title: t('portal.membership.confirmTitle'),
    body: t('portal.membership.confirmBody', { date: endDate.value }),
    confirmLabel: t('portal.membership.confirmAction'),
    tone: 'danger',
  })
  if (!ok) return
  try {
    await cancelMembership(reason.value.trim())
    reason.value = ''
  } catch {
    error.value = t('portal.membership.cancelFailed')
  }
}

async function onUndo() {
  error.value = ''
  try {
    await undoCancel()
  } catch (e) {
    error.value =
      e instanceof ApiError && e.code === 'membership_ended'
        ? t('portal.membership.undoEnded')
        : t('portal.membership.undoFailed')
  }
}
</script>

<template>
  <section
    class="mt-10 max-w-lg rounded-md border border-gray-200 p-4"
    aria-labelledby="cancel-membership-title"
    data-testid="cancel-membership"
  >
    <h2 id="cancel-membership-title" class="text-lg font-semibold text-gray-900">
      {{ t('portal.membership.title') }}
    </h2>

    <p v-if="isLoading" class="mt-2 text-sm text-gray-500">{{ t('common.loading') }}...</p>

    <template v-else-if="membership">
      <div
        v-if="cancelled"
        class="mt-3 flex flex-col gap-3 rounded-md bg-amber-50 p-3 text-sm text-amber-900 sm:flex-row sm:items-center sm:justify-between"
        data-testid="cancelled-state"
      >
        <p class="font-medium">{{ t('portal.membership.cancelledUntil', { date: endDate }) }}</p>
        <button
          v-if="membership.can_undo"
          type="button"
          :disabled="undoing"
          class="min-h-11 rounded-md border border-amber-300 bg-white px-4 py-2 text-sm font-semibold text-amber-900 hover:bg-amber-100 disabled:opacity-50"
          data-testid="undo-cancel"
          @click="onUndo"
        >
          {{ t('portal.membership.undo') }}
        </button>
      </div>

      <template v-else>
        <ul class="mt-3 list-disc space-y-2 pl-5 text-sm text-gray-700">
          <li>{{ t('portal.membership.yearEnd', { date: endDate }) }}</li>
          <li>{{ t('portal.membership.noRefund') }}</li>
          <li v-if="membership.on_waiting_list" data-testid="notice-waiting-list">
            {{ t('portal.membership.waitingList') }}
          </li>
          <li v-if="membership.holds_slip || membership.harbor_share_owner" data-testid="notice-slip">
            {{ t('portal.membership.slipOrShare') }}
          </li>
          <li v-if="membership.open_invoices > 0" data-testid="notice-invoices">
            {{ t('portal.membership.openInvoices', { n: membership.open_invoices }, membership.open_invoices) }}
            <RouterLink to="/portal/invoices" class="font-medium text-brand-700 underline">
              {{ t('portal.membership.openInvoicesLink') }}
            </RouterLink>
          </li>
          <li v-if="boardRoles" data-testid="notice-board">
            {{ t('portal.membership.boardRole', { roles: boardRoles }) }}
          </li>
        </ul>

        <label for="cancel-membership-reason" class="mt-4 block text-sm font-medium text-gray-700">
          {{ t('portal.membership.reasonLabel') }}
        </label>
        <textarea
          id="cancel-membership-reason"
          v-model="reason"
          rows="3"
          maxlength="2000"
          class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          :placeholder="t('portal.membership.reasonPlaceholder')"
        />

        <button
          type="button"
          :disabled="cancelling"
          class="mt-3 min-h-11 w-full rounded-md bg-red-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-red-700 disabled:opacity-50 sm:w-auto"
          data-testid="cancel-membership-button"
          @click="onCancel"
        >
          {{ t('portal.membership.cancel') }}
        </button>
      </template>

      <p v-if="error" class="mt-3 text-sm text-red-700" role="alert">{{ error }}</p>
    </template>
  </section>
</template>
