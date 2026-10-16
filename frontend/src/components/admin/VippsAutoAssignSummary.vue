<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { unassignVippsCharge, type VippsAutoAssignResult } from '@/composables/useBankImports'

// Shows what an import or Synk Vipps booked without review (BRY-340) and lets
// the treasurer undo a booking, which puts the charge back in Tildel.
const props = defineProps<{ result: VippsAutoAssignResult }>()

const { t } = useI18n()
const queryClient = useQueryClient()
const { ensureFreshTotp } = useFreshTotp()

const undone = ref<Set<string>>(new Set())
const busy = ref<string | null>(null)
const error = ref<string | null>(null)

async function undo(rowID: string) {
  if (!(await ensureFreshTotp())) return
  busy.value = rowID
  error.value = null
  try {
    await unassignVippsCharge(rowID)
    undone.value = new Set([...undone.value, rowID])
    queryClient.invalidateQueries({ queryKey: ['accounting'] })
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = null
  }
}

function targetLabel(rule: string, target: string) {
  return rule === 'member_invoice' ? target : t('admin.bankImports.autoAssignAccount', { code: target })
}
</script>

<template>
  <div class="mt-2" data-testid="vipps-auto-assign-summary">
    <p v-if="!props.result.enabled">{{ t('admin.bankImports.autoAssignOff', { review: props.result.needs_review }) }}</p>
    <template v-else>
      <p>
        {{ t('admin.bankImports.autoAssignCounts', { assigned: props.result.assigned.length, review: props.result.needs_review }) }}
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
    <p v-if="error" class="mt-1 text-red-700">{{ error }}</p>
  </div>
</template>
