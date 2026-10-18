<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAccountsList } from '@/composables/useAccounting'
import { priceCategoryKey } from '@/lib/priceCategories'

// One account in a picker: "code — name", and underneath, muted, what books
// to it by default (price categories · price items), so a treasurer finds
// the slipping account without knowing it is called Salgsinntekter. Hints
// come from the accounts list by code, because suggestion rows carry none.
const props = defineProps<{ code: string; name: string }>()

const { t } = useI18n()
const { data: accounts } = useAccountsList()

const MAX_ITEMS = 4

const hint = computed(() => {
  const account = accounts.value?.find((a) => a.code === props.code)
  if (!account) return ''
  const categories = (account.price_categories ?? []).map((c) => {
    const key = priceCategoryKey(c)
    return key ? t(key) : c
  })
  const names = (account.price_items ?? []).map((it) => it.name)
  let items = names.slice(0, MAX_ITEMS).join(', ')
  if (names.length > MAX_ITEMS) {
    items += ' ' + t('admin.bankReconcile.accountMoreItems', { count: names.length - MAX_ITEMS })
  }
  return [categories.join(', '), items].filter(Boolean).join(' · ')
})
</script>

<template>
  <span class="block min-w-0">
    <span class="block font-mono">{{ code }} — {{ name }}</span>
    <span v-if="hint" class="block truncate text-xs text-gray-500" :title="hint" data-testid="account-hint">{{ hint }}</span>
  </span>
</template>
