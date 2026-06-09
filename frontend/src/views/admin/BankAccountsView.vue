<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BankAccountsPanel from '@/components/admin/BankAccountsPanel.vue'

const { t } = useI18n()
</script>

<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.bankAccounts.title') }}</h1>
    <BankAccountsPanel />
  </div>
</template>
