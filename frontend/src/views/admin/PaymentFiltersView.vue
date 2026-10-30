<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowLeft, ArrowUp, ArrowDown, Pencil, Plus, TriangleAlert } from 'lucide-vue-next'
import Modal from '@/components/ui/Modal.vue'
import MultiSelectList from '@/components/ui/MultiSelectList.vue'
import PaymentGroupIcon from '@/components/admin/PaymentGroupIcon.vue'
import { ApiError } from '@/lib/errors'
import { groupLabel, PAYMENT_FILTER_ICON_IDS, type PaymentFilterIcon } from '@/lib/paymentFilterIcons'
import { priceCategoryKey } from '@/lib/priceCategories'
import { useCan } from '@/composables/useCan'
import { useToast } from '@/composables/useToast'
import { useConfirm } from '@/stores/confirm'
import { usePaymentFilterGroups, type PaymentFilterGroup } from '@/composables/usePaymentFilterGroups'

// The Payments page's filter groups (BRY-522): which price items each chip
// holds, in which order, and which chips are shown. Reached from the Payments
// page; every admin viewer may read it, the roles in WRITE_ROLES.paymentFilters
// change it. The server decides what an order's group is
// (internal/paymentfilters); this page only edits the configuration.
const { t } = useI18n()
const toast = useToast()
const confirm = useConfirm()
const { can } = useCan()
const canEdit = can('paymentFilters')

const { groups, priceItems, loading, loadFailed, loaded, load, create, update, setItems, reorder, remove } =
  usePaymentFilterGroups()

const PAYMENTS_PATH = '/admin/harbor/recent-payments'
const NAME_MAX = 40
const KNOWN_ERRORS = [
  'invalid_name',
  'reserved_name',
  'name_taken',
  'invalid_icon',
  'too_many_groups',
  'builtin_group',
  'no_default_name',
  'unknown_price_item',
  'order_mismatch',
  'not_found',
]

function errorMessage(err: unknown): string {
  const code = err instanceof ApiError ? err.code : undefined
  return code && KNOWN_ERRORS.includes(code) ? t(`paymentFilters.errors.${code}`) : t('paymentFilters.errors.generic')
}

function label(g: PaymentFilterGroup): string {
  return groupLabel(g, t)
}

function groupBySlug(slug: string | null | undefined): PaymentFilterGroup | undefined {
  return slug ? groups.value.find((g) => g.slug === slug) : undefined
}

const fallbackGroup = computed(() => groups.value.find((g) => g.builtin === 'other'))

function describe(g: PaymentFilterGroup): string {
  if (g.builtin === 'faktura') return t('paymentFilters.builtinFaktura')
  if (g.builtin === 'other') return t('paymentFilters.builtinOther')
  return t('paymentFilters.itemCount', { count: g.item_count }, g.item_count)
}

// Active price items that have been sold (an active QR poster has them, or an
// order line does) and are in no group: their payments show under Annet. An
// item nobody sold is not listed, so leaving others ungrouped is not nagged.
const unassigned = computed(() => priceItems.value.filter((p) => p.is_active && p.sold && !p.group_slug))

// Reorder -------------------------------------------------------------------

// Up and down are disabled while a reorder is in flight, so two taps cannot
// send two orders computed from the same list.
const reordering = ref(false)

const movable = computed(() => groups.value.filter((g) => g.builtin !== 'other'))

function canMove(g: PaymentFilterGroup, dir: -1 | 1): boolean {
  if (g.builtin === 'other' || reordering.value) return false
  const i = movable.value.findIndex((m) => m.slug === g.slug)
  return i >= 0 && i + dir >= 0 && i + dir < movable.value.length
}

