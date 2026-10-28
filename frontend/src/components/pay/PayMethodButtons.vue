<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CreditCard, Loader2 } from 'lucide-vue-next'

// The payer's online payment choice, shared by every public pay surface. The
// server lists the configured providers in display order; this renders one
// button per method it knows. Text-only: no provider logos.
const props = withDefaults(
  defineProps<{
    providers: string[]
    // Which method leads (first and filled); unset keeps the server's order.
    emphasis?: 'vipps' | 'card'
    busy?: boolean
    disabled?: boolean
  }>(),
  { emphasis: undefined, busy: false, disabled: false },
)

const emit = defineEmits<{ pay: [provider: string] }>()

const { t } = useI18n()

type Method = 'vipps' | 'card'

// Provider name → the method the payer sees. Card payments run on Stripe.
const METHOD_BY_PROVIDER: Record<string, Method> = { vipps: 'vipps', stripe: 'card' }

const methods = computed(() => {
  const list = props.providers
    .filter((p) => METHOD_BY_PROVIDER[p])
    .map((provider) => ({ provider, method: METHOD_BY_PROVIDER[provider] }))
  if (props.emphasis) {
    const lead = list.filter((m) => m.method === props.emphasis)
    return [...lead, ...list.filter((m) => m.method !== props.emphasis)]
  }
  return list
})

// The button the payer pressed shows the spinner while the payment starts.
const pressed = ref<string | null>(null)
watch(
  () => props.busy,
  (busy) => {
    if (!busy) pressed.value = null
  },
)

function label(method: Method): string {
  return t(method === 'vipps' ? 'pay.methods.vipps' : 'pay.methods.card')
}

function buttonClass(method: Method, index: number): string {
  if (method === 'vipps') return 'bg-[#ff5b24] text-white hover:bg-[#e94e1b]'
  return index === 0
    ? 'bg-slate-800 text-white hover:bg-slate-900'
    : 'border border-slate-300 bg-white text-slate-900 hover:bg-slate-50'
}

// While a payment starts, every button is disabled, but only the others fade:
// the pressed one stays solid with its spinner, so the payer sees which
// method is starting.
function isPressed(provider: string): boolean {
  return props.busy && pressed.value === provider
}

function choose(provider: string) {
  if (props.busy || props.disabled) return
  pressed.value = provider
  emit('pay', provider)
}
</script>

<template>
  <div v-if="methods.length" class="space-y-3" data-testid="pay-methods">
    <p v-if="methods.length > 1" class="text-sm font-medium text-slate-700">{{ t('pay.methods.chooseTitle') }}</p>
    <button
      v-for="(m, i) in methods"
      :key="m.provider"
      type="button"
      class="flex min-h-[44px] w-full items-center justify-center gap-2 rounded-md px-6 py-3 text-xl font-bold shadow-sm"
      :class="[buttonClass(m.method, i), isPressed(m.provider) ? '' : 'disabled:opacity-50']"
      :data-provider="m.provider"
      :aria-busy="isPressed(m.provider) ? 'true' : 'false'"
      :disabled="busy || disabled"
      @click="choose(m.provider)"
    >
      <Loader2 v-if="isPressed(m.provider)" class="h-5 w-5 animate-spin" aria-hidden="true" />
      <CreditCard v-else-if="m.method === 'card'" class="h-5 w-5" aria-hidden="true" />
      {{ isPressed(m.provider) ? t('pay.processing') : label(m.method) }}
    </button>
  </div>
</template>
