<template>
  <section class="card" data-testid="claude-telemetry-settings">
    <header class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t("admin.settings.claudeTelemetry.title") }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.claudeTelemetry.description") }}
          </p>
        </div>
        <span class="rounded bg-primary-50 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">
          {{ t("admin.settings.claudeTelemetry.profile") }} 2.1.284
        </span>
      </div>
    </header>

    <div v-if="loading" class="flex items-center justify-center p-8 text-gray-500">
      {{ t("common.loading") }}
    </div>

    <div v-else class="space-y-6 p-6">
      <div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        <p class="font-medium">{{ t("admin.settings.claudeTelemetry.scopeTitle") }}</p>
        <p class="mt-1">{{ t("admin.settings.claudeTelemetry.scopeHint") }}</p>
        <p class="mt-2">{{ t("admin.settings.claudeTelemetry.riskWarning") }}</p>
      </div>

      <div class="grid gap-4 md:grid-cols-2">
        <label class="block">
          <span class="input-label">{{ t("admin.settings.claudeTelemetry.mode") }}</span>
          <select v-model="form.mode" class="input" data-testid="claude-telemetry-mode">
            <option value="off">{{ t("admin.settings.claudeTelemetry.modes.off") }}</option>
            <option value="record">{{ t("admin.settings.claudeTelemetry.modes.record") }}</option>
            <option value="forward">{{ t("admin.settings.claudeTelemetry.modes.forward") }}</option>
            <option value="both">{{ t("admin.settings.claudeTelemetry.modes.both") }}</option>
          </select>
        </label>
        <div class="flex items-center justify-between rounded-lg border border-gray-200 px-4 py-3 dark:border-dark-600">
          <div>
            <p class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t("admin.settings.claudeTelemetry.setupToken") }}
            </p>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t("admin.settings.claudeTelemetry.setupTokenHint") }}
            </p>
          </div>
          <Toggle v-model="form.setup_token_enabled" data-testid="claude-telemetry-setup-token" />
        </div>
      </div>

      <div v-if="recordsLocally" class="space-y-2">
        <label class="input-label" for="claude-telemetry-record-file">
          {{ t("admin.settings.claudeTelemetry.recordFile") }}
        </label>
        <input
          id="claude-telemetry-record-file"
          v-model.trim="form.record_file"
          class="input font-mono"
          data-testid="claude-telemetry-record-file"
        />
        <p class="input-hint">{{ t("admin.settings.claudeTelemetry.recordFileHint") }}</p>
      </div>

      <div class="grid gap-4 lg:grid-cols-2">
        <fieldset class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <div class="flex items-center justify-between">
            <legend class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t("admin.settings.claudeTelemetry.firstParty") }}
            </legend>
            <Toggle v-model="form.first_party.enabled" data-testid="claude-telemetry-first-party-enabled" />
          </div>
          <label class="block">
            <span class="input-label">{{ t("admin.settings.claudeTelemetry.targetUrl") }}</span>
            <input v-model.trim="form.first_party.url" class="input font-mono text-xs" data-testid="claude-telemetry-first-party-url" />
          </label>
          <p class="input-hint">{{ t("admin.settings.claudeTelemetry.firstPartyHint") }}</p>
        </fieldset>

        <fieldset class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <div class="flex items-center justify-between">
            <legend class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t("admin.settings.claudeTelemetry.datadog") }}
            </legend>
            <Toggle v-model="form.datadog.enabled" data-testid="claude-telemetry-datadog-enabled" />
          </div>
          <label class="block">
            <span class="input-label">{{ t("admin.settings.claudeTelemetry.targetUrl") }}</span>
            <input v-model.trim="form.datadog.url" class="input font-mono text-xs" />
          </label>
          <label class="block">
            <span class="input-label">
              {{ t("admin.settings.claudeTelemetry.apiKey") }}
              <span v-if="apiKeyConfigured" class="ml-1 text-emerald-600">
                {{ t("admin.settings.claudeTelemetry.configured") }}
              </span>
            </span>
            <input
              v-model="form.datadog.api_key"
              type="password"
              autocomplete="new-password"
              class="input font-mono"
              data-testid="claude-telemetry-datadog-key"
              :disabled="form.datadog.clear_api_key"
              :placeholder="apiKeyConfigured
                ? t('admin.settings.claudeTelemetry.keyKeepPlaceholder')
                : t('admin.settings.claudeTelemetry.keyPlaceholder')"
            />
          </label>
          <label v-if="apiKeyConfigured" class="flex items-center gap-2 text-sm text-red-600 dark:text-red-400">
            <input v-model="form.datadog.clear_api_key" type="checkbox" data-testid="claude-telemetry-clear-key" />
            {{ t("admin.settings.claudeTelemetry.clearKey") }}
          </label>
        </fieldset>
      </div>

      <fieldset class="space-y-4 border-t border-gray-100 pt-5 dark:border-dark-700">
        <legend class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t("admin.settings.claudeTelemetry.delivery") }}
        </legend>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <NumberField v-model="form.batch.delay_ms" :label="t('admin.settings.claudeTelemetry.batchDelay')" :min="100" :max="3600000" />
          <NumberField v-model="form.batch.size" :label="t('admin.settings.claudeTelemetry.batchSize')" :min="1" :max="1000" />
          <NumberField v-model="form.batch.queue_size" :label="t('admin.settings.claudeTelemetry.queueSize')" :min="1" :max="100000" />
          <NumberField v-model="form.timeout_ms" :label="t('admin.settings.claudeTelemetry.timeout')" :min="100" :max="120000" />
          <NumberField v-model="form.retry.max_attempts" :label="t('admin.settings.claudeTelemetry.maxAttempts')" :min="1" :max="10" />
          <NumberField v-model="form.retry.base_delay_ms" :label="t('admin.settings.claudeTelemetry.retryBase')" :min="1" :max="60000" />
          <NumberField v-model="form.retry.max_delay_ms" :label="t('admin.settings.claudeTelemetry.retryMax')" :min="1" :max="300000" />
          <NumberField v-model="form.sampling.default_rate" :label="t('admin.settings.claudeTelemetry.defaultSampling')" :min="0" :max="1" :step="0.01" />
        </div>
        <label class="block">
          <span class="input-label">{{ t("admin.settings.claudeTelemetry.eventSampling") }}</span>
          <textarea v-model="eventRatesText" rows="4" class="input font-mono text-xs" data-testid="claude-telemetry-event-rates" />
          <span class="input-hint">{{ t("admin.settings.claudeTelemetry.eventSamplingHint") }}</span>
        </label>
        <div class="flex items-center justify-between">
          <div>
            <p class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t("admin.settings.claudeTelemetry.flushOnExit") }}
            </p>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t("admin.settings.claudeTelemetry.flushOnExitHint") }}
            </p>
          </div>
          <Toggle v-model="form.flush_on_exit" />
        </div>
      </fieldset>

      <section class="space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700" data-testid="claude-telemetry-runtime">
        <div class="flex items-center justify-between">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t("admin.settings.claudeTelemetry.runtime") }}
          </h3>
          <span :class="runtime?.available ? 'text-emerald-600' : 'text-gray-500'" class="text-xs font-medium">
            {{ runtime?.available
              ? t("admin.settings.claudeTelemetry.runtimeAvailable")
              : t("admin.settings.claudeTelemetry.runtimePending") }}
          </span>
        </div>
        <dl class="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
          <div><dt class="text-gray-500">{{ t("admin.settings.claudeTelemetry.queue") }}</dt><dd>{{ runtime?.queue_length ?? 0 }} / {{ runtime?.queue_capacity ?? form.batch.queue_size }}</dd></div>
          <div><dt class="text-gray-500">{{ t("admin.settings.claudeTelemetry.dropped") }}</dt><dd>{{ runtime?.dropped_events ?? 0 }}</dd></div>
          <div><dt class="text-gray-500">{{ t("admin.settings.claudeTelemetry.lastFlush") }}</dt><dd>{{ formatTime(runtime?.last_flush_at) }}</dd></div>
          <div><dt class="text-gray-500">{{ t("admin.settings.claudeTelemetry.deliveryCounts") }}</dt><dd>FP {{ deliverySummary(runtime?.first_party) }} · DD {{ deliverySummary(runtime?.datadog) }}</dd></div>
        </dl>
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.claudeTelemetry.provenanceHint") }}
        </p>
      </section>

      <div class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700">
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="saving || !dirty"
          data-testid="claude-telemetry-save"
          @click="save"
        >
          {{ saving ? t("common.saving") : t("common.save") }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import Toggle from "@/components/common/Toggle.vue";
import { useAppStore } from "@/stores";
import { useAdminSettingsStore } from "@/stores/adminSettings";
import { extractApiErrorMessage } from "@/utils/apiError";
import type {
  ClaudeTelemetryDeliveryStatus,
  ClaudeTelemetryRuntimeStatus,
  ClaudeTelemetrySettings,
  UpdateClaudeTelemetrySettingsRequest,
} from "@/api/admin/settings";

const NumberField = defineComponent({
  props: {
    modelValue: { type: Number, required: true },
    label: { type: String, required: true },
    min: { type: Number, required: true },
    max: { type: Number, required: true },
    step: { type: Number, default: 1 },
  },
  emits: ["update:modelValue"],
  setup(props, { emit }) {
    return () => h("label", { class: "block" }, [
      h("span", { class: "input-label" }, props.label),
      h("input", {
        class: "input",
        type: "number",
        value: props.modelValue,
        min: props.min,
        max: props.max,
        step: props.step,
        onInput: (event: Event) => emit(
          "update:modelValue",
          Number((event.target as HTMLInputElement).value),
        ),
      }),
    ]);
  },
});

const { t } = useI18n();
const appStore = useAppStore();
const settingsStore = useAdminSettingsStore();
const eventRatesText = ref("{}");
const baseline = ref("");
const runtime = ref<ClaudeTelemetryRuntimeStatus | null>(null);
const apiKeyConfigured = ref(false);
let refreshTimer: number | null = null;

const defaultForm = (): UpdateClaudeTelemetrySettingsRequest => ({
  mode: "record",
  setup_token_enabled: false,
  record_file: "./data/telemetry/telemetry-batches.ndjson",
  first_party: {
    enabled: true,
    url: "https://api.anthropic.com/api/event_logging/v2/batch",
  },
  datadog: {
    enabled: false,
    url: "https://http-intake.logs.us5.datadoghq.com/api/v2/logs",
    api_key: "",
    clear_api_key: false,
  },
  batch: { delay_ms: 10_000, size: 400, queue_size: 8192 },
  timeout_ms: 15_000,
  retry: { max_attempts: 3, base_delay_ms: 500, max_delay_ms: 10_000 },
  sampling: { default_rate: 1, event_rates: {} },
  flush_on_exit: false,
});

const form = reactive<UpdateClaudeTelemetrySettingsRequest>(defaultForm());
const loading = computed(() => settingsStore.claudeTelemetryLoading);
const saving = computed(() => settingsStore.claudeTelemetrySaving);
const recordsLocally = computed(() => form.mode === "record" || form.mode === "both");

function snapshot(): string {
  return JSON.stringify({ form, eventRatesText: eventRatesText.value });
}

const dirty = computed(() => snapshot() !== baseline.value);

function applySettings(settings: ClaudeTelemetrySettings): void {
  const { runtime: nextRuntime, datadog, profile: _profile, ...config } = settings;
  const next = JSON.parse(JSON.stringify({
    ...config,
    datadog: {
      enabled: datadog.enabled,
      url: datadog.url,
      api_key: "",
      clear_api_key: false,
    },
  })) as UpdateClaudeTelemetrySettingsRequest;
  Object.assign(form, next);
  runtime.value = nextRuntime;
  apiKeyConfigured.value = Boolean(settings.datadog.api_key_configured);
  eventRatesText.value = JSON.stringify(settings.sampling.event_rates ?? {}, null, 2);
  baseline.value = snapshot();
}

function parseEventRates(): Record<string, number> | null {
  try {
    const value = JSON.parse(eventRatesText.value || "{}") as unknown;
    if (!value || Array.isArray(value) || typeof value !== "object") return null;
    const rates: Record<string, number> = {};
    for (const [event, rate] of Object.entries(value as Record<string, unknown>)) {
      if (!event.trim() || typeof rate !== "number" || !Number.isFinite(rate) || rate < 0 || rate > 1) {
        return null;
      }
      rates[event] = rate;
    }
    return rates;
  } catch {
    return null;
  }
}

function validate(): Record<string, number> | null {
  const rates = parseEventRates();
  if (!rates) {
    appStore.showError(t("admin.settings.claudeTelemetry.invalidSampling"));
    return null;
  }
  if ((form.mode === "record" || form.mode === "both") && !form.record_file.trim()) {
    appStore.showError(t("admin.settings.claudeTelemetry.recordFileRequired"));
    return null;
  }
  if ((form.mode === "forward" || form.mode === "both") && !form.first_party.enabled && !form.datadog.enabled) {
    appStore.showError(t("admin.settings.claudeTelemetry.forwardTargetRequired"));
    return null;
  }
  const integerInRange = (value: number, min: number, max: number) =>
    Number.isInteger(value) && value >= min && value <= max;
  if (
    !integerInRange(form.batch.delay_ms, 100, 3_600_000) ||
    !integerInRange(form.batch.size, 1, 1000) ||
    !integerInRange(form.batch.queue_size, form.batch.size, 100_000) ||
    !integerInRange(form.timeout_ms, 100, 120_000) ||
    !integerInRange(form.retry.max_attempts, 1, 10) ||
    !integerInRange(form.retry.base_delay_ms, 1, 60_000) ||
    !integerInRange(form.retry.max_delay_ms, form.retry.base_delay_ms, 300_000) ||
    !Number.isFinite(form.sampling.default_rate) ||
    form.sampling.default_rate < 0 ||
    form.sampling.default_rate > 1
  ) {
    appStore.showError(t("admin.settings.claudeTelemetry.invalidDelivery"));
    return null;
  }
  if (
    form.datadog.enabled &&
    (!apiKeyConfigured.value || form.datadog.clear_api_key) &&
    !form.datadog.api_key?.trim()
  ) {
    appStore.showError(t("admin.settings.claudeTelemetry.apiKeyRequired"));
    return null;
  }
  return rates;
}

async function save(): Promise<void> {
  const rates = validate();
  if (!rates) return;
  const payload = JSON.parse(JSON.stringify(form)) as UpdateClaudeTelemetrySettingsRequest;
  payload.sampling.event_rates = rates;
  if (payload.datadog.clear_api_key) {
    payload.datadog.api_key = "";
  }
  try {
    applySettings(await settingsStore.saveClaudeTelemetry(payload));
    appStore.showSuccess(t("admin.settings.claudeTelemetry.saved"));
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.claudeTelemetry.saveFailed")));
  }
}

function formatTime(value?: string | null): string {
  if (!value) return "—";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

function deliverySummary(status?: ClaudeTelemetryDeliveryStatus): string {
  return `${status?.successes ?? 0}/${status?.failures ?? 0}`;
}

watch(
  () => settingsStore.claudeTelemetry,
  (settings) => {
    if (!settings) return;
    if (dirty.value) {
      runtime.value = settings.runtime;
      return;
    }
    applySettings(settings);
  },
  { immediate: true },
);

watch(
  () => form.datadog.clear_api_key,
  (clear) => {
    if (clear) form.datadog.api_key = "";
  },
);

watch(
  () => form.datadog.api_key,
  (key) => {
    if (key?.trim()) form.datadog.clear_api_key = false;
  },
);

onMounted(async () => {
  try {
    applySettings(await settingsStore.loadClaudeTelemetry(true));
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.claudeTelemetry.loadFailed")));
    baseline.value = snapshot();
  }
  refreshTimer = window.setInterval(() => {
    void settingsStore.loadClaudeTelemetry(true, true).catch(() => {
      // Keep the last known status; foreground loads and saves surface errors.
    });
  }, 5000);
});

onBeforeUnmount(() => {
  if (refreshTimer !== null) window.clearInterval(refreshTimer);
});
</script>
