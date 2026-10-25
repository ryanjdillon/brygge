<script setup lang="ts">
import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import NavBar from '@/components/layout/NavBar.vue'
import Footer from '@/components/layout/Footer.vue'
import ToastContainer from '@/components/ui/ToastContainer.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import NetworkStatus from '@/components/ui/NetworkStatus.vue'
import FreshTotpModal from '@/components/auth/FreshTotpModal.vue'
import TotpExpiringModal from '@/components/auth/TotpExpiringModal.vue'

const { t } = useI18n()
const route = useRoute()
// A `bare` route (e.g. a print sheet) renders without the site chrome.
const bare = computed(() => route.meta.bare === true)
</script>

<template>
  <div class="flex min-h-screen flex-col">
    <a href="#main-content" class="skip-nav">{{ t('common.skipToContent') }}</a>
    <NavBar v-if="!bare" />
    <main id="main-content" class="flex-1">
      <RouterView />
    </main>
    <Footer v-if="!bare" />
    <ToastContainer />
    <ConfirmDialog />
    <NetworkStatus />
    <FreshTotpModal />
    <TotpExpiringModal />
  </div>
</template>
