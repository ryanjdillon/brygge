<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  ShieldCheck,
  ShieldAlert,
  KeyRound,
  RefreshCw,
  Copy,
  Download,
  Check,
  Fingerprint,
  Plus,
  Pencil,
  Trash2,
} from 'lucide-vue-next'
import QRCode from 'qrcode'

import { useAuthStore } from '@/stores/auth'
import { useTotp, TotpError } from '@/composables/useTotp'
import { usePasskey, passkeysSupported, PasskeyError, type PasskeyInfo } from '@/composables/usePasskey'
import { useTotpGateStore } from '@/stores/totpGate'
import Checkbox from '@/components/ui/form/Checkbox.vue'
import Input from '@/components/ui/form/Input.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const totp = useTotp()
const passkey = usePasskey()
const gate = useTotpGateStore()

// Three-state setup flow:
//   idle      → user hasn't started enrolling
//   pending   → /setup returned a secret + QR; awaiting user code
//   confirmed → /confirm succeeded; recovery codes ready to display
type Stage = 'idle' | 'pending' | 'confirmed'
const stage = ref<Stage>('idle')

const secret = ref('')
const qrDataUrl = ref('')
const codeInput = ref('')
const recoveryCodes = ref<string[]>([])
const codesAcknowledged = ref(false)

const error = ref<string | null>(null)
const busy = ref(false)

const isEnrolled = computed(() => auth.user?.totpEnabled === true)

// --- Passkeys ---
const passkeysAvailable = passkeysSupported()
const passkeys = ref<PasskeyInfo[]>([])
const passkeyBusy = ref(false)
const passkeyError = ref<string | null>(null)
const editingId = ref<string | null>(null)
const editName = ref('')

async function loadPasskeys() {
  if (!passkeysAvailable) return
  try {
    passkeys.value = await passkey.list()
  } catch {
    // A failed list shouldn't break the page; leave the section empty.
  }
}

// isFreshRequired reports whether an error is the backend's "step up first"
// signal — enrolling or removing a passkey needs a recent 2FA verification.
function isFreshRequired(e: unknown): boolean {
  return e instanceof PasskeyError && e.message === 'totp_fresh_required'
}

