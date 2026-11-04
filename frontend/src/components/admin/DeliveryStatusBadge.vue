<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { problemStatuses, statusClass, type SendStatus } from '@/lib/deliveryStatus'

// Faktura list badge for the latest email of a faktura. Only a status that
// needs someone to look (bounced, blocked, not sent...) shows; a delivered
// or in-flight send stays quiet.
const props = defineProps<{ status?: SendStatus | null }>()

const { t } = useI18n()

const shown = computed(() => !!props.status && problemStatuses.has(props.status))
const label = computed(() => (props.status ? t(`admin.faktura.sent.deliveryLog.status.${props.status}`) : ''))
</script>

<template>
  <span
    v-if="shown && status"
    class="mt-1 inline-flex max-w-full items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset"
    :class="statusClass[status]"
    :title="t('admin.faktura.sent.deliveryLog.badgeTitle', { status: label })"
    data-testid="delivery-status-badge"
  >
    {{ label }}
  </span>
</template>
