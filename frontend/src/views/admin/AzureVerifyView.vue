<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-6 pb-8">
      <div class="flex items-center gap-2">
        <Icon name="shield" size="md" class="text-gray-500 dark:text-gray-400" />
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('admin.azureVerify.title') }}
        </h2>
      </div>

      <div class="card p-4 sm:p-6">
        <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.azureVerify.description') }}
        </p>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label" for="azure-verify-base-url">
              {{ t('admin.azureVerify.connection.baseUrl') }}
            </label>
            <input
              id="azure-verify-base-url"
              v-model="conn.baseUrl"
              spellcheck="false"
              class="input font-mono text-xs"
              placeholder="https://api.example.com"
              :disabled="running"
            />
          </div>
          <div>
            <label class="input-label" for="azure-verify-api-key">
              {{ t('admin.azureVerify.connection.apiKey') }}
            </label>
            <input
              id="azure-verify-api-key"
              v-model="conn.apiKey"
              type="password"
              autocomplete="off"
              spellcheck="false"
              class="input font-mono text-xs"
              placeholder="sk-..."
              :disabled="running"
            />
          </div>
        </div>
        <div class="mt-4 flex flex-wrap items-center gap-3">
          <button
            type="button"
            class="btn btn-primary btn-sm"
            :disabled="!canConnect || loadingModels || running"
            @click="fetchModels"
          >
            <Icon :name="loadingModels ? 'refresh' : 'search'" size="xs" :class="loadingModels ? 'animate-spin' : ''" />
            {{ t('admin.azureVerify.connection.fetchModels') }}
          </button>
          <span v-if="modelsLoadedFor" class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.azureVerify.connection.modelsCount', { n: models.length }) }}
          </span>
        </div>
        <p v-if="modelsError" class="mt-3 break-all text-sm text-red-600 dark:text-red-400">
          {{ modelsError }}
        </p>
      </div>

      <template v-if="modelsLoadedFor">
        <div class="card p-4 sm:p-6">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.azureVerify.reference.title') }}
          </h3>
          <p class="mb-3 mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.azureVerify.reference.hint') }}
          </p>
          <div class="flex flex-wrap gap-2">
            <span
              v-for="item in referenceStatus"
              :key="item.model"
              class="inline-flex items-center gap-1 rounded-md px-2 py-1 font-mono text-xs ring-1 ring-inset"
              :class="referenceClass(item.state)"
            >
              {{ item.model }}
              <span class="font-sans">· {{ t(`admin.azureVerify.reference.states.${item.state}`) }}</span>
            </span>
          </div>
        </div>

        <div class="card p-4 sm:p-6">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.azureVerify.models.title') }}
            </h3>
            <div class="flex flex-wrap items-center gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="running" @click="selectDefault">
                {{ t('admin.azureVerify.models.selectGpt6') }}
              </button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="running" @click="selected = [...models]">
                {{ t('admin.azureVerify.models.selectAll') }}
              </button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="running" @click="selected = []">
                {{ t('admin.azureVerify.models.clear') }}
              </button>
            </div>
          </div>
          <div class="grid max-h-72 grid-cols-1 gap-x-4 gap-y-1 overflow-y-auto sm:grid-cols-2 lg:grid-cols-3">
            <label
              v-for="m in models"
              :key="m"
              class="flex cursor-pointer items-center gap-2 rounded px-1 py-0.5 font-mono text-xs text-gray-800 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-dark-800"
            >
              <input v-model="selected" type="checkbox" :value="m" :disabled="running" />
              {{ m }}
            </label>
          </div>
          <div class="mt-4 flex flex-wrap items-center gap-3">
            <button
              v-if="!running"
              type="button"
              class="btn btn-primary btn-sm"
              :disabled="selected.length === 0"
              @click="startProbe"
            >
              <Icon name="play" size="xs" />
              {{ t('admin.azureVerify.models.start', { n: selected.length }) }}
            </button>
            <button v-else type="button" class="btn btn-secondary btn-sm" :disabled="stopRequested" @click="stopRequested = true">
              <Icon name="x" size="xs" />
              {{ t('admin.azureVerify.models.stop') }}
            </button>
            <span class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.azureVerify.models.hint') }}
            </span>
          </div>
        </div>
      </template>

      <div v-if="rows.length" class="card p-4 sm:p-6">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">
          {{ t('admin.azureVerify.results.title') }}
        </h3>
        <p class="mb-4 mt-1 text-sm text-gray-700 dark:text-gray-300">
          {{ t('admin.azureVerify.results.summary', summary) }}
        </p>

        <div class="divide-y divide-gray-100 dark:divide-dark-700">
          <div v-for="row in rows" :key="row.model" class="py-3">
            <div class="flex flex-wrap items-center gap-3">
              <span class="font-mono text-sm text-gray-900 dark:text-white">{{ row.model }}</span>
              <span class="rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset" :class="verdictClass(row)">
                <Icon v-if="row.state === 'running'" name="refresh" size="xs" class="mr-1 inline animate-spin" />
                {{ verdictLabel(row) }}
              </span>
              <span v-if="row.result?.served_model" class="font-mono text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.azureVerify.results.servedModel', { model: row.result.served_model }) }}
              </span>
            </div>
            <p v-if="row.result?.suspicious" class="mt-2 text-xs text-amber-700 dark:text-amber-300">
              {{ t('admin.azureVerify.results.suspicious') }}
            </p>
            <p v-if="row.requestError || row.result?.error" class="mt-2 break-all text-xs text-red-600 dark:text-red-400">
              {{ row.requestError || row.result?.error }}
            </p>

            <details v-if="row.result" class="mt-2">
              <summary class="cursor-pointer select-none text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.azureVerify.results.details') }}
              </summary>
              <div class="mt-3 space-y-4">
                <div v-if="row.result.evidences?.length">
                  <div class="mb-1 text-xs font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.azureVerify.results.evidences') }}
                  </div>
                  <ul class="space-y-1">
                    <li v-for="(ev, i) in row.result.evidences" :key="i" class="flex items-start gap-2 text-xs">
                      <span class="mt-0.5 shrink-0 rounded px-1.5 py-0.5 font-medium" :class="evidenceClass(ev.kind)">
                        {{ t(`admin.azureVerify.results.evidenceKinds.${ev.kind}`) }}
                      </span>
                      <span class="min-w-0 text-gray-800 dark:text-gray-200">
                        {{ ev.detail }}
                        <span v-if="ev.value" class="ml-1 break-all font-mono text-gray-500 dark:text-gray-400">{{ ev.value }}</span>
                      </span>
                    </li>
                  </ul>
                </div>

                <div v-if="row.result.steps?.length">
                  <div class="mb-1 text-xs font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.azureVerify.results.steps') }}
                  </div>
                  <div class="space-y-2">
                    <div
                      v-for="step in row.result.steps"
                      :key="step.name"
                      class="rounded-md bg-gray-50 p-3 text-xs dark:bg-dark-800"
                    >
                      <div class="flex flex-wrap items-center gap-2">
                        <Icon :name="step.ok ? 'checkCircle' : 'xCircle'" size="sm" :class="step.ok ? 'text-green-600' : 'text-red-500'" />
                        <span class="font-medium text-gray-900 dark:text-white">
                          {{ t(`admin.azureVerify.results.stepNames.${step.name}`) }}
                        </span>
                        <span class="text-gray-500 dark:text-gray-400">HTTP {{ step.status || '-' }} · {{ step.duration_ms }} ms</span>
                      </div>
                      <p v-if="step.error" class="mt-1 break-all text-red-600 dark:text-red-400">{{ step.error }}</p>
                      <pre
                        v-if="step.headers && Object.keys(step.headers).length"
                        class="mt-2 overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] text-gray-700 dark:text-gray-300"
                      >{{ formatHeaders(step.headers) }}</pre>
                    </div>
                  </div>
                </div>
              </div>
            </details>
          </div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  adminAzureVerifyAPI,
  type AzureVerifyConnection,
  type AzureVerifyEvidenceKind,
  type AzureVerifyProbeResponse
} from '@/api/admin/azureVerify'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