async function move(g: PaymentFilterGroup, dir: -1 | 1) {
  if (!canMove(g, dir)) return
  const before = groups.value
  const order = movable.value.map((m) => m.slug)
  const i = order.indexOf(g.slug)
  ;[order[i], order[i + dir]] = [order[i + dir], order[i]]
  // Optimistic: the list moves at once, and goes back if the server refuses.
  groups.value = [
    ...order.map((slug) => before.find((b) => b.slug === slug)!),
    ...before.filter((b) => b.builtin === 'other'),
  ]
  reordering.value = true
  try {
    groups.value = (await reorder(order)).groups
  } catch (err) {
    groups.value = before
    toast.error(errorMessage(err))
    // Someone else changed the groups (order_mismatch), or the server is
    // not what the list showed: read it again either way.
    await load()
  } finally {
    reordering.value = false
  }
}

// Enabled switch -------------------------------------------------------------

const switching = ref<string[]>([])

async function toggleEnabled(g: PaymentFilterGroup) {
  if (!canEdit || switching.value.includes(g.slug)) return
  switching.value = [...switching.value, g.slug]
  try {
    await update(g.slug, { enabled: !g.enabled })
  } catch (err) {
    toast.error(errorMessage(err))
  } finally {
    await load()
    switching.value = switching.value.filter((s) => s !== g.slug)
  }
}

// Editor ----------------------------------------------------------------------

interface Editor {
  mode: 'new' | 'edit'
  slug: string | null
  builtin: string | null
  labelKey: string | null
  // The stored name; null on a seeded row that still shows its default.
  currentName: string | null
  name: string
  reset: boolean
  icon: PaymentFilterIcon
  initialIcon: PaymentFilterIcon
  itemIds: string[]
  initialItemIds: string[]
}

const editor = ref<Editor | null>(null)
const saving = ref(false)
const formError = ref('')

function openNew() {
  editor.value = {
    mode: 'new',
    slug: null,
    builtin: null,
    labelKey: null,
    currentName: null,
    name: '',
    reset: false,
    icon: PAYMENT_FILTER_ICON_IDS[0],
    initialIcon: PAYMENT_FILTER_ICON_IDS[0],
    itemIds: [],
    initialItemIds: [],
  }
  formError.value = ''
}

function openEdit(g: PaymentFilterGroup) {
  editor.value = {
    mode: 'edit',
    slug: g.slug,
    builtin: g.builtin,
    labelKey: g.label_key,
    currentName: g.name,
    name: g.name ?? '',
    reset: false,
    icon: g.icon,
    initialIcon: g.icon,
    itemIds: [...g.price_item_ids],
    initialItemIds: [...g.price_item_ids],
  }
  formError.value = ''
}

function closeEditor() {
  if (saving.value) return
  editor.value = null
}

const editorDefaultLabel = computed(() => (editor.value?.labelKey ? t(`recentPayments.kind.${editor.value.labelKey}`) : ''))

// "Use the default name" is offered where a default exists and a name has
// been set (stored, or typed).
const canResetName = computed(() => {
  const e = editor.value
  return !!e && !!e.labelKey && !e.reset && (e.currentName !== null || e.name.trim() !== '')
})

function resetName() {
  if (editor.value) {
    editor.value.name = ''
    editor.value.reset = true
  }
}

function onNameInput() {
  if (editor.value) editor.value.reset = false
}

// The picker lists items that can reach an order first; an item that cannot
// (the server decides from the checkout categories, QR posters and order lines)
// follows with a note, since what a faktura bills never classifies an order.
const itemOptions = computed(() => {
  const slug = editor.value?.slug ?? null
  return [...priceItems.value]
    .sort((a, b) => Number(b.order_reachable) - Number(a.order_reachable))
    .map((p) => {
      const categoryKey = priceCategoryKey(p.category)
      const hint = [
        categoryKey ? t(categoryKey) : p.category,
        p.is_active ? '' : t('paymentFilters.editor.archived'),
        p.order_reachable ? '' : t('paymentFilters.editor.fakturaOnly'),
        p.qr_labels.length ? t('paymentFilters.editor.qr', { labels: p.qr_labels.join(', ') }) : '',
      ]
        .filter(Boolean)
        .join(' · ')
      const elsewhere = p.group_slug && p.group_slug !== slug ? groupBySlug(p.group_slug) : undefined
      return {
        value: p.id,
        label: p.name,
        hint,
        meta: elsewhere ? t('paymentFilters.editor.inGroup', { group: label(elsewhere) }) : undefined,
        // "All" must not move items out of other groups in one tap; each such
        // item is ticked on purpose (and confirmed on save).
        bulkSkip: !p.order_reachable || !!elsewhere,
      }
    })
})

