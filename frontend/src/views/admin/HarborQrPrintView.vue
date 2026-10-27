<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import { Printer } from 'lucide-vue-next'
import { useClubStore } from '@/stores/club'
import { i18n, LOCALE_OPTIONS } from '@/i18n'
import HarborQrCard from '@/components/admin/HarborQrCard.vue'
import { useFitCards } from '@/composables/useFitCards'
import {
  CARDS_PER_PAGE,
  DEFAULT_PRINT_LANGS,
  PRINT_LANGS_STORAGE_KEY,
  cardTranslations,
  chunkPages,
  gridDims,
  normalisePrintLangs,
  parseCardsPerPage,
  parseLangList,
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
  label_translations?: Record<string, string>
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

const sheetRoot = ref<HTMLElement | null>(null)

function printSheet() {
  window.print()
}

const LOCALE_ORDER = LOCALE_OPTIONS.map((o) => o.code)

const clubLang = computed(() => club.defaultLanguage || 'nb')

// Storage can be unavailable (private mode, blocked site data); the
// defaults and the URL query still work then.
function readStoredLangs(): string[] | null {
  try {
    return parseLangList(localStorage.getItem(PRINT_LANGS_STORAGE_KEY))
  } catch {
    return null
  }
}

function storeLangs(list: string[]) {
  try {
    localStorage.setItem(PRINT_LANGS_STORAGE_KEY, list.join(','))
  } catch {
    // Not remembered for next time; the URL still carries the choice.
  }
}

const storedLangs = ref<string[] | null>(readStoredLangs())

// The query wins so a shared or bookmarked link reproduces the sheet.
const langs = computed(() =>
  normalisePrintLangs(
    parseLangList(route.query.langs) ?? storedLangs.value ?? DEFAULT_PRINT_LANGS,
    clubLang.value,
    LOCALE_ORDER,
  ),
)

// Anything that changes a card's text or the header (and so the card's
// height) calls for a new fit.
const { overflow, overflowCount } = useFitCards(sheetRoot, per, () => [
  pages.value,
  langs.value,
  ready.value,
  club.name,
  club.hasLogo,
])

// Each track is the size it has on a full page, so a page with fewer cards
// (a single card, the last page) keeps the cards at that size. Only the tracks
// the page needs are created, and the grid centres them.
const GRID_GAP = '6mm'
function gridStyle(count: number) {
  const { cols, rows } = gridDims(per.value)
  const usedCols = Math.min(cols, Math.max(count, 1))
  const usedRows = Math.min(rows, Math.ceil(Math.max(count, 1) / cols))
  const col = `calc((100% - ${cols - 1} * ${GRID_GAP}) / ${cols})`
  const row = `calc((100% - ${rows - 1} * ${GRID_GAP}) / ${rows})`
  return {
    gridTemplateColumns: `repeat(${usedCols}, ${col})`,
    gridTemplateRows: `repeat(${usedRows}, ${row})`,
  }
}

// Pathed rather than relative so the replace resolves even where the route
// has no record of its own.
function replaceQuery(patch: Record<string, string>) {
  router.replace({ path: route.path, query: { ...route.query, ...patch } })
}

function setLangs(list: string[]) {
  replaceQuery({ langs: list.join(',') })
}

function toggleLang(code: string) {
  if (code === clubLang.value) return
  const current = langs.value
  const next = normalisePrintLangs(
    current.includes(code) ? current.filter((c) => c !== code) : [...current, code],
    clubLang.value,
    LOCALE_ORDER,
  )
  storedLangs.value = next
  storeLangs(next)
  setLangs(next)
}

// Read straight from the bundled messages so each line is in its own
// language without switching the app's locale.
function messageIn(code: string, path: string): string {
  const value = path
    .split('.')
    .reduce<unknown>(
      (node, key) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[key] : undefined),
      i18n.global.getLocaleMessage(code),
    )
  return typeof value === 'string' ? value : ''
}

