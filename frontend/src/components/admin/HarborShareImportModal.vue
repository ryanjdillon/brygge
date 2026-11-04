<script setup lang="ts">
// Harbor share (moloandel) import, BRY-139 / BRD-14. Upload → dry run
// grouped by outcome → pick members for ambiguous and unmatched rows (each
// pick re-runs the dry run, so the preview is always what Apply will write) →
// apply with the preview's plan_token. The server answers 409 plan_changed if
// members changed since the preview; the preview is then re-run.
// The caller prompts ensureFreshTotp() before opening this.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import FileInput from '@/components/ui/form/FileInput.vue'
import MemberSearch, { type MemberHit } from '@/components/members/MemberSearch.vue'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { formatDate } from '@/lib/format'
import type { components } from '@/types/api'

type Result = components['schemas']['HarborShareImportResult']
type Row = components['schemas']['HarborShareImportRow']
type Outcome = Row['outcome']
type State = components['schemas']['HarborShareState']
type Candidate = components['schemas']['HarborShareCandidate']

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'applied'): void
}>()

const { t, locale } = useI18n()
const { totpAwareFetch } = useFreshTotp()

const file = ref<File | null>(null)
const preview = ref<Result | null>(null)
const applied = ref<Result | null>(null)
const resolutions = ref<Record<number, string>>({})
const searchPicks = ref<Record<number, MemberHit | null>>({})
const error = ref<string | null>(null)
const notice = ref<string | null>(null)
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    file.value = null
    preview.value = null
    applied.value = null
    resolutions.value = {}
    searchPicks.value = {}
    error.value = null
    notice.value = null
  },
  { immediate: true },
)

// Changes first, then what needs the operator, then rows that change nothing.
const OUTCOME_ORDER: Outcome[] = [
  'set', 'date_added', 'ambiguous', 'unmatched', 'conflict', 'not_owner_but_set',
  'duplicate', 'error', 'unchanged', 'skipped',
]
const OUTCOME_KEY: Record<Outcome, string> = {
  set: 'set',
  date_added: 'dateAdded',
  unchanged: 'unchanged',
  conflict: 'conflict',
  not_owner_but_set: 'notOwnerButSet',
  duplicate: 'duplicate',
  skipped: 'skipped',
  unmatched: 'unmatched',
  ambiguous: 'ambiguous',
  error: 'error',
}
const OUTCOME_TONE: Partial<Record<Outcome, string>> = {
  set: 'text-green-700',
  date_added: 'text-green-700',
  ambiguous: 'text-amber-700',
  unmatched: 'text-amber-700',
  conflict: 'text-red-700',
  not_owner_but_set: 'text-red-700',
  duplicate: 'text-red-700',
  error: 'text-red-700',
}

const shown = computed(() => applied.value ?? preview.value)
const groups = computed(() => {
  const rows = shown.value?.rows ?? []
  return OUTCOME_ORDER.map((outcome) => ({ outcome, rows: rows.filter((r) => r.outcome === outcome) })).filter(
    (g) => g.rows.length > 0,
  )
})
const changeCount = computed(() => (preview.value ? preview.value.counts.set + preview.value.counts.date_added : 0))

function outcomeLabel(o: Outcome) {
  return t(`admin.users.harborShareImport.outcome.${OUTCOME_KEY[o]}`)
}

function stateLabel(s: State | null | undefined) {
  if (!s) return '—'
  if (!s.owner) return t('admin.users.harborShareImport.notOwner')
  return s.paid_on
    ? t('admin.users.harborShareYesPaid', { date: formatDate(s.paid_on, locale.value) })
    : t('admin.users.harborShareYesUnknownDate')
}

function slipsLabel(c: Candidate) {
  return c.slips?.length
    ? t('admin.users.harborShareImport.slips', { slips: c.slips.join(', ') })
    : t('admin.users.harborShareImport.noSlip')
}

function memberLabel(r: Row) {
  return [r.name, r.email].filter(Boolean).join(' · ')
}

function close() {
  emit('update:open', false)
}

function onFile(files: FileList | null) {
  file.value = files?.[0] ?? null
  error.value = null
}

