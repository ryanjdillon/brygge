<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import { Printer } from 'lucide-vue-next'
import { useClubStore } from '@/stores/club'
import HarborQrCard from '@/components/admin/HarborQrCard.vue'
import {
  CARDS_PER_PAGE,
  GRID_HEIGHT_MM,
  chunkPages,
  parseCardsPerPage,
  targetPriceLine,
  type CardsPerPage,
} from '@/lib/qrSheet'

type Target = {
  id: string
  label: string
  unit_amount: number
  unit_label: string
  fixed_units: boolean
  archived: boolean
  public_url: string
  short_url: string
}

// Bound, not a literal src, so Vite doesn't try to import it as an asset.
const CLUB_LOGO_URL = '/api/v1/club/logo'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const club = useClubStore()

const targets = ref<Target[]>([])
const qrSrc = ref<Record<string, string>>({})
const loading = ref(true)
const error = ref('')

const per = computed<CardsPerPage>(() => parseCardsPerPage(route.query.per))

const requestedIds = computed(() =>
  String(route.query.ids ?? '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean),
)

// Requested order wins; unknown and archived ids drop out.
const selected = computed(() => {
  const byId = new Map(targets.value.filter((tg) => !tg.archived).map((tg) => [tg.id, tg]))
  return requestedIds.value.map((id) => byId.get(id)).filter((tg): tg is Target => !!tg)
})

const pages = computed(() => chunkPages(selected.value, per.value))

const ready = computed(
  () => !loading.value && selected.value.length > 0 && selected.value.every((tg) => qrSrc.value[tg.id]),
)

function printSheet() {
  window.print()
}

function instructions(page: Target[]): string {
  return page.every((tg) => tg.fixed_units)
    ? t('harborTargets.print.instructionsFixed')
    : t('harborTargets.print.instructions')
}

function setPer(v: CardsPerPage) {
  router.replace({ query: { ...route.query, per: String(v) } })
}

// Renders only the QRs not yet rendered, so switching the selection or the
// layout reuses what is already there.
async function renderQrs(list: Target[]) {
  const missing = list.filter((tg) => !qrSrc.value[tg.id])
  if (!missing.length) return
  // One bad URL leaves that card without a QR (and printing disabled)
  // instead of failing the whole sheet.
  const results = await Promise.allSettled(
    missing.map(async (tg) => {
      const svg = await QRCode.toString(tg.public_url, {
        type: 'svg',
        errorCorrectionLevel: 'M',
        margin: 4,
      })
      return [tg.id, `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`] as const
    }),
  )
  const rendered = results.flatMap((r) => (r.status === 'fulfilled' ? [r.value] : []))
  qrSrc.value = { ...qrSrc.value, ...Object.fromEntries(rendered) }
}

watch(selected, (list) => {
  void renderQrs(list)
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/v1/harbor/payment-targets', {
      credentials: 'include',
    })
    if (!res.ok) throw new Error(String(res.status))
    targets.value = await res.json()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

// The landscape page rule is injected only while this view is mounted, so it
// never leaks into printing other pages of the SPA.
let pageStyle: HTMLStyleElement | null = null

onMounted(async () => {
  pageStyle = document.createElement('style')
  pageStyle.textContent = '@page { size: A4 landscape; margin: 0; }'
  document.head.appendChild(pageStyle)
  await Promise.all([club.ensureLoaded(), load()])
})

onBeforeUnmount(() => {
  pageStyle?.remove()
})
</script>

<template>
  <div class="qr-sheet-root">
    <div class="qr-toolbar">
      <h1 class="text-base font-semibold text-slate-900">
        {{ t('harborTargets.print.title') }}
      </h1>
      <div class="flex items-center gap-1" role="group" :aria-label="t('harborTargets.print.perPage')">
        <button
          v-for="n in CARDS_PER_PAGE"
          :key="n"
          type="button"
          class="rounded-md border px-2.5 py-1 text-sm"
          :class="
            n === per
              ? 'border-slate-900 bg-slate-900 text-white'
              : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'
          "
          :aria-pressed="n === per"
          @click="setPer(n)"
        >
          {{ t('harborTargets.print.perPageOption', { n }) }}
        </button>
      </div>
      <button
        type="button"
        class="flex items-center gap-1.5 rounded-md bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-700 disabled:opacity-50"
        :disabled="!ready"
        data-testid="print-button"
        @click="printSheet"
      >
        <Printer class="h-4 w-4" /> {{ t('harborTargets.print.print') }}
      </button>
      <p class="w-full text-xs text-slate-500">
        {{ t('harborTargets.print.hint') }}
      </p>
    </div>

    <p v-if="error" class="qr-message text-red-700">{{ error }}</p>
    <p v-else-if="loading" class="qr-message text-slate-500">{{ t('common.loading') }}…</p>
    <p v-else-if="!selected.length" class="qr-message text-slate-500" data-testid="empty">
      {{ t('harborTargets.print.empty') }}
    </p>

    <template v-else>
      <section
        v-for="(page, i) in pages"
        :key="i"
        class="qr-page"
        :class="i < pages.length - 1 ? 'qr-page--break' : ''"
        data-testid="qr-page"
      >
        <header class="qr-page__header" data-testid="qr-page-header">
          <img v-if="club.hasLogo" :src="CLUB_LOGO_URL" alt="" class="qr-page__logo" data-testid="club-logo" />
          <div class="min-w-0">
            <p class="qr-page__club">{{ club.name }}</p>
            <p class="qr-page__instructions" data-testid="instructions">
              {{ instructions(page) }}
            </p>
          </div>
        </header>
        <div class="qr-page__grid" :style="{ height: `${GRID_HEIGHT_MM}mm` }">
          <HarborQrCard
            v-for="tg in page"
            :key="tg.id"
            :label="tg.label"
            :price="targetPriceLine(tg.unit_amount, tg.unit_label, tg.fixed_units)"
            :qr-src="qrSrc[tg.id] ?? ''"
            :per="per"
            :short-url="tg.short_url"
          />
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
/* Always the light "paper" palette, on screen as well as in print. */
.qr-sheet-root {
  min-height: 100vh;
  padding: 0 0 12mm;
  background: #e2e8f0;
  color: #0f172a;
  -webkit-print-color-adjust: exact;
  print-color-adjust: exact;
}

.qr-toolbar {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem 1rem;
  margin-bottom: 8mm;
  border-bottom: 1px solid #cbd5e1;
  background: #f8fafc;
}

.qr-message {
  padding: 2rem 1rem;
  text-align: center;
  font-size: 0.875rem;
}

.qr-page {
  box-sizing: border-box;
  width: 297mm;
  height: 210mm;
  margin: 0 auto 8mm;
  padding: 10mm;
  overflow: hidden;
  background: #fff;
  box-shadow: 0 2px 12px rgb(15 23 42 / 0.15);
}

.qr-page__header {
  box-sizing: border-box;
  display: flex;
  align-items: center;
  gap: 5mm;
  height: 18mm;
  margin-bottom: 6mm;
  padding-bottom: 3mm;
  border-bottom: 0.3mm solid #cbd5e1;
}

.qr-page__logo {
  height: 14mm;
  width: auto;
  flex-shrink: 0;
}

.qr-page__club {
  margin: 0;
  font-size: 20pt;
  font-weight: 700;
  line-height: 1.1;
  letter-spacing: -0.01em;
}

.qr-page__instructions {
  margin: 1mm 0 0;
  font-size: 11pt;
  color: #475569;
}

/* Cards keep the cell size fixed by cards-per-page; a short page centres
   them rather than stretching. */
.qr-page__grid {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  align-content: center;
  gap: 6mm;
}

@media print {
  .qr-toolbar,
  .qr-message {
    display: none;
  }

  .qr-sheet-root {
    min-height: 0;
    padding: 0;
    background: #fff;
  }

  .qr-page {
    margin: 0;
    box-shadow: none;
  }

  .qr-page--break {
    break-after: page;
  }
}
</style>
