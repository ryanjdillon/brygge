<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useClubStore } from '@/stores/club'
import { Github } from 'lucide-vue-next'

const { t } = useI18n()
const club = useClubStore()
club.ensureLoaded()

const repoUrl = 'https://github.com/brygge-klubb/brygge'
</script>

<template>
  <footer class="border-t border-gray-200 bg-gray-50" role="contentinfo">
    <div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <div class="flex flex-col items-center justify-between gap-4 sm:flex-row">
        <p class="text-sm text-gray-500">
          &copy; {{ new Date().getFullYear() }} {{ club.name || 'Brygge' }}
          <span class="text-gray-400"> · powered by </span>
          <a
            :href="repoUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-gray-500 hover:text-gray-700 underline"
          >Brygge</a>
        </p>
        <div class="flex items-center gap-4">
          <RouterLink
            to="/contact"
            class="text-sm text-gray-500 hover:text-gray-700"
          >
            {{ t('nav.contact') }}
          </RouterLink>
          <a
            :href="repoUrl"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="Brygge source code on GitHub"
            class="text-gray-500 hover:text-gray-700"
          >
            <Github class="h-5 w-5" aria-hidden="true" />
          </a>
        </div>
      </div>
    </div>
  </footer>
</template>