async function send(dryRun: boolean): Promise<Response | null> {
  if (!file.value) return null
  const fd = new FormData()
  fd.append('file', file.value)
  fd.append('dry_run', String(dryRun))
  if (Object.keys(resolutions.value).length > 0) fd.append('resolutions', JSON.stringify(resolutions.value))
  if (!dryRun && preview.value) fd.append('plan_token', preview.value.plan_token)
  return totpAwareFetch('/api/v1/admin/users/harbor-shares/import', { method: 'POST', credentials: 'include', body: fd })
}

async function readBody(res: Response): Promise<{ error?: string; code?: string } | null> {
  return res.json().catch(() => null)
}

function errorText(res: Response, body: { error?: string } | null) {
  // totpAwareFetch already re-prompted on a stale step-up; a lingering 403
  // means the prompt was dismissed.
  if (res.status === 403) return t('admin.users.importFreshRequired')
  return body?.error ?? t('admin.users.importError')
}

// A row the server now matches by itself ignores its pick on a dry run and
// refuses it on apply, so the pick is forgotten here.
function dropIgnoredResolutions(result: Result) {
  const dropped = (result.rows ?? []).filter((r) => r.resolution_dropped).map((r) => r.row)
  if (dropped.length === 0) return
  const nextResolutions = { ...resolutions.value }
  const nextPicks = { ...searchPicks.value }
  for (const row of dropped) {
    delete nextResolutions[row]
    delete nextPicks[row]
  }
  resolutions.value = nextResolutions
  searchPicks.value = nextPicks
}

async function runPreview(): Promise<boolean> {
  error.value = null
  busy.value = true
  try {
    const res = await send(true)
    if (!res) return false
    if (!res.ok) {
      error.value = errorText(res, await readBody(res))
      return false
    }
    const result = (await res.json()) as Result
    dropIgnoredResolutions(result)
    preview.value = result
    return true
  } catch {
    error.value = t('admin.users.importError')
    return false
  } finally {
    busy.value = false
  }
}

async function apply() {
  if (changeCount.value === 0) return
  error.value = null
  notice.value = null
  busy.value = true
  try {
    const res = await send(false)
    if (!res) return
    if (!res.ok) {
      const body = await readBody(res)
      if (res.status === 409 && body?.code === 'plan_changed') {
        notice.value = t('admin.users.harborShareImport.planChanged')
        busy.value = false
        await runPreview()
        return
      }
      error.value = errorText(res, body)
      return
    }
    applied.value = (await res.json()) as Result
    emit('applied')
  } catch {
    error.value = t('admin.users.importError')
  } finally {
    busy.value = false
  }
}

// Each pick re-runs the preview. A pick the server refuses is reverted, so it
// does not ride along on every later preview and apply.
async function resolve(row: number, userId: string | null, hit: MemberHit | null = null) {
  const prevResolutions = resolutions.value
  const prevPicks = searchPicks.value
  const next = { ...resolutions.value }
  if (userId) next[row] = userId
  else delete next[row]
  resolutions.value = next
  searchPicks.value = { ...searchPicks.value, [row]: hit }
  if (!(await runPreview())) {
    resolutions.value = prevResolutions
    searchPicks.value = prevPicks
  }
}

async function onSearchPick(row: number, hit: MemberHit | null) {
  if (hit) await resolve(row, hit.id, hit)
  else searchPicks.value = { ...searchPicks.value, [row]: null }
}

async function undoResolution(row: number) {
  await resolve(row, null)
}

function backToUpload() {
  preview.value = null
  resolutions.value = {}
  searchPicks.value = {}
  notice.value = null
}
</script>

<template>
  <Modal
    :open="open"
    :title="t('admin.users.harborShareImport.title')"
    size="4xl"
    full-screen-on-mobile
    @update:open="(v: boolean) => !v && close()"
  >
    <!-- Upload -->
    <div v-if="!preview" data-testid="hsi-upload">
      <p class="text-sm text-gray-600">{{ t('admin.users.harborShareImport.description') }}</p>
      <p class="mt-2 text-xs text-gray-500">{{ t('admin.users.harborShareImport.formatHint') }}</p>
      <pre class="mt-2 overflow-x-auto rounded bg-gray-50 p-2 text-xs text-gray-700">name;email;harbor_share_owner;harbor_share_paid_on
