<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from '@/composables/useToast'

const { t } = useI18n()
const toast = useToast()

function onOffline() {
  toast.error(t('error.offline'), 0)
}

function onOnline() {
  toast.success(t('error.backOnline'))
}

onMounted(() => {
  window.addEventListener('offline', onOffline)
  window.addEventListener('online', onOnline)
})

onUnmounted(() => {
  window.removeEventListener('offline', onOffline)
  window.removeEventListener('online', onOnline)
})
</script>

<template>
  <span />
</template>