function reportAddError(e: unknown) {
  if (e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError')) return
  if (e instanceof PasskeyError && e.status === 409) {
    passkeyError.value = t('security.passkey.alreadyRegistered')
  } else {
    passkeyError.value = t('security.passkey.addFailed')
  }
}

async function addPasskey() {
  passkeyError.value = null
  passkeyBusy.value = true
  try {
    await passkey.register()
    await loadPasskeys()
  } catch (e) {
    // Not fresh? Prompt for a 2FA step-up, then retry enrollment in one flow
    // rather than dead-ending on a message the user can't act on.
    if (isFreshRequired(e) && (await gate.open())) {
      try {
        await passkey.register()
        await loadPasskeys()
      } catch (retryErr) {
        reportAddError(retryErr)
      }
    } else if (!isFreshRequired(e)) {
      reportAddError(e)
    }
  } finally {
    passkeyBusy.value = false
  }
}

function startRename(pk: PasskeyInfo) {
  editingId.value = pk.id
  editName.value = pk.name
}

async function saveRename(id: string) {
  const name = editName.value.trim()
  if (!name) {
    editingId.value = null
    return
  }
  try {
    await passkey.rename(id, name)
    await loadPasskeys()
  } catch (e) {
    passkeyError.value = t('security.passkey.renameFailed')
  }
  editingId.value = null
}

async function removePasskey(id: string) {
  passkeyError.value = null
  try {
    await passkey.remove(id)
    await loadPasskeys()
  } catch (e) {
    if (isFreshRequired(e) && (await gate.open())) {
      try {
        await passkey.remove(id)
        await loadPasskeys()
      } catch {
        passkeyError.value = t('security.passkey.removeFailed')
      }
    } else if (!isFreshRequired(e)) {
      passkeyError.value = t('security.passkey.removeFailed')
    }
  }
}

function passkeyLabel(pk: PasskeyInfo): string {
  return pk.name || t('security.passkey.unnamed')
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString()
}

onMounted(async () => {
  // The auth store fetches /me eagerly at boot, but make sure it's
  // settled before we render — a stale enrolled flag would flicker.
  await auth.ready
  await loadPasskeys()
})

async function beginEnrollment() {
  error.value = null
  busy.value = true
  try {
    const { secret: s, qr_url } = await totp.setup()
    secret.value = s
    qrDataUrl.value = await QRCode.toDataURL(qr_url, { margin: 1, width: 220 })
    stage.value = 'pending'
  } catch (e) {
    error.value = formatError(e, t('security.errors.setupFailed'))
  } finally {
    busy.value = false
  }
}

async function submitConfirm() {
  error.value = null
  if (!/^\d{6}$/.test(codeInput.value.trim())) {
    error.value = t('security.errors.codeFormat')
    return
  }
  busy.value = true
  try {
    const result = await totp.confirm(secret.value, codeInput.value.trim())
    recoveryCodes.value = result.recovery_codes
    stage.value = 'confirmed'
    codeInput.value = ''
  } catch (e) {
    error.value = formatError(e, t('security.errors.confirmFailed'))
  } finally {
    busy.value = false
  }
}

async function regenerateCodes() {
  error.value = null
  busy.value = true
  try {
    const result = await totp.regenerateCodes()
    recoveryCodes.value = result.recovery_codes
    codesAcknowledged.value = false
    stage.value = 'confirmed'
  } catch (e) {
    if (e instanceof TotpError && e.message === 'totp_fresh_required') {
      error.value = t('security.errors.freshRequired')
    } else {
      error.value = formatError(e, t('security.errors.regenerateFailed'))
    }
  } finally {
    busy.value = false
  }
}

function copyAllCodes() {
  navigator.clipboard.writeText(recoveryCodes.value.join('\n'))
}

function downloadCodes() {
  const blob = new Blob([recoveryCodes.value.join('\n')], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `brygge-recovery-codes-${new Date().toISOString().split('T')[0]}.txt`
  a.click()
  URL.revokeObjectURL(url)
}

function dismissCodes() {
  recoveryCodes.value = []
  codesAcknowledged.value = false
  stage.value = 'idle'
  // If the user arrived from a 403 redirect on a protected page,
  // honor the original destination. The freshly-confirmed enrollment
  // already stamped the session as TOTP-verified, so they walk
  // straight in.
  const next = route.query.next
  if (typeof next === 'string' && next.startsWith('/')) {
    router.replace(next)
  }
}

function handleTotpKeyup(e: KeyboardEvent) {
  if (e.key === 'Enter') submitConfirm()
}

function formatError(e: unknown, fallback: string): string {
  if (e instanceof TotpError && e.message) return e.message
  if (e instanceof Error && e.message) return e.message
  return fallback
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h2 class="text-xl font-semibold text-gray-900">{{ t('security.title') }}</h2>
      <p class="mt-1 text-sm text-gray-600">{{ t('security.subtitle') }}</p>
    </div>

    <!-- Status card -->
    <div class="rounded-lg border bg-white p-4" :class="isEnrolled ? 'border-green-200' : 'border-amber-200'">
      <div class="flex items-center gap-3">
        <ShieldCheck v-if="isEnrolled" class="h-6 w-6 text-green-600" />
        <ShieldAlert v-else class="h-6 w-6 text-amber-600" />
        <div>
          <p class="font-medium text-gray-900">
            {{ isEnrolled ? t('security.statusEnabled') : t('security.statusDisabled') }}
          </p>
          <p class="text-sm text-gray-500">
            {{ isEnrolled ? t('security.statusEnabledDescription') : t('security.statusDisabledDescription') }}
          </p>
        </div>
      </div>
    </div>

    <!-- Recovery codes display (after confirm or regenerate) -->
    <div v-if="recoveryCodes.length > 0" class="rounded-lg border border-amber-300 bg-amber-50 p-4">
      <div class="mb-3 flex items-start gap-2">
        <KeyRound class="mt-0.5 h-5 w-5 text-amber-600" />
        <div>
          <p class="font-medium text-amber-900">{{ t('security.recoveryCodesTitle') }}</p>
          <p class="text-sm text-amber-800">{{ t('security.recoveryCodesWarning') }}</p>
        </div>
      </div>
      <div class="grid grid-cols-2 gap-2 rounded border border-amber-200 bg-white p-3 font-mono text-sm">
        <span v-for="c in recoveryCodes" :key="c" class="select-all">{{ c }}</span>
      </div>
      <div class="mt-3 flex flex-wrap gap-2">
        <button class="inline-flex items-center gap-1 rounded-md bg-white px-3 py-1.5 text-sm font-medium text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50" @click="copyAllCodes">
          <Copy class="h-4 w-4" /> {{ t('security.copyAll') }}
        </button>
        <button class="inline-flex items-center gap-1 rounded-md bg-white px-3 py-1.5 text-sm font-medium text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50" @click="downloadCodes">
          <Download class="h-4 w-4" /> {{ t('security.downloadCodes') }}
        </button>
      </div>
      <div class="mt-4 text-amber-900">
        <Checkbox v-model="codesAcknowledged">{{ t('security.acknowledgeSaved') }}</Checkbox>
      </div>
      <button
        class="mt-3 inline-flex items-center gap-1 rounded-md bg-green-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-green-700 disabled:opacity-50"
        :disabled="!codesAcknowledged"
        @click="dismissCodes"
      >
        <Check class="h-4 w-4" /> {{ t('security.continueAfterSave') }}
      </button>
    </div>

    <!-- Enrollment flow -->
    <div v-if="!isEnrolled && stage === 'idle' && recoveryCodes.length === 0" class="rounded-lg border border-gray-200 bg-white p-4">
      <h3 class="font-medium text-gray-900">{{ t('security.enableTitle') }}</h3>
      <p class="mt-1 text-sm text-gray-600">{{ t('security.enableDescription') }}</p>
      <button
        class="mt-3 inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
        :disabled="busy"
        @click="beginEnrollment"
      >
        <ShieldCheck class="h-4 w-4" />
        {{ busy ? t('common.loading') : t('security.enableButton') }}
      </button>
    </div>

    <div v-if="stage === 'pending'" class="rounded-lg border border-gray-200 bg-white p-4">
      <h3 class="font-medium text-gray-900">{{ t('security.scanTitle') }}</h3>
      <p class="mt-1 text-sm text-gray-600">{{ t('security.scanDescription') }}</p>
      <div class="mt-4 flex flex-col items-center gap-3 sm:flex-row sm:items-start">
        <img v-if="qrDataUrl" :src="qrDataUrl" alt="TOTP QR" class="rounded border border-gray-200" />
        <div class="text-sm text-gray-600">
          <p class="mb-1 font-medium text-gray-700">{{ t('security.manualEntry') }}</p>
          <code class="block break-all rounded bg-gray-100 px-2 py-1 font-mono">{{ secret }}</code>
        </div>
      </div>
      <div class="mt-4">
        <label class="block text-sm font-medium text-gray-700" for="totp-code">
          {{ t('security.codeLabel') }}
        </label>
        <div class="mt-1 inline-block w-32">
          <Input
            id="totp-code"
            v-model="codeInput"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            :maxlength="6"
            text-align="center"
            input-class="font-mono tracking-widest text-lg"
            @keyup="handleTotpKeyup"
          />
        </div>
        <button
          class="ml-3 inline-flex items-center rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="busy"
          @click="submitConfirm"
        >
          {{ busy ? t('common.loading') : t('security.confirmButton') }}
        </button>
      </div>
    </div>

    <!-- Manage existing enrollment -->
    <div v-if="isEnrolled && recoveryCodes.length === 0" class="rounded-lg border border-gray-200 bg-white p-4">
      <h3 class="font-medium text-gray-900">{{ t('security.manageTitle') }}</h3>
      <div class="mt-3 flex flex-col gap-2 sm:flex-row">
        <button
          class="inline-flex items-center gap-2 rounded-md bg-white px-4 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50 disabled:opacity-50"
          :disabled="busy"
          @click="regenerateCodes"
        >
          <RefreshCw class="h-4 w-4" />
          {{ t('security.regenerateButton') }}
        </button>
      </div>
      <p class="mt-2 text-xs text-gray-500">{{ t('security.regenerateHelp') }}</p>
    </div>

    <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

    <!-- Passkeys -->
    <div v-if="passkeysAvailable" class="rounded-lg border border-gray-200 bg-white p-4">
      <div class="flex items-center gap-2">
        <Fingerprint class="h-5 w-5 text-brand-600" />
        <h3 class="font-medium text-gray-900">{{ t('security.passkey.title') }}</h3>
      </div>
      <p class="mt-1 text-sm text-gray-600">{{ t('security.passkey.description') }}</p>

      <ul v-if="passkeys.length" class="mt-3 divide-y divide-gray-100 rounded border border-gray-100">
        <li v-for="pk in passkeys" :key="pk.id" class="flex items-center justify-between gap-3 px-3 py-2">
          <div class="min-w-0 flex-1">
            <div v-if="editingId === pk.id" class="flex items-center gap-2">
              <Input
                v-model="editName"
                type="text"
                :maxlength="64"
                :aria-label="t('security.passkey.nameLabel')"
                @keyup.enter="saveRename(pk.id)"
              />
              <button class="text-sm font-medium text-brand-600 hover:text-brand-700" @click="saveRename(pk.id)">
                {{ t('security.passkey.save') }}
              </button>
            </div>
            <template v-else>
              <p class="truncate font-medium text-gray-900">{{ passkeyLabel(pk) }}</p>
              <p class="text-xs text-gray-500">{{ t('security.passkey.added') }} {{ formatDate(pk.created_at) }}</p>
            </template>
          </div>
          <div v-if="editingId !== pk.id" class="flex shrink-0 items-center gap-1">
            <button
              class="rounded p-1.5 text-gray-500 hover:bg-gray-50"
              :title="t('security.passkey.rename')"
              @click="startRename(pk)"
            >
              <Pencil class="h-4 w-4" />
            </button>
            <button
              class="rounded p-1.5 text-red-600 hover:bg-red-50"
              :title="t('security.passkey.remove')"
              @click="removePasskey(pk.id)"
            >
              <Trash2 class="h-4 w-4" />
            </button>
          </div>
        </li>
      </ul>
      <p v-else class="mt-3 text-sm text-gray-500">{{ t('security.passkey.none') }}</p>

      <button
        class="mt-3 inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
        :disabled="passkeyBusy"
        @click="addPasskey"
      >
        <Plus class="h-4 w-4" />
        {{ passkeyBusy ? t('common.loading') : t('security.passkey.addButton') }}
      </button>
      <p v-if="passkeyError" class="mt-2 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ passkeyError }}</p>
    </div>
  </div>
</template>
