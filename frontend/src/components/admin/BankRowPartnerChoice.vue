<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatNOK as formatNOKBase, formatDate as fmtDate } from '@/lib/format'
import type { PartnerSuggestion } from '@/composables/useBankReconcile'

// The "does this row belong with another?" choice in Lag bilag (BRY-380).
// The value is a partner row id (book together), 'alone', or null (not yet
// chosen). With a strong partner the parent keeps booking disabled until a
// choice is made; weak partners are offered but not required.
const props = defineProps<{
  partners: PartnerSuggestion[]
  loading?: boolean
}>()
const choice = defineModel<string | null>({ required: true })

const { t, locale } = useI18n()

const hasStrong = computed(() => props.partners.some((p) => p.strength === 'strong'))

function formatNOK(n: number): string {
  return formatNOKBase(n, { signed: true })
}
</script>

<template>
  <p v-if="loading" class="mt-3 text-xs text-gray-500">{{ t('common.loading') }}…</p>
  <fieldset
    v-else-if="partners.length"
    class="mt-3 rounded-md border p-3"
    :class="hasStrong ? 'border-indigo-300 bg-indigo-50' : 'border-gray-200 bg-gray-50'"
    data-testid="partner-choice"
  >
    <legend class="px-1 text-sm font-semibold text-gray-900">
      {{ t('admin.bankReconcile.partners.question') }}
    </legend>
    <p class="text-xs text-gray-700">
      {{ hasStrong ? t('admin.bankReconcile.partners.introStrong') : t('admin.bankReconcile.partners.introWeak') }}
    </p>
    <div class="mt-2 space-y-2">
      <label
        v-for="p in partners"
        :key="p.id"
        class="flex cursor-pointer items-start gap-2 rounded-md border bg-white p-2 text-sm"
        :class="choice === p.id ? 'border-indigo-400 ring-1 ring-indigo-300' : 'border-gray-200'"
      >
        <input v-model="choice" type="radio" :value="p.id" class="mt-1" :data-testid="`partner-option-${p.id}`" />
        <span class="min-w-0 flex-1">
          <span class="font-semibold">{{ t('admin.bankReconcile.partners.together') }}</span>
          <span class="ml-2 tabular-nums">{{ formatNOK(p.amount) }}</span>
          <span class="ml-2 text-gray-500">{{ fmtDate(p.row_date, locale) }}</span>
          <span v-if="p.counterpart" class="ml-2 text-gray-700">{{ p.counterpart }}</span>
          <span class="mt-0.5 block text-xs text-gray-600">
            {{ p.reasons.map((r) => t('admin.bankReconcile.partners.reasons.' + r)).join(' · ') }}
            · {{ t('admin.bankReconcile.partners.daysApart', p.days_apart) }}
          </span>
          <span class="mt-0.5 block text-xs font-semibold text-gray-800">
            {{ t('admin.bankReconcile.partners.net', { net: formatNOK(p.net) }) }}
          </span>
        </span>
      </label>
      <label
        class="flex cursor-pointer items-start gap-2 rounded-md border bg-white p-2 text-sm"
        :class="choice === 'alone' ? 'border-indigo-400 ring-1 ring-indigo-300' : 'border-gray-200'"
      >
        <input v-model="choice" type="radio" value="alone" class="mt-1" data-testid="partner-option-alone" />
        <span>{{ t('admin.bankReconcile.partners.alone') }}</span>
      </label>
    </div>
    <p v-if="hasStrong && choice === null" class="mt-2 text-xs font-semibold text-indigo-800" data-testid="partner-choice-required">
      {{ t('admin.bankReconcile.partners.choiceRequired') }}
    </p>
  </fieldset>
</template>