// The items ticked here that are in another group now: saving moves them.
function pendingMoves(e: Editor) {
  return e.itemIds.flatMap((id) => {
    const item = priceItems.value.find((p) => p.id === id)
    const from = item?.group_slug && item.group_slug !== e.slug ? groupBySlug(item.group_slug) : undefined
    return item && from ? [{ item, from }] : []
  })
}

function sameSet(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((x) => b.includes(x))
}

async function save() {
  const e = editor.value
  if (!e || saving.value) return
  formError.value = ''

  let name = e.name.trim()
  let reset = e.reset
  if (e.mode === 'edit' && !reset && name === '' && e.currentName !== null) {
    // Emptying a stored name goes back to the default where there is one.
    if (e.labelKey) reset = true
    else {
      formError.value = t('paymentFilters.errors.invalid_name')
      return
    }
  }
  if (e.mode === 'new' && name === '') {
    formError.value = t('paymentFilters.errors.invalid_name')
    return
  }
  const nameChanged = !reset && name !== '' && name !== (e.currentName ?? '')
  // The no-filter chip's name would read the same as it; the server refuses it
  // in every locale, this is the label this one shows.
  if (nameChanged && name.toLowerCase() === t('recentPayments.kind.all').toLowerCase()) {
    formError.value = t('paymentFilters.errors.reserved_name')
    return
  }
  // Two chips that read the same cannot be told apart: check against what the
  // other groups render in this language, seeded defaults included.
  if (nameChanged && groups.value.some((g) => g.slug !== e.slug && label(g).toLowerCase() === name.toLowerCase())) {
    formError.value = t('paymentFilters.errors.name_taken')
    return
  }
  if (reset) name = ''

  // In flight from here, before the confirm: a second submit while the dialog
  // is open must not start a second save.
  saving.value = true
  const moves = pendingMoves(e).filter(({ item }) => !e.initialItemIds.includes(item.id))
  if (moves.length > 0) {
    const target = reset || !name ? (e.labelKey ? editorDefaultLabel.value : (e.currentName ?? '')) : name
    const ok = await confirm({
      title: t('paymentFilters.moveConfirm.title'),
      body: t('paymentFilters.moveConfirm.body', { count: moves.length, group: target }, moves.length),
      details: moves.map(({ item, from }) => `${item.name}: ${label(from)} → ${target}`),
      confirmLabel: t('paymentFilters.moveConfirm.confirm'),
      tone: 'warning',
    })
    if (!ok) {
      saving.value = false
      return
    }
  }

  try {
    if (e.mode === 'new') {
      await create({ name, icon: e.icon, price_item_ids: e.itemIds })
    } else if (e.slug) {
      const body: { name?: string; reset_name?: boolean; icon?: PaymentFilterIcon } = {}
      if (nameChanged) body.name = name
      if (reset && e.currentName !== null) body.reset_name = true
      if (e.icon !== e.initialIcon) body.icon = e.icon
      if (Object.keys(body).length > 0) await update(e.slug, body)
      if (!e.builtin && !sameSet(e.itemIds, e.initialItemIds)) await setItems(e.slug, e.itemIds)
    }
    editor.value = null
    toast.success(t('paymentFilters.saved'))
  } catch (err) {
    formError.value = errorMessage(err)
  } finally {
    saving.value = false
    // A write that failed halfway leaves the server ahead of the list.
    await load()
  }
}

