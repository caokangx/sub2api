<template>
  <div class="card p-4 sm:p-6">
    <p class="text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.signatureDecoder.injection.description') }}
    </p>
    <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
      {{ t('admin.signatureDecoder.injection.scopeNote') }}
    </p>

    <div class="mt-4 flex flex-wrap items-center gap-3">
      <button type="button" class="btn btn-primary btn-sm" :disabled="running || !canConnect" @click="start">
        <Icon :name="running ? 'refresh' : 'play'" size="xs" :class="running ? 'animate-spin' : ''" />
        {{ t('admin.signatureDecoder.injection.start') }}
      </button>
      <span v-if="running" class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.signatureDecoder.injection.progress', { done, total: PADDING_STEPS.length }) }}
      </span>
    </div>

    <p v-if="error" class="mt-3 break-all text-sm text-red-600 dark:text-red-400">
      {{ error }}
    </p>

    <div v-if="report" class="mt-5 space-y-4">
      <div class="flex flex-wrap items-center gap-2">
        <span class="rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset" :class="verdictClass">
          {{ t(`admin.signatureDecoder.injection.verdict.${report.verdict}`) }}
        </span>
        <span
          v-if="report.inflated"
          class="rounded-md bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700 ring-1 ring-inset ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800"
        >
          {{ t('admin.signatureDecoder.injection.inflatedTag') }}
        </span>
        <span class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ report.model }}</span>
      </div>

      <dl class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div>
          <dt class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.signatureDecoder.injection.injectedLabel') }}
          </dt>
          <dd class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('admin.signatureDecoder.injection.injectedValue', { n: report.injectedTokens.toLocaleString() }) }}
          </dd>
          <dd class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.signatureDecoder.injection.injectedNote') }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.signatureDecoder.injection.inflationLabel') }}
          </dt>
          <dd class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
            {{ report.inflation === null ? '-' : `${report.inflation.toFixed(2)}x` }}
          </dd>
          <dd class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.signatureDecoder.injection.inflationNote') }}
          </dd>
        </div>
      </dl>

      <ul v-if="report.hints.length" class="space-y-1">
        <li
          v-for="hint in report.hints"
          :key="hint"
          class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
        >
          {{ t(`admin.signatureDecoder.injection.hints.${hint}`) }}
        </li>
      </ul>

      <div class="overflow-x-auto">
        <table class="w-full min-w-[640px] text-left text-sm">
          <thead class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
            <tr>
              <th class="py-2 pr-4 font-medium">{{ t('admin.signatureDecoder.injection.table.units') }}</th>
              <th class="py-2 pr-4 font-medium">{{ t('admin.signatureDecoder.injection.table.expected') }}</th>
              <th class="py-2 pr-4 font-medium">{{ t('admin.signatureDecoder.injection.table.reported') }}</th>
              <th class="py-2 pr-4 font-medium">input_tokens</th>
              <th class="py-2 pr-4 font-medium">cache_creation</th>
              <th class="py-2 font-medium">cache_read</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 font-mono text-xs dark:divide-dark-700">
            <tr v-for="row in report.rows" :key="row.units" class="text-gray-900 dark:text-white">
              <td class="py-2 pr-4">{{ row.units }}</td>
              <td class="py-2 pr-4">{{ row.expected ?? '-' }}</td>
              <td class="py-2 pr-4 font-semibold">{{ row.usage.total }}</td>
              <td class="py-2 pr-4">{{ row.usage.input }}</td>
              <td class="py-2 pr-4">{{ row.usage.cacheCreation }}</td>
              <td class="py-2">{{ row.usage.cacheRead }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { analyzeInjection, PADDING_STEPS, type InjectionReport } from '@/utils/claudeInjection'
import { ReasoningError, type ReasoningConn } from '@/utils/claudeReasoning'

const props = defineProps<{
  conn: ReasoningConn
}>()

const { t } = useI18n()

const running = ref(false)
const done = ref(0)
const error = ref('')
const report = ref<InjectionReport | null>(null)

const canConnect = computed(
  () => !!props.conn.baseUrl.trim() && !!props.conn.apiKey.trim() && !!props.conn.model.trim()
)

const verdictClass = computed(() => {
  switch (report.value?.verdict) {
    case 'none':
      return 'bg-green-50 text-green-700 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800'
    case 'minor':
      return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800'
    default:
      return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800'
  }
})

function describeError(err: unknown): string {
  if (err instanceof ReasoningError) {
    const [code, detail] = err.message.split(/:(.*)/s)
    if (['missingBaseUrl', 'missingApiKey', 'upstreamError'].includes(code)) {
      return t(`admin.signatureDecoder.verify.reqErrors.${code}`)
    }
    if (code === 'proxy' || code === 'nonJson') {
      return t(`admin.signatureDecoder.verify.reqErrors.${code}`, { detail: detail ?? '' })
    }
    if (code === 'http') {
      return t('admin.signatureDecoder.verify.reqErrors.http', { status: detail ?? '' })
    }
    return err.message
  }
  return err instanceof Error ? err.message : String(err)
}

async function start(): Promise<void> {
  running.value = true
  done.value = 0
  error.value = ''
  report.value = null
  try {
    report.value = await analyzeInjection({ ...props.conn }, (n) => {
      done.value = n
    })
  } catch (err) {
    error.value = describeError(err)
  } finally {
    running.value = false
  }
}
</script>
