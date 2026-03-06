<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <main>
    <h1>{{ t('nav.pricing') }}</h1>
  </main>
</template>
