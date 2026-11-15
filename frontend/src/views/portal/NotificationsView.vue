<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Bell, BellOff, Send, Smartphone } from 'lucide-vue-next'
import {
  useNotificationPreferences,
  useUpdatePreference,
  usePushSubscription,
  useTestPush,
  PushPermissionError,
  PushNotConfiguredError,
} from '@/composables/useNotifications'
import { isAppleMobile, isStandaloneDisplay } from '@/lib/pwa'
import { testPushFailure, testPushOutcome, type TestPushMessage } from '@/lib/testPushResult'
import TestPushNotice from '@/components/notifications/TestPushNotice.vue'

const { t } = useI18n()
const { categories, isLoading } = useNotificationPreferences()
const { mutate: updatePref } = useUpdatePreference()
const { isSupported, isSubscribed, isLoading: pushLoading, checkSubscription, subscribe, unsubscribe } = usePushSubscription()
const { mutate: sendTest, isPending: testSending } = useTestPush()

// iOS delivers web push only to Brygge opened from its Home Screen icon.
const showIosHelp = isAppleMobile() && !isStandaloneDisplay()

const testResult = ref<TestPushMessage | null>(null)
const pushError = ref<string | null>(null)

onMounted(() => checkSubscription())

// Called straight from the click: subscribe() asks for permission before
// anything else, which iOS only allows inside the user gesture.
function togglePush() {
  testResult.value = null
  pushError.value = null
  if (isSubscribed.value) {
    unsubscribe().catch(() => {
      pushError.value = t('notifications.pushUnsubscribeFailed')
    })
    return
  }
  subscribe().catch((err: unknown) => {
    if (err instanceof PushPermissionError) pushError.value = t('notifications.pushDenied')
    else if (err instanceof PushNotConfiguredError) pushError.value = t('notifications.test.notConfigured')
    else pushError.value = t('notifications.pushSubscribeFailed')
  })
}

function toggleCategory(category: string, currentEnabled: boolean) {
  updatePref({ category, enabled: !currentEnabled })
}

function handleTestPush() {
  testResult.value = null
  sendTest(undefined, {
    onSuccess: (res) => {
      testResult.value = testPushOutcome(t, res)
    },
    onError: (err) => {
      testResult.value = testPushFailure(t, err)
    },
  })
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h2 class="text-xl font-semibold text-gray-900">{{ t('notifications.title') }}</h2>
      <p class="mt-1 text-sm text-gray-600">{{ t('notifications.subtitle') }}</p>
    </div>

    <!-- iPhone/iPad: push only works from the Home Screen icon (BRY-682) -->
    <div
      v-if="showIosHelp"
      class="rounded-lg border border-amber-200 bg-amber-50 p-4"
      data-testid="ios-push-help"
    >
      <div class="flex gap-3">
        <Smartphone class="mt-0.5 h-5 w-5 shrink-0 text-amber-600" />
        <div class="min-w-0">
          <p class="text-sm font-medium text-amber-800">{{ t('notifications.iosTitle') }}</p>
          <p class="mt-1 text-sm text-amber-700">{{ t('notifications.iosIntro') }}</p>
          <ol class="mt-2 list-decimal space-y-1 pl-5 text-sm text-amber-700">
            <li>{{ t('notifications.iosStep1') }}</li>
            <li>{{ t('notifications.iosStep2') }}</li>
            <li>{{ t('notifications.iosStep3') }}</li>
            <li>{{ t('notifications.iosStep4') }}</li>
          </ol>
          <p class="mt-2 text-sm text-amber-700">{{ t('notifications.iosLoginNote') }}</p>
          <p class="mt-2 text-xs text-amber-700">{{ t('notifications.iosVersion') }}</p>
        </div>
      </div>
    </div>

    <!-- Push toggle -->
    <div class="rounded-lg border border-gray-200 bg-white p-4">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex items-center gap-3">
          <component :is="isSubscribed ? Bell : BellOff" class="h-5 w-5 shrink-0" :class="isSubscribed ? 'text-brand-600' : 'text-gray-400'" />
          <div>
            <p class="font-medium text-gray-900">{{ t('notifications.pushToggle') }}</p>
            <p class="text-sm text-gray-500">{{ isSubscribed ? t('notifications.pushEnabled') : t('notifications.pushDisabled') }}</p>
          </div>
        </div>
        <div v-if="isSupported" class="flex flex-col gap-2 sm:flex-row">
          <button
            v-if="isSubscribed"
            type="button"
            :disabled="testSending || pushLoading"
            class="inline-flex min-h-11 items-center justify-center gap-2 rounded-md border border-brand-600 px-4 text-sm font-medium text-brand-700 transition hover:bg-brand-50 disabled:opacity-50"
            data-testid="send-test-push"
            @click="handleTestPush"
          >
            <Send class="h-4 w-4" />
            {{ testSending ? t('common.loading') : t('notifications.test.button') }}
          </button>
          <button
            type="button"
            :disabled="pushLoading"
            class="min-h-11 rounded-md px-4 text-sm font-medium transition"
            :class="isSubscribed
              ? 'bg-gray-100 text-gray-700 hover:bg-gray-200'
              : 'bg-brand-600 text-white hover:bg-brand-700'"
            @click="togglePush"
          >
            {{ pushLoading ? t('common.loading') : (isSubscribed ? t('notifications.disable') : t('notifications.enable')) }}
          </button>
        </div>
        <span v-else class="text-sm text-gray-400">
          {{ showIosHelp ? t('notifications.iosNeedsHomeScreen') : t('notifications.notSupported') }}
        </span>
      </div>

      <p
        v-if="pushError"
        role="alert"
        class="mt-3 rounded-md bg-red-50 p-3 text-sm text-red-800"
        data-testid="push-error"
      >
        {{ pushError }}
      </p>
      <TestPushNotice v-if="testResult" :message="testResult" class="mt-3" />
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
          class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:ring-offset-2 disabled:opacity-50 disabled:cursor-not-allowed"
          :class="pref.enabled ? 'bg-brand-600' : 'bg-gray-200'"
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
