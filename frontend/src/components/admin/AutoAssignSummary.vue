<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { unassignBankRow, unassignVippsCharge, type AutoAssignResult } from '@/composables/useBankImports'
import { asClearedSettlementRefusal, type ClearedSettlementRefusal } from '@/composables/useClearedSettlement'
import ClearedSettlementRefusalNote from '@/components/admin/ClearedSettlementRefusal.vue'

// Shows what an import or sync booked without review — Vipps charges
// (BRY-340) or KID-less bank deposits (BRY-366) — and lets the treasurer
// undo a booking, which puts the payment back in Tildel. source picks the
// undo endpoint.
const props = defineProps<{ result: AutoAssignResult; source: 'vipps' | 'bank' }>()

const { t } = useI18n()
const queryClient = useQueryClient()
const { ensureFreshTotp } = useFreshTotp()

const undone = ref<Set<string>>(new Set())
const busy = ref<string | null>(null)
const error = ref<string | null>(null)
// A charge whose settlement is already cleared is refused; the note offers to
// undo the clearing and then repeats the undo of that charge.
const cleared = ref<{ rowID: string; refusal: ClearedSettlementRefusal } | null>(null)

async function undo(rowID: string) {
  if (!(await ensureFreshTotp())) return
  busy.value = rowID
  error.value = null
  cleared.value = null
  try {
    if (props.source === 'bank') await unassignBankRow(rowID)
    else await unassignVippsCharge(rowID)
    undone.value = new Set([...undone.value, rowID])
    queryClient.invalidateQueries({ queryKey: ['accounting'] })
  } catch (e) {
    const refusal = asClearedSettlementRefusal(e)
    if (refusal) cleared.value = { rowID, refusal }
    else error.value = (e as Error).message
  } finally {
    busy.value = null
  }
}

function undoAgain() {
  const rowID = cleared.value?.rowID
  cleared.value = null
  if (rowID) void undo(rowID)
}

function targetLabel(rule: string, target: string) {
  return rule === 'member_invoice' ? target : t('admin.bankImports.autoAssignAccount', { code: target })
}
</script>

<template>
  <div class="mt-2" :data-testid="`${props.source}-auto-assign-summary`">
    <p v-if="!props.result.enabled">{{ t('admin.bankImports.autoAssignOff', { review: props.result.needs_review }) }}</p>
    <template v-else>
      <p>
        {{
          t(props.source === 'bank' ? 'admin.bankImports.bankAutoAssignCounts' : 'admin.bankImports.autoAssignCounts', {
            assigned: props.result.assigned.length,
            review: props.result.needs_review,
          })
        }}
      </p>
      <ul v-if="props.result.assigned.length" class="mt-1 space-y-0.5">
        <li v-for="a in props.result.assigned" :key="a.row_id" class="flex items-center gap-2">
          <span>{{ a.date }} · {{ a.amount.toLocaleString('nb-NO') }} kr → {{ targetLabel(a.rule, a.target) }}</span>
          <span v-if="undone.has(a.row_id)" class="text-gray-500">{{ t('admin.bankImports.autoAssignUndone') }}</span>
          <button
            v-else
            type="button"
            class="text-brand-700 underline hover:text-brand-900 disabled:opacity-50"
            :disabled="busy === a.row_id"
            @click="undo(a.row_id)"
          >
            {{ t('admin.bankImports.autoAssignUndo') }}
          </button>
        </li>
      </ul>
    </template>
    <p v-if="props.result.error" class="mt-1 text-red-700">{{ props.result.error }}</p>
    <p v-if="error" class="mt-1 text-red-700">{{ error }}</p>
    <ClearedSettlementRefusalNote v-if="cleared" :refusal="cleared.refusal" @undone="undoAgain" />
  </div>
</template>
