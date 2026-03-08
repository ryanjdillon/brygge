<script setup lang="ts">
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Bell, BellOff, Smartphone } from 'lucide-vue-next'
import { useNotificationPreferences, useUpdatePreference, usePushSubscription } from '@/composables/useNotifications'

const { t } = useI18n()
const { categories, isLoading } = useNotificationPreferences()
const { mutate: updatePref } = useUpdatePreference()
const { isSupported, isSubscribed, isLoading: pushLoading, checkSubscription, subscribe, unsubscribe } = usePushSubscription()

const isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent)
const isStandalone = window.matchMedia('(display-mode: standalone)').matches || (navigator as any).standalone

onMounted(() => checkSubscription())

function togglePush() {
  if (isSubscribed.value) {
    unsubscribe()
  } else {
    subscribe()
  }
}

function toggleCategory(category: string, currentEnabled: boolean) {
  updatePref({ category, enabled: !currentEnabled })
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h2 class="text-xl font-semibold text-gray-900">{{ t('notifications.title') }}</h2>
      <p class="mt-1 text-sm text-gray-600">{{ t('notifications.subtitle') }}</p>
    </div>

    <!-- iOS Banner -->
    <div v-if="isIOS && !isStandalone" class="rounded-lg border border-amber-200 bg-amber-50 p-4">
      <div class="flex gap-3">
        <Smartphone class="h-5 w-5 text-amber-600 shrink-0 mt-0.5" />
        <div>
          <p class="text-sm font-medium text-amber-800">{{ t('notifications.iosTitle') }}</p>
          <p class="mt-1 text-sm text-amber-700">{{ t('notifications.iosInstructions') }}</p>
        </div>
      </div>
    </div>

    <!-- Push toggle -->
    <div class="rounded-lg border border-gray-200 bg-white p-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <component :is="isSubscribed ? Bell : BellOff" class="h-5 w-5" :class="isSubscribed ? 'text-blue-600' : 'text-gray-400'" />
          <div>
            <p class="font-medium text-gray-900">{{ t('notifications.pushToggle') }}</p>
            <p class="text-sm text-gray-500">{{ isSubscribed ? t('notifications.pushEnabled') : t('notifications.pushDisabled') }}</p>
          </div>
        </div>
        <button
          v-if="isSupported"
          :disabled="pushLoading"
          class="rounded-md px-3 py-1.5 text-sm font-medium transition"
          :class="isSubscribed
            ? 'bg-gray-100 text-gray-700 hover:bg-gray-200'
            : 'bg-blue-600 text-white hover:bg-blue-700'"
          @click="togglePush"
        >
          {{ pushLoading ? t('common.loading') : (isSubscribed ? t('notifications.disable') : t('notifications.enable')) }}
        </button>
        <span v-else class="text-sm text-gray-400">{{ t('notifications.notSupported') }}</span>
      </div>
    </div>

    <!-- Category preferences -->
    <div v-if="isLoading" class="animate-pulse space-y-3">
      <div v-for="i in 6" :key="i" class="h-14 rounded-lg bg-gray-100" />
    </div>

    <div v-else class="divide-y divide-gray-100 rounded-lg border border-gray-200 bg-white">
      <div
        v-for="pref in categories"
        :key="pref.category"
        class="flex items-center justify-between px-4 py-3"
      >
        <div>
          <p class="text-sm font-medium text-gray-900">
            {{ t(`notifications.cat.${pref.category}`) }}
            <span v-if="pref.required" class="ml-1 text-xs text-gray-400">({{ t('notifications.required') }})</span>
          </p>
        </div>
        <button
          :disabled="pref.required"
          class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 disabled:opacity-50 disabled:cursor-not-allowed"
          :class="pref.enabled ? 'bg-blue-600' : 'bg-gray-200'"
          role="switch"
          :aria-checked="pref.enabled"
          @click="!pref.required && toggleCategory(pref.category, pref.enabled)"
        >
          <span
            class="pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow ring-0 transition-transform duration-200"
            :class="pref.enabled ? 'translate-x-5' : 'translate-x-0'"
          />
        </button>
      </div>
    </div>
  </div>
</template>