async function removeGroup() {
  const e = editor.value
  if (!e || !e.slug || e.builtin || saving.value) return
  saving.value = true
  const ok = await confirm({
    title: t('paymentFilters.delete.title', { name: e.currentName ?? editorDefaultLabel.value }),
    body: t('paymentFilters.delete.body', { count: e.initialItemIds.length }, e.initialItemIds.length),
    confirmLabel: t('common.delete'),
    tone: 'danger',
  })
  if (!ok) {
    saving.value = false
    return
  }
  try {
    await remove(e.slug)
    editor.value = null
    toast.success(t('paymentFilters.deleted'))
  } catch (err) {
    formError.value = errorMessage(err)
  } finally {
    saving.value = false
    await load()
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-3xl px-3 py-6 sm:px-6">
    <RouterLink
      :to="PAYMENTS_PATH"
      class="inline-flex min-h-11 items-center gap-1.5 text-sm font-medium text-slate-600 hover:text-slate-900"
      data-testid="back-link"
    >
      <ArrowLeft class="h-4 w-4" aria-hidden="true" />
      {{ t('paymentFilters.back') }}
    </RouterLink>

    <div class="mt-1 flex items-start justify-between gap-3">
      <div class="min-w-0">
        <h1 class="text-xl font-bold text-slate-900 sm:text-2xl">{{ t('paymentFilters.title') }}</h1>
        <p class="mt-1 text-sm text-slate-500">{{ t('paymentFilters.subtitle') }}</p>
      </div>
      <button
        v-if="canEdit"
        type="button"
        class="flex min-h-11 min-w-11 shrink-0 items-center justify-center gap-1.5 rounded-md bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-800"
        :aria-label="t('paymentFilters.newGroup')"
        data-testid="new-group"
        @click="openNew"
      >
        <Plus class="h-4 w-4" aria-hidden="true" />
        <span class="hidden sm:inline">{{ t('paymentFilters.newGroup') }}</span>
      </button>
    </div>

    <p
      v-if="!canEdit"
      class="mt-4 rounded-md bg-slate-50 px-3 py-2 text-sm text-slate-600"
      data-testid="read-only-note"
    >
      {{ t('paymentFilters.readOnly') }}
    </p>

    <div
      v-if="loadFailed"
      role="alert"
      class="mt-5 flex flex-wrap items-center gap-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800"
      data-testid="load-error"
    >
      <TriangleAlert class="h-4 w-4 shrink-0" aria-hidden="true" />
      <span class="min-w-0 flex-1">{{ t('paymentFilters.loadError') }}</span>
      <button
        type="button"
        class="min-h-11 shrink-0 rounded-md border border-red-300 bg-white px-4 text-sm font-medium text-red-800 hover:bg-red-100"
        :disabled="loading"
        data-testid="load-retry"
        @click="load"
      >
        {{ t('paymentFilters.retry') }}
      </button>
    </div>

    <p v-else-if="!loaded" class="mt-6 text-sm text-slate-500">{{ t('common.loading') }}</p>

    <template v-if="loaded">
      <!-- A viewer without write access sees the state, with the controls off -->
      <fieldset :disabled="!canEdit" class="m-0 mt-5 min-w-0 border-0 p-0">
        <ul class="space-y-3" data-testid="group-list">
          <li
            v-for="g in groups"
            :key="g.slug"
            class="rounded-lg border border-slate-200 bg-white p-3"
            :class="g.enabled ? '' : 'bg-slate-50'"
            data-testid="filter-group"
            :data-slug="g.slug"
          >
            <div class="flex items-center gap-3">
              <PaymentGroupIcon :icon="g.icon" size="lg" />
              <div class="min-w-0 flex-1">
                <p class="truncate font-medium text-slate-900" data-testid="group-label">{{ label(g) }}</p>
                <p class="text-sm text-slate-500">
                  {{ describe(g) }}
                  <span
                    v-if="!g.enabled"
                    class="ml-1 rounded bg-slate-200 px-1.5 py-0.5 text-xs font-medium text-slate-700"
                    data-testid="hidden-badge"
                  >
                    {{ t('paymentFilters.hidden') }}
                  </span>
                </p>
              </div>
            </div>

            <div class="mt-2 flex flex-wrap items-center justify-end gap-2">
              <!-- The fallback group is always shown, always last -->
              <button
                v-if="g.builtin !== 'other'"
                type="button"
                role="switch"
                :aria-checked="g.enabled"
                :aria-label="t('paymentFilters.showAria', { name: label(g) })"
                class="flex min-h-11 min-w-11 items-center justify-center rounded-md disabled:opacity-50"
                :disabled="!canEdit || switching.includes(g.slug)"
                data-testid="enabled-switch"
                @click="toggleEnabled(g)"
              >
                <span
                  class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors"
                  :class="g.enabled ? 'bg-slate-900' : 'bg-slate-300'"
                  aria-hidden="true"
                >
                  <span
                    class="inline-block h-5 w-5 transform rounded-full bg-white transition-transform"
                    :class="g.enabled ? 'translate-x-5' : 'translate-x-0.5'"
                  />
                </span>
              </button>
              <template v-if="canEdit && g.builtin !== 'other'">
                <button
                  type="button"
                  class="flex min-h-11 min-w-11 items-center justify-center rounded-md border border-slate-300 bg-white text-slate-700 hover:bg-slate-50 disabled:opacity-40"
                  :aria-label="t('paymentFilters.moveUp', { name: label(g) })"
                  :disabled="!canMove(g, -1)"
                  data-testid="move-up"
                  @click="move(g, -1)"
                >
                  <ArrowUp class="h-4 w-4" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  class="flex min-h-11 min-w-11 items-center justify-center rounded-md border border-slate-300 bg-white text-slate-700 hover:bg-slate-50 disabled:opacity-40"
                  :aria-label="t('paymentFilters.moveDown', { name: label(g) })"
                  :disabled="!canMove(g, 1)"
                  data-testid="move-down"
                  @click="move(g, 1)"
                >
                  <ArrowDown class="h-4 w-4" aria-hidden="true" />
                </button>
              </template>
              <button
                v-if="canEdit"
                type="button"
                class="flex min-h-11 items-center gap-1.5 rounded-md border border-slate-300 bg-white px-4 text-sm font-medium text-slate-700 hover:bg-slate-50"
                :aria-label="t('paymentFilters.editGroup', { name: label(g) })"
                data-testid="edit-group"
                @click="openEdit(g)"
              >
                <Pencil class="h-4 w-4" aria-hidden="true" />
                {{ t('paymentFilters.edit') }}
              </button>
            </div>
          </li>
        </ul>
      </fieldset>

      <!-- Items nobody has put in a group: their payments show under Annet -->
      <section class="mt-8" data-testid="unassigned-panel">
        <h2 class="text-sm font-semibold text-slate-900">
          {{ t('paymentFilters.unassignedTitle') }}
          <span class="ml-1 font-normal text-slate-500" data-testid="unassigned-count">({{ unassigned.length }})</span>
        </h2>
        <p v-if="unassigned.length === 0" class="mt-1 text-sm text-slate-500">{{ t('paymentFilters.unassignedNone') }}</p>
        <template v-else>
          <p class="mt-1 text-sm text-slate-500">
            {{ t('paymentFilters.unassignedBody', { group: fallbackGroup ? label(fallbackGroup) : t('recentPayments.kind.other') }) }}
          </p>
          <ul class="mt-2 flex flex-wrap gap-2">
            <li
              v-for="p in unassigned"
              :key="p.id"
              class="max-w-full truncate rounded-full bg-slate-100 px-3 py-1 text-sm text-slate-700"
              data-testid="unassigned-item"
            >
              {{ p.name }}
            </li>
          </ul>
        </template>
      </section>
    </template>

    <!-- Editor: full screen on phones -->
    <Modal
      :open="editor !== null"
      :title="editor?.mode === 'new' ? t('paymentFilters.editor.titleNew') : t('paymentFilters.editor.titleEdit')"
      size="xl"
      full-screen-on-mobile
      @update:open="(v: boolean) => { if (!v) closeEditor() }"
    >
      <form v-if="editor" class="space-y-5" data-testid="group-editor" @submit.prevent="save">
        <div>
          <label for="pfg-name" class="block text-sm font-medium text-slate-900">{{ t('paymentFilters.editor.name') }}</label>
          <input
            id="pfg-name"
            v-model="editor.name"
            type="text"
            :maxlength="NAME_MAX"
            :placeholder="editorDefaultLabel"
            class="mt-1 min-h-11 w-full rounded-md border border-slate-300 px-3 py-2 text-base sm:text-sm"
            data-testid="name-input"
            @input="onNameInput"
          />
          <p class="mt-1 text-xs text-slate-500">{{ t('paymentFilters.editor.nameHelp') }}</p>
          <button
            v-if="canResetName"
            type="button"
            class="mt-1 min-h-11 text-sm font-medium text-slate-700 underline"
            data-testid="reset-name"
            @click="resetName"
          >
            {{ t('paymentFilters.editor.useDefault', { name: editorDefaultLabel }) }}
          </button>
        </div>

        <fieldset class="m-0 min-w-0 border-0 p-0">
          <legend class="text-sm font-medium text-slate-900">{{ t('paymentFilters.editor.icon') }}</legend>
          <div class="mt-2 flex flex-wrap gap-2" role="radiogroup" data-testid="icon-grid">
            <label v-for="id in PAYMENT_FILTER_ICON_IDS" :key="id" class="relative block h-11 w-11 cursor-pointer">
              <input
                v-model="editor.icon"
                type="radio"
                name="pfg-icon"
                :value="id"
                :aria-label="t(`paymentFilters.icons.${id}`)"
                class="peer sr-only"
              />
              <span
                class="flex h-11 w-11 items-center justify-center rounded-md ring-offset-1 hover:bg-slate-50 peer-checked:ring-2 peer-checked:ring-slate-900 peer-focus-visible:ring-2 peer-focus-visible:ring-slate-500"
              >
                <PaymentGroupIcon :icon="id" />
              </span>
            </label>
          </div>
        </fieldset>

        <div v-if="!editor.builtin">
          <p class="text-sm font-medium text-slate-900">{{ t('paymentFilters.editor.items') }}</p>
          <p class="mb-2 text-xs text-slate-500">{{ t('paymentFilters.editor.itemsHelp') }}</p>
          <MultiSelectList
            v-model="editor.itemIds"
            :options="itemOptions"
            :placeholder="t('paymentFilters.editor.itemsSearch')"
            :empty-text="t('paymentFilters.editor.itemsEmpty')"
            max-height-class="max-h-72"
          />
        </div>

        <p v-if="formError" role="alert" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-800" data-testid="form-error">
          {{ formError }}
        </p>

        <div class="flex flex-wrap items-center justify-between gap-2 border-t border-slate-100 pt-4">
          <button
            v-if="editor.mode === 'edit' && !editor.builtin"
            type="button"
            class="min-h-11 rounded-md border border-red-300 bg-white px-4 text-sm font-medium text-red-700 hover:bg-red-50"
            :disabled="saving"
            data-testid="delete-group"
            @click="removeGroup"
          >
            {{ t('common.delete') }}
          </button>
          <span v-else />
          <div class="flex gap-2">
            <button
              type="button"
              class="min-h-11 rounded-md border border-slate-300 bg-white px-4 text-sm font-medium text-slate-700 hover:bg-slate-50"
              :disabled="saving"
              data-testid="cancel-editor"
              @click="closeEditor"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              class="min-h-11 rounded-md bg-slate-900 px-4 text-sm font-medium text-white hover:bg-slate-800 disabled:opacity-50"
              :disabled="saving"
              data-testid="save-group"
            >
              {{ t('common.save') }}
            </button>
          </div>
        </div>
      </form>
    </Modal>
  </div>
</template>