function instructionLines(page: Target[]): { code: string; text: string }[] {
  const key = page.every((tg) => tg.fixed_units)
    ? 'harborTargets.print.instructionsFixed'
    : 'harborTargets.print.instructions'
  return langs.value.map((code) => ({ code, text: messageIn(code, key) })).filter((l) => l.text)
}

function translationsOf(tg: Target): string[] {
  return cardTranslations(tg.label_translations, langs.value, clubLang.value)
}

function setPer(v: CardsPerPage) {
  replaceQuery({ per: String(v) })
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
  // Put the effective choice in the URL so a copied link prints the same
  // sheet for someone with other remembered languages. Waiting for the
  // router keeps the replace from dropping a query not yet resolved.
  await router.isReady()
  if (!route.query.langs) setLangs(langs.value)
})

onBeforeUnmount(() => {
  pageStyle?.remove()
})
</script>

<template>
  <div ref="sheetRoot" class="qr-sheet-root">
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
      <fieldset class="flex flex-wrap items-center gap-x-3 gap-y-1" data-testid="lang-picker">
        <legend class="sr-only">{{ t('harborTargets.print.languages') }}</legend>
        <span class="text-sm text-slate-700" aria-hidden="true">{{ t('harborTargets.print.languages') }}:</span>
        <label
          v-for="opt in LOCALE_OPTIONS"
          :key="opt.code"
          class="flex items-center gap-1 text-sm text-slate-700"
          :data-testid="`lang-${opt.code}`"
        >
          <input
            type="checkbox"
            :checked="langs.includes(opt.code)"
            :disabled="opt.code === clubLang"
            @change="toggleLang(opt.code)"
          />
          {{ opt.label }}
        </label>
      </fieldset>
      <button
        type="button"
        class="flex items-center gap-1.5 rounded-md bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-700 disabled:opacity-50"
        :disabled="!ready"
        data-testid="print-button"
        @click="printSheet"
      >
        <Printer class="h-4 w-4" /> {{ t('harborTargets.print.print') }}
      </button>
      <p
        v-if="overflowCount"
        class="w-full text-sm font-medium text-red-700"
        role="status"
        data-testid="overflow-count"
      >
        {{ t('harborTargets.print.overflowCount', { n: overflowCount }) }}
      </p>
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
          <p class="qr-page__club">{{ club.name }}</p>
          <div class="qr-page__instructions" data-testid="instructions">
            <p
              v-for="(line, j) in instructionLines(page)"
              :key="line.code"
              :lang="line.code"
              :class="j === 0 ? 'qr-page__instr-main' : 'qr-page__instr-other'"
              data-testid="instruction-line"
            >
              {{ line.text }}
            </p>
          </div>
        </header>
        <div class="qr-page__grid" :style="gridStyle(page.length)">
          <HarborQrCard
            v-for="tg in page"
            :key="tg.id"
            :label="tg.label"
            :price="targetPriceLine(tg.unit_amount, tg.unit_label, tg.fixed_units)"
            :qr-src="qrSrc[tg.id] ?? ''"
            :per="per"
            :short-url="tg.short_url"
            :translations="translationsOf(tg)"
            :overflow="!!overflow[tg.id]"
            :data-target-id="tg.id"
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

/* A fixed A4 page laid out as a column: the header takes the height its
   lines need, so no instruction line is ever clipped, and the card grid
   takes the rest. */
.qr-page {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
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
  flex: none;
  gap: 5mm;
  min-height: 14mm;
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
  flex-shrink: 0;
  max-width: 40%;
  margin: 0;
  font-size: 20pt;
  font-weight: 700;
  line-height: 1.1;
  letter-spacing: -0.01em;
}

/* Beside the club name: the club language a little larger, the others small
   and grey. */
.qr-page__instructions {
  flex: 1 1 0;
  min-width: 0;
}

.qr-page__instructions p {
  margin: 0;
}

.qr-page__instr-main {
  font-size: 10pt;
  line-height: 1.2;
  color: #334155;
}

.qr-page__instr-other {
  font-size: 7pt;
  line-height: 1.15;
  color: #64748b;
}

.qr-page__grid {
  flex: 1 1 0;
  min-height: 0;
  display: grid;
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