Ola Nordmann;;ja;
Kari Nordmann;kari@example.com;ja;15.03.2026</pre>
      <FileInput accept=".csv,text/csv" class="mt-3 block text-sm" @change="onFile" />
      <p v-if="error" class="mt-2 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700" data-testid="hsi-error">{{ error }}</p>
      <div class="mt-4 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <button type="button" class="min-h-11 rounded-md px-3 py-2 text-sm text-gray-600 hover:bg-gray-100" @click="close">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          data-testid="hsi-preview"
          :disabled="!file || busy"
          class="min-h-11 rounded-md bg-brand-600 px-3 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          @click="runPreview"
        >
          {{ busy ? t('common.loading') : t('admin.users.harborShareImport.preview') }}
        </button>
      </div>
    </div>

    <!-- Preview, or the applied result -->
    <div v-else>
      <p v-if="applied" class="rounded-md bg-green-50 px-3 py-2 text-sm text-green-800" data-testid="hsi-applied">
        {{ t('admin.users.harborShareImport.appliedSummary', { set: applied.counts.set, dateAdded: applied.counts.date_added }) }}
      </p>
      <p v-else class="text-sm text-gray-600">{{ t('admin.users.harborShareImport.previewIntro') }}</p>
      <p v-if="notice" class="mt-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800" data-testid="hsi-notice">{{ notice }}</p>
      <p v-if="error" class="mt-2 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700" data-testid="hsi-error">{{ error }}</p>

      <section v-for="g in groups" :key="g.outcome" class="mt-4" :data-testid="`hsi-group-${g.outcome}`">
        <h3 class="text-sm font-semibold" :class="OUTCOME_TONE[g.outcome] ?? 'text-gray-700'">
          {{ outcomeLabel(g.outcome) }} ({{ g.rows.length }})
        </h3>
        <p class="text-xs text-gray-500">{{ t(`admin.users.harborShareImport.outcomeHint.${OUTCOME_KEY[g.outcome]}`) }}</p>

        <!-- Table from md up -->
        <table class="mt-2 hidden min-w-full text-xs md:table">
          <thead class="bg-gray-50">
            <tr>
              <th class="px-2 py-1 text-left font-medium text-gray-600">#</th>
              <th class="px-2 py-1 text-left font-medium text-gray-600">{{ t('admin.users.harborShareImport.colMember') }}</th>
              <th class="px-2 py-1 text-left font-medium text-gray-600">{{ t('admin.users.harborShareImport.colCurrent') }}</th>
              <th class="px-2 py-1 text-left font-medium text-gray-600">{{ t('admin.users.harborShareImport.colProposed') }}</th>
              <th class="px-2 py-1 text-left font-medium text-gray-600">{{ t('admin.users.harborShareImport.colNote') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            <tr v-for="r in g.rows" :key="r.row" class="align-top">
              <td class="px-2 py-1 text-gray-500">{{ r.row }}</td>
              <td class="px-2 py-1 text-gray-800">
                {{ memberLabel(r) }}
                <span v-if="r.deactivated" class="ml-1 rounded bg-gray-100 px-1 text-gray-600">{{ t('admin.users.harborShareImport.deactivated') }}</span>
              </td>
              <td class="px-2 py-1 text-gray-600">{{ stateLabel(r.current) }}</td>
              <td class="px-2 py-1 text-gray-800">{{ stateLabel(r.proposed) }}</td>
              <td class="px-2 py-1 text-gray-600">
                <template v-if="!applied">
                  <div v-if="r.outcome === 'ambiguous'" class="flex flex-col gap-1">
                    <span>{{ t('admin.users.harborShareImport.chooseCandidate') }}</span>
                    <button
                      v-for="c in r.candidates ?? []"
                      :key="c.user_id"
                      type="button"
                      :data-testid="`hsi-candidate-${r.row}-${c.user_id}`"
                      class="min-h-11 rounded-md border border-gray-300 px-2 py-1 text-left hover:border-brand-500 hover:bg-brand-50"
                      :disabled="busy"
                      @click="resolve(r.row, c.user_id)"
                    >
                      {{ c.name }} ·
                      {{ slipsLabel(c) }}
                      <span v-if="c.deactivated"> · {{ t('admin.users.harborShareImport.deactivated') }}</span>
                    </button>
                  </div>
                  <MemberSearch
                    v-else-if="r.outcome === 'unmatched'"
                    :model-value="searchPicks[r.row] ?? null"
                    :placeholder="t('admin.users.harborShareImport.searchMember')"
                    status="all"
                    @update:model-value="(m: MemberHit | null) => onSearchPick(r.row, m)"
                  />
                  <button
                    v-else-if="r.resolved"
                    type="button"
                    class="min-h-11 text-brand-700 underline"
                    :data-testid="`hsi-undo-${r.row}`"
                    @click="undoResolution(r.row)"
                  >
                    {{ t('admin.users.harborShareImport.undoResolution') }}
                  </button>
                </template>
                <span v-if="r.message" class="block">{{ r.message }}</span>
              </td>
            </tr>
          </tbody>
        </table>

        <!-- Stacked cards below md -->
        <ul class="mt-2 space-y-2 md:hidden">
          <li v-for="r in g.rows" :key="r.row" class="rounded-md border border-gray-200 p-3 text-sm">
            <div class="flex items-start justify-between gap-2">
              <span class="font-medium text-gray-900">{{ memberLabel(r) }}</span>
              <span class="text-xs text-gray-500">#{{ r.row }}</span>
            </div>
            <span v-if="r.deactivated" class="mt-1 inline-block rounded bg-gray-100 px-1 text-xs text-gray-600">{{ t('admin.users.harborShareImport.deactivated') }}</span>
            <dl v-if="r.current || r.proposed" class="mt-1 grid grid-cols-2 gap-x-2 text-xs">
              <dt class="text-gray-500">{{ t('admin.users.harborShareImport.colCurrent') }}</dt>
              <dd class="text-gray-700">{{ stateLabel(r.current) }}</dd>
              <dt class="text-gray-500">{{ t('admin.users.harborShareImport.colProposed') }}</dt>
              <dd class="text-gray-900">{{ stateLabel(r.proposed) }}</dd>
            </dl>
            <template v-if="!applied">
              <div v-if="r.outcome === 'ambiguous'" class="mt-2 flex flex-col gap-1">
                <span class="text-xs text-gray-600">{{ t('admin.users.harborShareImport.chooseCandidate') }}</span>
                <button
                  v-for="c in r.candidates ?? []"
                  :key="c.user_id"
                  type="button"
                  class="min-h-11 rounded-md border border-gray-300 px-3 py-2 text-left text-sm hover:border-brand-500 hover:bg-brand-50"
                  :disabled="busy"
                  @click="resolve(r.row, c.user_id)"
                >
                  {{ c.name }} ·
                  {{ slipsLabel(c) }}
                  <span v-if="c.deactivated"> · {{ t('admin.users.harborShareImport.deactivated') }}</span>
                </button>
              </div>
              <MemberSearch
                v-else-if="r.outcome === 'unmatched'"
                class="mt-2"
                :model-value="searchPicks[r.row] ?? null"
                :placeholder="t('admin.users.harborShareImport.searchMember')"
                status="all"
                @update:model-value="(m: MemberHit | null) => onSearchPick(r.row, m)"
              />
              <button
                v-else-if="r.resolved"
                type="button"
                class="mt-1 min-h-11 text-sm text-brand-700 underline"
                @click="undoResolution(r.row)"
              >
                {{ t('admin.users.harborShareImport.undoResolution') }}
              </button>
            </template>
            <p v-if="r.message" class="mt-1 text-xs text-gray-600">{{ r.message }}</p>
          </li>
        </ul>
      </section>

      <div class="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <template v-if="applied">
          <button
            type="button"
            class="min-h-11 rounded-md bg-brand-600 px-3 py-2 text-sm font-semibold text-white hover:bg-brand-700"
            @click="close"
          >
            {{ t('common.close') }}
          </button>
        </template>
        <template v-else>
          <button type="button" class="min-h-11 rounded-md px-3 py-2 text-sm text-gray-600 hover:bg-gray-100" @click="backToUpload">
            {{ t('admin.users.harborShareImport.chooseAnotherFile') }}
          </button>
          <button
            type="button"
            data-testid="hsi-apply"
            :disabled="changeCount === 0 || busy"
            class="min-h-11 rounded-md bg-brand-600 px-3 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
            @click="apply"
          >
            {{ busy ? t('common.loading') : t('admin.users.harborShareImport.apply', { n: changeCount }) }}
          </button>
        </template>
      </div>
    </div>
  </Modal>
</template>
