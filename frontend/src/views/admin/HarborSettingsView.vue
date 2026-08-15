<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Anchor, Caravan } from 'lucide-vue-next'
import HarborSettingsSection from './HarborSettingsSection.vue'
import MotorhomeSettingsSection from './MotorhomeSettingsSection.vue'

const { t } = useI18n()
const tab = ref<'harbor' | 'motorhome'>('harbor')
</script>

<template>
  <div class="space-y-5">
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.harborSettings.pageTitle') }}</h1>

    <div class="flex gap-1 border-b border-slate-200">
      <button
        type="button"
        class="-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium"
        :class="tab === 'harbor' ? 'border-brand-600 text-brand-700' : 'border-transparent text-slate-500 hover:text-slate-700'"
        @click="tab = 'harbor'"
      >
        <Anchor class="h-4 w-4" /> {{ t('admin.harborSettings.tabHarbor') }}
      </button>
      <button
        type="button"
        class="-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium"
        :class="tab === 'motorhome' ? 'border-brand-600 text-brand-700' : 'border-transparent text-slate-500 hover:text-slate-700'"
        @click="tab = 'motorhome'"
      >
        <Caravan class="h-4 w-4" /> {{ t('admin.harborSettings.tabMotorhome') }}
      </button>
    </div>

    <HarborSettingsSection v-show="tab === 'harbor'" />
    <MotorhomeSettingsSection v-show="tab === 'motorhome'" />
  </div>
</template>
