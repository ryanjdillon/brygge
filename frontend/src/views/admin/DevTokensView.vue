<script setup lang="ts">
import { ref, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { KeyRound, AlertTriangle, Copy, Check, Trash2 } from "lucide-vue-next";
import { useFreshTotp } from "@/composables/useFreshTotp";
import { useConfirm } from "@/stores/confirm";

interface DevToken {
  id: string;
  name: string;
  prefix: string;
  scope: string;
  created_at: string;
  expires_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

const { t, locale } = useI18n();
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp();
const confirm = useConfirm();

const enabled = ref(true);
const maxTtlHours = ref(168);
const tokens = ref<DevToken[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

const name = ref("");
const ttlHours = ref(24);
const creating = ref(false);
const created = ref<{ token: string; name: string } | null>(null);
const copied = ref(false);

const ttlOptions = [1, 8, 24, 72, 168];
const endpoint = `${window.location.origin}/api/v1/dev/query`;

async function readError(res: Response): Promise<string> {
  const body = await res.json().catch(() => ({ error: res.statusText }));
  return body.error ?? res.statusText;
}

async function load() {
  loading.value = true;
  error.value = null;
  try {
    const res = await fetch("/api/v1/admin/dev/tokens", {
      credentials: "include",
    });
    if (!res.ok) throw new Error(await readError(res));
    const body = await res.json();
    enabled.value = body.enabled;
    maxTtlHours.value = body.max_ttl_hours;
    tokens.value = body.tokens;
  } catch (e) {
    error.value =
      e instanceof Error ? e.message : t("admin.devTokens.loadFailed");
  } finally {
    loading.value = false;
  }
}

async function create() {
  if (!name.value.trim()) return;
  if (!(await ensureFreshTotp())) return;
  creating.value = true;
  error.value = null;
  try {
    const res = await totpAwareFetch("/api/v1/admin/dev/tokens", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: name.value.trim(),
        ttl_hours: ttlHours.value,
      }),
    });
    if (!res.ok) throw new Error(await readError(res));
    const body = await res.json();
    created.value = { token: body.token, name: body.name };
    name.value = "";
    await load();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed";
  } finally {
    creating.value = false;
  }
}

