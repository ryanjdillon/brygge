<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <footer class="border-t border-gray-200 bg-gray-50">
    <div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <div class="flex flex-col items-center justify-between gap-4 sm:flex-row">
        <p class="text-sm text-gray-500">
          &copy; {{ new Date().getFullYear() }} Brygge
        </p>
        <div class="flex gap-4">
          <RouterLink
            to="/contact"
            class="text-sm text-gray-500 hover:text-gray-700"
          >
            {{ t('nav.contact') }}
          </RouterLink>
        </div>
      </div>
    </div>
  </footer>
</template>
