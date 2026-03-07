<script setup lang="ts">
import { RouterView } from 'vue-router'
import { useI18n } from 'vue-i18n'
import NavBar from '@/components/layout/NavBar.vue'
import Footer from '@/components/layout/Footer.vue'

const { t } = useI18n()
</script>

<template>
  <div class="flex min-h-screen flex-col">
    <a href="#main-content" class="skip-nav">{{ t('common.skipToContent') }}</a>
    <NavBar />
    <main id="main-content" class="flex-1">
      <RouterView />
    </main>
    <Footer />
  </div>
</template>