async function revoke(tok: DevToken) {
  if (!(await ensureFreshTotp())) return;
  const ok = await confirm({
    title: t("admin.devTokens.revokeConfirmTitle"),
    body: t("admin.devTokens.revokeConfirmBody", { name: tok.name }),
    confirmLabel: t("admin.devTokens.revoke"),
    tone: "danger",
  });
  if (!ok) return;
  error.value = null;
  const res = await totpAwareFetch(`/api/v1/admin/dev/tokens/${tok.id}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    error.value = await readError(res);
    return;
  }
  await load();
}

async function copyToken() {
  if (!created.value) return;
  await navigator.clipboard.writeText(created.value.token);
  copied.value = true;
  setTimeout(() => (copied.value = false), 2000);
}

function status(tok: DevToken): "revoked" | "expired" | "active" {
  if (tok.revoked_at) return "revoked";
  if (new Date(tok.expires_at) <= new Date()) return "expired";
  return "active";
}

function fmt(ts: string | null): string {
  return ts
    ? new Date(ts).toLocaleString(locale.value)
    : t("admin.devTokens.never");
}

onMounted(load);
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">
      {{ t("admin.devTokens.title") }}
    </h1>
    <p class="mt-1 text-sm text-gray-600">
      {{ t("admin.devTokens.subtitle") }}
    </p>

    <div
      v-if="!loading && !enabled"
      class="mt-4 flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900"
    >
      <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
      <p>{{ t("admin.devTokens.disabled") }}</p>
    </div>

    <form
      v-if="enabled"
      class="mt-5 flex flex-wrap items-end gap-3"
      @submit.prevent="create"
    >
      <label class="flex-1 min-w-[12rem]">
        <span class="block text-sm font-medium text-gray-700">{{
          t("admin.devTokens.name")
        }}</span>
        <input
          v-model="name"
          maxlength="100"
          class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          :placeholder="t('admin.devTokens.namePlaceholder')"
        />
      </label>
      <label>
        <span class="block text-sm font-medium text-gray-700">{{
          t("admin.devTokens.ttl")
        }}</span>
        <select
          v-model.number="ttlHours"
          class="mt-1 rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
        >
          <option
            v-for="h in ttlOptions.filter((h) => h <= maxTtlHours)"
            :key="h"
            :value="h"
          >
            {{ t("admin.devTokens.ttlHours", { n: h }) }}
          </option>
        </select>
      </label>
      <button
        type="submit"
        :disabled="creating || !name.trim()"
        class="inline-flex items-center gap-1 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
      >
        <KeyRound class="h-4 w-4" />
        {{ creating ? t("common.loading") : t("admin.devTokens.create") }}
      </button>
    </form>

    <div
      v-if="created"
      class="mt-5 rounded-md border border-green-200 bg-green-50 p-4"
    >
      <p class="text-sm font-semibold text-green-900">
        {{ t("admin.devTokens.newTokenTitle", { name: created.name }) }}
      </p>
      <p class="mt-1 text-sm text-green-800">
        {{ t("admin.devTokens.newTokenWarning") }}
      </p>
      <div class="mt-2 flex items-center gap-2">
        <code
          class="flex-1 overflow-x-auto rounded bg-white px-2 py-1 font-mono text-xs text-gray-900"
          >{{ created.token }}</code
        >
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded border border-gray-200 bg-white px-2 py-1 text-xs text-gray-700 hover:bg-gray-50"
          @click="copyToken"
        >
          <component :is="copied ? Check : Copy" class="h-3.5 w-3.5" />
          {{ copied ? t("admin.devTokens.copied") : t("admin.devTokens.copy") }}
        </button>
      </div>
      <p class="mt-2 text-xs text-green-800">
        {{ t("admin.devTokens.usageHint") }}
      </p>
      <code
        class="mt-1 block overflow-x-auto rounded bg-white px-2 py-1 font-mono text-xs text-gray-700"
        >curl -H "Authorization: Bearer $BRYGGE_DEV_TOKEN" -d '{"sql":"select
        1"}' {{ endpoint }}</code
      >
    </div>

    <div
      v-if="error"
      class="mt-5 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700"
    >
      {{ error }}
    </div>

    <div class="mt-6 overflow-x-auto">
      <p v-if="!loading && tokens.length === 0" class="text-sm text-gray-500">
        {{ t("admin.devTokens.empty") }}
      </p>
      <table
        v-else-if="tokens.length"
        class="min-w-full divide-y divide-gray-200 text-sm"
      >
        <thead>
          <tr
            class="text-left text-xs font-medium uppercase tracking-wide text-gray-500"
          >
            <th class="py-2 pr-4">{{ t("admin.devTokens.cols.name") }}</th>
            <th class="py-2 pr-4">{{ t("admin.devTokens.cols.prefix") }}</th>
            <th class="py-2 pr-4">{{ t("admin.devTokens.cols.status") }}</th>
            <th class="py-2 pr-4">{{ t("admin.devTokens.cols.expires") }}</th>
            <th class="py-2 pr-4">{{ t("admin.devTokens.cols.lastUsed") }}</th>
            <th class="py-2"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          <tr v-for="tok in tokens" :key="tok.id">
            <td class="py-2 pr-4 text-gray-900">{{ tok.name }}</td>
            <td class="py-2 pr-4 font-mono text-xs text-gray-600">
              {{ tok.prefix }}…
            </td>
            <td class="py-2 pr-4">
              <span
                class="rounded px-1.5 py-0.5 text-xs"
                :class="{
                  'bg-green-100 text-green-800': status(tok) === 'active',
                  'bg-gray-100 text-gray-600': status(tok) === 'expired',
                  'bg-red-100 text-red-700': status(tok) === 'revoked',
                }"
              >
                {{ t(`admin.devTokens.status.${status(tok)}`) }}
              </span>
            </td>
            <td class="py-2 pr-4 text-gray-600">{{ fmt(tok.expires_at) }}</td>
            <td class="py-2 pr-4 text-gray-600">{{ fmt(tok.last_used_at) }}</td>
            <td class="py-2 text-right">
              <button
                v-if="status(tok) === 'active'"
                type="button"
                class="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-red-700 hover:bg-red-50"
                @click="revoke(tok)"
              >
                <Trash2 class="h-3.5 w-3.5" />
                {{ t("admin.devTokens.revoke") }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
