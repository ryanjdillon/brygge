<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { ChevronDown } from 'lucide-vue-next'
import type { FaqItem } from '@/lib/faqData'

defineProps<{ items: FaqItem[] }>()

// A club-authored link is internal when it's a site path ("/pricing"); render
// those with RouterLink, everything else as a plain external anchor.
const isInternal = (url: string) => url.startsWith('/')
</script>

<template>
  <ul class="divide-y divide-slate-200 rounded-2xl border border-slate-200 bg-white">
    <li v-for="item in items" :key="item.id">
      <details class="group">
        <summary
          class="flex cursor-pointer list-none items-center justify-between gap-4 px-5 py-4 text-left font-semibold text-slate-900 outline-none transition hover:bg-slate-50 focus-visible:ring-2 focus-visible:ring-teal-500"
        >
          {{ item.question }}
          <ChevronDown
            class="h-5 w-5 flex-none text-slate-400 transition group-open:rotate-180"
            aria-hidden="true"
          />
        </summary>
        <div class="px-5 pb-5 text-slate-600">
          <p class="whitespace-pre-line">{{ item.answer }}</p>
          <template v-if="item.link_url">
            <RouterLink
              v-if="isInternal(item.link_url)"
              :to="item.link_url"
              class="mt-2 inline-flex items-center font-semibold text-teal-700 hover:text-teal-800 hover:underline"
            >
              {{ item.link_label || item.link_url }}
            </RouterLink>
            <a
              v-else
              :href="item.link_url"
              target="_blank"
              rel="noopener noreferrer"
              class="mt-2 inline-flex items-center font-semibold text-teal-700 hover:text-teal-800 hover:underline"
            >
              {{ item.link_label || item.link_url }}
            </a>
          </template>
        </div>
      </details>
    </li>
  </ul>
</template>
