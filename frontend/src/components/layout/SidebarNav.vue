<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { X } from 'lucide-vue-next'
import type { NavGroup } from './navTypes'

// Shared sidebar contents (portal label + themed nav groups). The
// surrounding <aside> drawer, overlay and hamburger stay in each layout
// since their open/close state is layout-owned. `navigate` is emitted on
// every link click so a parent can intercept it (e.g. the admin TOTP
// gate) before the route changes.
defineProps<{
  title: string
  groups: NavGroup[]
  ariaLabel: string
}>()

const emit = defineEmits<{
  (e: 'navigate', event: MouseEvent, to: string): void
  (e: 'close'): void
}>()

const route = useRoute()
const { t } = useI18n()

function isActive(to: string): boolean {
  // Exact match for the portal/admin roots so they don't stay
  // highlighted on every nested route.
  if (to === '/portal' || to === '/admin') {
    return route.path === to || route.path === `${to}/`
  }
  return route.path.startsWith(to)
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div class="flex items-center justify-between border-b border-gray-200 px-4 py-4">
      <span class="text-sm font-bold uppercase tracking-wider text-gray-500">{{ title }}</span>
      <button
        class="text-gray-500 hover:text-gray-700 lg:hidden"
        :aria-label="t('common.close')"
        @click="emit('close')"
      >
        <X class="h-5 w-5" />
      </button>
    </div>

    <nav class="flex-1 overflow-y-auto px-3 py-4" :aria-label="ariaLabel">
      <div v-for="(group, gi) in groups" :key="gi" :class="gi > 0 ? 'mt-5' : ''">
        <div
          v-if="group.titleKey"
          class="mb-1 px-3 text-xs font-semibold uppercase tracking-wider text-gray-400"
        >
          {{ t(group.titleKey) }}
        </div>
        <div class="space-y-0.5">
          <RouterLink
            v-for="item in group.items"
            :key="item.to"
            :to="item.to"
            :class="[
              'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition',
              isActive(item.to)
                ? 'bg-blue-50 text-blue-700'
                : 'text-gray-700 hover:bg-gray-100 hover:text-gray-900',
            ]"
            @click="(e: MouseEvent) => emit('navigate', e, item.to)"
          >
            <component
              :is="item.icon"
              :class="['h-5 w-5', isActive(item.to) ? 'text-blue-600' : 'text-gray-400']"
            />
            <span class="flex-1">{{ item.label }}</span>
            <span
              v-if="item.badge && item.badge > 0"
              class="inline-flex min-w-[1.25rem] items-center justify-center rounded-full bg-red-600 px-1.5 py-0.5 text-[10px] font-semibold text-white"
            >
              {{ item.badge }}
            </span>
          </RouterLink>
        </div>
      </div>
    </nav>
  </div>
</template>