// Newer models a genuine Azure reseller is expected to carry; missing ones are flagged.
const REFERENCE_MODELS = [
  'gpt-6.1-terra',
  'gpt-6.1-sol',
  'gpt-6-sol',
  'gpt-6-astra',
  'gpt-6-luna',
  'gpt-6',
  'gpt-5.6-sol',
  'gpt-5.6-terra',
  'gpt-5.6-luna'
]

type RowState = 'pending' | 'running' | 'done' | 'error'
type ReferenceState = 'missing' | 'unavailable' | 'listed'

interface ProbeRow {
  model: string
  state: RowState
  result?: AzureVerifyProbeResponse
  requestError?: string
}

const conn = ref({ baseUrl: '', apiKey: '' })
const models = ref<string[]>([])
const selected = ref<string[]>([])
const modelsLoadedFor = ref('')
const modelsError = ref('')
const loadingModels = ref(false)
const rows = ref<ProbeRow[]>([])
const running = ref(false)
const stopRequested = ref(false)

const canConnect = computed(() => !!conn.value.baseUrl.trim() && !!conn.value.apiKey.trim())

function connection(): AzureVerifyConnection {
  return { base_url: conn.value.baseUrl.trim(), api_key: conn.value.apiKey.trim() }
}

function selectDefault(): void {
  selected.value = models.value.filter((m) => m.startsWith('gpt-6'))
}

