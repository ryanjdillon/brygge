<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useClubStore } from '@/stores/club'

const { t } = useI18n()
const club = useClubStore()
club.ensureLoaded()

const repoUrl = 'https://github.com/ryanjdillon/brygge'
</script>

<template>
  <footer class="border-t border-gray-200 bg-gray-50" role="contentinfo">
    <div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <!-- Business contact block — required by Vipps "krav til nettside"
           (DIL-346). Org.nr, address, phone, email must be clearly
           visible on every public page. -->
      <div class="grid grid-cols-1 gap-6 border-b border-gray-200 pb-6 text-sm text-gray-600 sm:grid-cols-2 lg:grid-cols-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-wide text-gray-500">{{ t('footer.contactHeading') }}</p>
          <p class="mt-2 font-semibold text-gray-900">{{ club.name || 'Brygge' }}</p>
          <p v-if="club.orgNumber" class="mt-1 text-xs">{{ t('footer.orgNumber') }} {{ club.orgNumber }}</p>
        </div>
        <div v-if="club.address">
          <p class="text-xs font-semibold uppercase tracking-wide text-gray-500">{{ t('footer.address') }}</p>
          <p class="mt-2 whitespace-pre-line">{{ club.address }}</p>
        </div>
        <div v-if="club.phone || club.treasurerEmail || club.chairmanEmail">
          <p class="text-xs font-semibold uppercase tracking-wide text-gray-500">{{ t('footer.reach') }}</p>
          <p v-if="club.phone" class="mt-2">
            <a :href="`tel:${club.phone}`" class="hover:underline">{{ club.phone }}</a>
          </p>
          <p v-if="club.treasurerEmail || club.chairmanEmail" class="mt-1">
            <a :href="`mailto:${club.treasurerEmail || club.chairmanEmail}`" class="hover:underline">
              {{ club.treasurerEmail || club.chairmanEmail }}
            </a>
          </p>
        </div>
        <div>
          <p class="text-xs font-semibold uppercase tracking-wide text-gray-500">{{ t('footer.legal') }}</p>
          <ul class="mt-2 space-y-1">
            <li>
              <RouterLink to="/contact" class="hover:underline">{{ t('nav.contact') }}</RouterLink>
            </li>
            <li>
              <RouterLink to="/salgsvilkar" class="hover:underline">{{ t('footer.terms') }}</RouterLink>
            </li>
            <li>
              <RouterLink to="/privacy" class="hover:underline">{{ t('footer.privacy') }}</RouterLink>
            </li>
          </ul>
        </div>
      </div>

      <div class="mt-6 flex flex-col items-center justify-between gap-3 text-xs text-gray-500 sm:flex-row">
        <span>&copy; {{ new Date().getFullYear() }} {{ club.name || 'Brygge' }}</span>
        <div class="flex items-center gap-3">
          <span>{{ t('footer.poweredBy') }} Brygge</span>
          <a
            :href="repoUrl"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="Brygge source code on GitHub"
            class="inline-flex items-center text-gray-500 hover:text-gray-900"
          >
            <svg viewBox="0 0 24 24" class="h-5 w-5" fill="currentColor" aria-hidden="true">
              <path d="M12 .5C5.65.5.5 5.65.5 12c0 6.35 5.15 11.5 11.5 11.5S23.5 18.35 23.5 12C23.5 5.65 18.35.5 12 .5Zm0 1.5c5.52 0 10 4.48 10 10s-4.48 10-10 10S2 17.52 2 12 6.48 2 12 2Z" />
              <path d="M12 5.4c-3.65 0-6.6 2.95-6.6 6.6 0 2.92 1.89 5.39 4.51 6.27.33.06.45-.14.45-.32 0-.16-.01-.69-.01-1.25-1.84.4-2.22-.78-2.22-.78-.3-.76-.73-.96-.73-.96-.6-.41.05-.4.05-.4.66.05 1.01.68 1.01.68.59 1.01 1.54.72 1.92.55.06-.43.23-.72.42-.89-1.47-.17-3.02-.74-3.02-3.27 0-.72.26-1.31.68-1.77-.07-.17-.3-.85.06-1.78 0 0 .56-.18 1.83.68a6.34 6.34 0 0 1 3.32 0c1.27-.86 1.83-.68 1.83-.68.36.93.13 1.61.06 1.78.42.46.68 1.05.68 1.77 0 2.54-1.55 3.1-3.03 3.26.24.2.45.6.45 1.21 0 .87-.01 1.58-.01 1.79 0 .18.12.39.46.32a6.6 6.6 0 0 0 4.51-6.27c0-3.65-2.95-6.6-6.6-6.6Z" />
            </svg>
          </a>
        </div>
      </div>
    </div>
  </footer>
</template>
