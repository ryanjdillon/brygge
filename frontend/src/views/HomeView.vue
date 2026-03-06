<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <main>
    <h1>Brygge</h1>
    <p>{{ t('nav.home') }}</p>
  </main>
</template>