async function fetchModels(): Promise<void> {
  loadingModels.value = true
  modelsError.value = ''
  try {
    const res = await adminAzureVerifyAPI.listModels(connection())
    if (res.error && res.models.length === 0) {
      modelsError.value = t('admin.azureVerify.connection.modelsFailed', {
        status: res.status || '-',
        error: res.error
      })
      return
    }
    models.value = res.models
    modelsLoadedFor.value = conn.value.baseUrl.trim()
    rows.value = []
    selectDefault()
  } catch (err) {
    modelsError.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loadingModels.value = false
  }
}

async function startProbe(): Promise<void> {
  const target = connection()
  // Probe in the listing order so results line up with the checkbox grid.
  const queue = models.value.filter((m) => selected.value.includes(m))
  rows.value = queue.map((model) => ({ model, state: 'pending' }))
  running.value = true
  stopRequested.value = false
  try {
    // Sequential on purpose: parallel probes quickly trip Azure's per-deployment rate limits.
    for (const row of rows.value) {
      if (stopRequested.value) break
      row.state = 'running'
      try {
        row.result = await adminAzureVerifyAPI.probe(target, row.model)
        row.state = row.result.status === 'ok' ? 'done' : 'error'
      } catch (err) {
        row.state = 'error'
        row.requestError = extractApiErrorMessage(err, t('common.error'))
      }
    }
  } finally {
    running.value = false
  }
}

const referenceStatus = computed<{ model: string; state: ReferenceState }[]>(() =>
  REFERENCE_MODELS.map((model) => {
    if (!models.value.includes(model)) return { model, state: 'missing' }
    const row = rows.value.find((r) => r.model === model)
    if (row?.result?.model_unavailable) return { model, state: 'unavailable' }
    return { model, state: 'listed' }
  })
)

const summary = computed(() => {
  const finished = rows.value.filter((r) => r.state === 'done' || r.state === 'error')
  return {
    total: rows.value.length,
    finished: finished.length,
    azure: finished.filter((r) => r.state === 'done' && r.result?.is_azure).length,
    notAzure: finished.filter((r) => r.state === 'done' && !r.result?.is_azure).length,
    failed: finished.filter((r) => r.state === 'error').length
  }
})

function verdictLabel(row: ProbeRow): string {
  if (row.state === 'pending') return t('admin.azureVerify.results.states.pending')
  if (row.state === 'running') return t('admin.azureVerify.results.states.running')
  if (row.state === 'error') {
    return row.result?.model_unavailable
      ? t('admin.azureVerify.results.states.unavailable')
      : t('admin.azureVerify.results.states.failed')
  }
  return row.result?.is_azure ? t('admin.azureVerify.results.states.azure') : t('admin.azureVerify.results.states.notAzure')
}

function verdictClass(row: ProbeRow): string {
  if (row.state === 'pending' || row.state === 'running') {
    return 'bg-gray-50 text-gray-600 ring-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:ring-dark-600'
  }
  if (row.state === 'error') {
    return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800'
  }
  return row.result?.is_azure
    ? 'bg-green-50 text-green-700 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800'
    : 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800'
}

function referenceClass(state: ReferenceState): string {
  switch (state) {
    case 'missing':
      return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800'
    case 'unavailable':
      return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800'
    default:
      return 'bg-green-50 text-green-700 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800'
  }
}

function evidenceClass(kind: AzureVerifyEvidenceKind): string {
  switch (kind) {
    case 'azure':
      return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
    case 'non_azure':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
}

function formatHeaders(headers: Record<string, string[]>): string {
  return Object.keys(headers)
    .sort()
    .flatMap((name) => headers[name].map((value) => `${name}: ${value}`))
    .join('\n')
}
</script>
