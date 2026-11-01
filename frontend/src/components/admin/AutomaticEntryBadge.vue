<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// "Automatisk" marker for a bilag the club's system actor booked (ACC-13), not
// a person. The label is visible text, so the meaning does not depend on hover
// (touch has none); the title is a desktop extra and the sr-only text carries
// the full explanation for assistive tech. `whitespace-nowrap` + `shrink-0`
// keep it on one line when it sits beside a wrapping description.
const { t } = useI18n()
</script>

<template>
  <span
    data-testid="automatic-entry-badge"
    class="inline-flex shrink-0 items-center whitespace-nowrap rounded-full bg-sky-100 px-2.5 py-0.5 text-xs font-medium text-sky-800"
    :title="t('admin.accounting.journal.automaticTooltip')"
  >
    <span aria-hidden="true">{{ t('admin.accounting.journal.automatic') }}</span>
    <span class="sr-only">{{ t('admin.accounting.journal.automaticTooltip') }}</span>
  </span>
</template>
