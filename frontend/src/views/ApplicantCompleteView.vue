<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { UserPlus } from 'lucide-vue-next'

const { t } = useI18n()
</script>

<template>
  <div class="mx-auto max-w-2xl">
    <div class="rounded-lg border border-gray-200 bg-white p-6 sm:p-8">
      <div class="flex items-center gap-3">
        <UserPlus class="h-6 w-6 flex-none text-brand-600" aria-hidden="true" />
        <h1 class="text-2xl font-bold text-gray-900">{{ t('applicant.complete.title') }}</h1>
      </div>
      <p class="mt-4 text-gray-700">{{ t('applicant.complete.body') }}</p>
      <div class="mt-6 flex flex-col gap-3 sm:flex-row">
        <RouterLink
          to="/portal/invoices"
          class="inline-flex min-h-11 items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700"
        >
          {{ t('applicant.complete.invoices') }}
        </RouterLink>
        <RouterLink
          to="/portal/profile"
          class="inline-flex min-h-11 items-center justify-center rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          {{ t('applicant.complete.profile') }}
        </RouterLink>
      </div>
    </div>
  </div>
</template>
