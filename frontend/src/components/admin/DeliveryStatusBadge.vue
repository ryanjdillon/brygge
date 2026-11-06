<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { problemStatuses, statusClass, type SendStatus } from '@/lib/deliveryStatus'

// Badge for the latest email of a faktura, or for a member whose address
// bounced. Only a status that needs someone to look (bounced, blocked, not
// sent...) shows; a delivered or in-flight send stays quiet. The member list
// passes its own label and title: it speaks of the address, not of a send.
const props = defineProps<{ status?: SendStatus | null; label?: string; title?: string }>()

const { t } = useI18n()

const shown = computed(() => !!props.status && problemStatuses.has(props.status))
const text = computed(() => props.label || (props.status ? t(`admin.faktura.sent.deliveryLog.status.${props.status}`) : ''))
const tooltip = computed(() => props.title || t('admin.faktura.sent.deliveryLog.badgeTitle', { status: text.value }))
</script>

<template>
  <span
    v-if="shown && status"
    class="mt-1 inline-flex max-w-full items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset"
    :class="statusClass[status]"
    :title="tooltip"
    data-testid="delivery-status-badge"
  >
    {{ text }}
  </span>
</template>
