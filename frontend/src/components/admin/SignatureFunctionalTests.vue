<template>
  <div class="card p-4 sm:p-6">
    <p class="text-sm text-gray-500 dark:text-gray-400">
      {{ t(`${P}.description`) }}
    </p>
    <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
      {{ t(`${P}.modelNote`) }}
      <span
        class="ml-1 rounded-md px-2 py-0.5 font-mono ring-1 ring-inset"
        :class="isFable ? 'bg-green-50 text-green-700 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800' : 'bg-gray-50 text-gray-600 ring-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:ring-dark-600'"
      >
        {{ isFable ? t(`${P}.fableStrict`) : t(`${P}.fableInfo`) }}
      </span>
    </p>

    <div class="mt-4 space-y-3">
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="running" @click="selectAll">
          {{ t(`${P}.selectAll`) }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="running" @click="invertSelection">
          {{ t(`${P}.invert`) }}
        </button>
        <span class="text-xs text-gray-500 dark:text-gray-400">
          {{ t(`${P}.selectedCount`, { n: selected.size, total: FUNCTIONAL_CASES.length, tokens: estimatedTokens.toLocaleString() }) }}
        </span>
      </div>

      <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
        <div
          v-for="group in groupedCases"
          :key="group.id"
          class="rounded-md border border-gray-200 p-3 dark:border-dark-700"
        >
          <div class="mb-2 text-xs font-semibold text-gray-700 dark:text-gray-300">
            {{ t(`${P}.groups.${group.id}`) }}
          </div>
          <label
            v-for="c in group.cases"
            :key="c.id"
            class="flex cursor-pointer items-start gap-2 py-0.5 text-xs text-gray-700 dark:text-gray-300"
          >
            <input
              type="checkbox"
              class="mt-0.5"
              :checked="selected.has(c.id)"
              :disabled="running"
              @change="toggle(c.id)"
            />
            <span>
              <span class="font-mono text-gray-500 dark:text-gray-400">{{ c.id }}.</span>
              {{ caseTitle(t, c.id) }}
              <span v-if="c.estInputTokens >= 10000" class="ml-1 text-amber-600 dark:text-amber-400">
                ~{{ formatTokens(c.estInputTokens) }}
              </span>
            </span>
          </label>
        </div>
      </div>
    </div>

    <div class="mt-4 flex flex-wrap items-center gap-3">
      <button
        type="button"
        class="btn btn-primary btn-sm"
        :disabled="running || !canConnect || selected.size === 0"
        @click="confirmOpen = true"
      >
        <Icon :name="running ? 'refresh' : 'play'" size="xs" :class="running ? 'animate-spin' : ''" />
        {{ t(`${P}.start`) }}
      </button>
      <button v-if="running" type="button" class="btn btn-secondary btn-sm" @click="stop">
        <Icon name="x" size="xs" />
        {{ t(`${P}.stop`) }}
      </button>
      <span v-if="running && currentId" class="text-xs text-gray-500 dark:text-gray-400">
        {{ t(`${P}.progress`, { id: currentId, title: caseTitle(t, currentId), done: doneCount, total: totalCount }) }}
      </span>
    </div>

    <p v-if="error" class="mt-3 break-all text-sm text-red-600 dark:text-red-400">{{ error }}</p>

    <div v-if="results.length" class="mt-6 space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t(`${P}.report.title`) }}</h3>
        <div v-if="report" class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-secondary btn-sm" @click="exportMarkdown">
            <Icon name="download" size="xs" />
            {{ t(`${P}.exportMarkdown`) }}
          </button>
          <button type="button" class="btn btn-secondary btn-sm" @click="exportJSON">
            <Icon name="download" size="xs" />
            {{ t(`${P}.exportJSON`) }}
          </button>
        </div>
      </div>

      <dl class="grid grid-cols-2 gap-3 sm:grid-cols-5">
        <div v-for="s in summaryCards" :key="s.key" class="rounded-md border p-3" :class="s.cls">
          <dt class="text-xs">{{ t(`${P}.status.${s.key}`) }}</dt>
          <dd class="mt-1 text-xl font-semibold">{{ s.value }}</dd>
        </div>
      </dl>
      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t(`${P}.report.assertionCounts`, assertionCounts) }}
        · {{ t(`${P}.report.tokens`) }}: input {{ totalUsage.input.toLocaleString() }} / output {{ totalUsage.output.toLocaleString() }}
        <template v-if="report"> · {{ t(`${P}.report.elapsed`) }}: {{ seconds(report.elapsedMs) }}</template>
      </p>

      <div class="overflow-x-auto">
        <table class="w-full min-w-[720px] text-left text-sm">
          <thead class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
            <tr>
              <th class="w-8 py-2"></th>
              <th class="py-2 pr-4 font-medium">#</th>
              <th class="py-2 pr-4 font-medium">{{ t(`${P}.report.case`) }}</th>
              <th class="py-2 pr-4 font-medium">{{ t(`${P}.report.status`) }}</th>
              <th class="py-2 pr-4 font-medium">{{ t(`${P}.report.checks`) }}</th>
              <th class="py-2 pr-4 font-medium">{{ t(`${P}.report.elapsed`) }}</th>
              <th class="py-2 font-medium">input / output</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <template v-for="r in results" :key="r.id">
              <tr class="cursor-pointer align-top hover:bg-gray-50 dark:hover:bg-dark-800" @click="toggleExpand(r.id)">
                <td class="py-2 text-gray-400">
                  <Icon :name="expanded.has(r.id) ? 'chevronDown' : 'chevronRight'" size="xs" />
                </td>
                <td class="py-2 pr-4 font-mono text-xs text-gray-500 dark:text-gray-400">{{ r.id }}</td>
                <td class="py-2 pr-4 text-gray-900 dark:text-white">{{ caseTitle(t, r.id) }}</td>
                <td class="py-2 pr-4">
                  <span class="rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset" :class="statusClass(r.status)">
                    {{ t(`${P}.status.${r.status}`) }}
                  </span>
                </td>
                <td class="py-2 pr-4 font-mono text-xs text-gray-500 dark:text-gray-400">{{ checkSummary(r) }}</td>
                <td class="py-2 pr-4 font-mono text-xs text-gray-500 dark:text-gray-400">{{ seconds(r.elapsedMs) }}</td>
                <td class="py-2 font-mono text-xs text-gray-500 dark:text-gray-400">
                  {{ r.usage.input.toLocaleString() }} / {{ r.usage.output.toLocaleString() }}
                </td>
              </tr>
              <tr v-if="expanded.has(r.id)">
                <td></td>
                <td colspan="6" class="space-y-4 pb-4 pr-2">
                  <p v-if="r.error" class="break-all text-sm text-red-600 dark:text-red-400">
                    {{ t(`${P}.report.error`) }}: {{ r.error }}
                  </p>
                  <ul v-if="r.assertions.length" class="space-y-1">
                    <li v-for="(a, i) in r.assertions" :key="i" class="flex flex-wrap items-start gap-2 text-xs">
                      <span class="rounded px-1.5 py-0.5 font-mono font-medium ring-1 ring-inset" :class="assertionClass(a.status)">
                        {{ a.status.toUpperCase() }}
                      </span>
                      <span class="font-mono text-gray-400">#{{ a.step }}</span>
                      <span class="text-gray-800 dark:text-gray-200">{{ assertionText(t, a) }}</span>
                      <span v-if="a.level === 'bonus'" class="text-gray-400">({{ t(`${P}.level.bonus`) }})</span>
                      <span v-if="a.scope === 'fable'" class="text-gray-400">({{ t(`${P}.scope.fable`) }})</span>
                      <span v-if="a.actual !== undefined" class="break-all font-mono text-gray-500 dark:text-gray-400">
                        → {{ a.actual }}
                      </span>
                    </li>
                  </ul>
                  <details v-for="(step, i) in r.steps" :key="i" class="rounded-md border border-gray-200 p-3 dark:border-dark-700">
                    <summary class="cursor-pointer select-none text-xs font-medium text-gray-700 dark:text-gray-300">
                      #{{ i + 1 }} {{ stepLabel(t, step) }}
                      <span class="ml-2 font-mono font-normal text-gray-500 dark:text-gray-400">
                        <template v-if="step.reply">HTTP {{ step.reply.status }} · {{ step.reply.elapsedMs }} ms</template>
                        <template v-else>-</template>
                        <template v-if="step.beta"> · anthropic-beta: {{ step.beta }}</template>
                      </span>
                    </summary>
                    <div class="mt-3 space-y-3">
                      <div>
                        <div class="mb-1 flex items-center justify-between">
                          <span class="input-label mb-0">{{ t(`${P}.report.request`) }}</span>
                          <button type="button" class="btn btn-secondary btn-sm" @click="copyCurl(r.id, i, step)">
                            <Icon name="terminal" size="xs" />
                            {{ copiedKey === `${r.id}-${i}` ? t('admin.signatureDecoder.verify.copied') : t(`${P}.copyCurl`) }}
                          </button>
                        </div>
                        <pre class="max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-md bg-gray-50 p-3 font-mono text-xs text-gray-900 dark:bg-dark-800 dark:text-white">{{ requestPreview(step) }}</pre>
                      </div>
                      <div>
                        <span class="input-label mb-1">{{ t(`${P}.report.response`) }}</span>
                        <pre class="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-gray-50 p-3 font-mono text-xs text-gray-900 dark:bg-dark-800 dark:text-white">{{ responsePreview(step) }}</pre>
                      </div>
                    </div>
                  </details>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>

    <ConfirmDialog
      :show="confirmOpen"
      :title="t(`${P}.confirm.title`)"
      :message="t(`${P}.confirm.message`, { n: selected.size, tokens: estimatedTokens.toLocaleString() })"
      :confirm-text="t(`${P}.start`)"
      @confirm="start"
      @cancel="confirmOpen = false"
    >
      <p v-if="selectedLongContext" class="text-xs text-amber-700 dark:text-amber-400">
        {{ t(`${P}.confirm.longContext`) }}
      </p>
    </ConfirmDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { buildMessagesCurl, type ReasoningConn } from '@/utils/claudeReasoning'
import {
  assertionText,
  buildReport,
  CASE_GROUPS,
  caseTitle,
  estimateInputTokens,
  FUNCTIONAL_CASES,
  I18N_PREFIX as P,
  isFableModel,
  orderCases,
  runFunctionalTests,
  sanitize,
  stepLabel,
  toJSON,
  toMarkdown,
  truncateString,
  type AssertionStatus,
  type CaseResult,
  type CaseStatus,
  type FunctionalReport,
  type StepRecord
} from '@/utils/claudeFunctionalTests'

const props = defineProps<{
  conn: ReasoningConn
}>()

const { t } = useI18n()

const selected = ref(new Set(FUNCTIONAL_CASES.map((c) => c.id)))
const running = ref(false)
const confirmOpen = ref(false)
const currentId = ref('')
const doneCount = ref(0)
const totalCount = ref(0)
const error = ref('')
const results = shallowRef<CaseResult[]>([])
const report = shallowRef<FunctionalReport | null>(null)
const expanded = ref(new Set<string>())
const copiedKey = ref('')
let controller: AbortController | null = null

const canConnect = computed(
  () => !!props.conn.baseUrl.trim() && !!props.conn.apiKey.trim() && !!props.conn.model.trim()
)
const isFable = computed(() => isFableModel(props.conn.model))
const estimatedTokens = computed(() => estimateInputTokens(selected.value))
const selectedLongContext = computed(() =>
  FUNCTIONAL_CASES.some((c) => c.group === 'longContext' && selected.value.has(c.id))
)

const groupedCases = computed(() =>
  CASE_GROUPS.map((id) => ({ id, cases: FUNCTIONAL_CASES.filter((c) => c.group === id) }))
)

function toggle(id: string): void {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}

function selectAll(): void {
  selected.value = new Set(FUNCTIONAL_CASES.map((c) => c.id))
}

function invertSelection(): void {
  selected.value = new Set(FUNCTIONAL_CASES.filter((c) => !selected.value.has(c.id)).map((c) => c.id))
}

function toggleExpand(id: string): void {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

function formatTokens(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}K` : String(n)
}

function seconds(ms: number): string {
  return `${(ms / 1000).toFixed(1)}s`
}

const countBy = <K extends string>(keys: readonly K[], pick: (r: CaseResult) => K[]) => {
  const out = Object.fromEntries(keys.map((k) => [k, 0])) as Record<K, number>
  for (const r of results.value) for (const k of pick(r)) out[k]++
  return out
}

const statusCounts = computed(() =>
  countBy<CaseStatus>(['pass', 'fail', 'error', 'skipped'], (r) => [r.status])
)
const assertionCounts = computed(() =>
  countBy<AssertionStatus>(['pass', 'fail', 'warn', 'info'], (r) => r.assertions.map((a) => a.status))
)
const totalUsage = computed(() =>
  results.value.reduce(
    (u, r) => ({ input: u.input + r.usage.input, output: u.output + r.usage.output }),
    { input: 0, output: 0 }
  )
)

const summaryCards = computed(() => [
  { key: 'pass', value: statusCounts.value.pass, cls: 'border-green-200 bg-green-50 text-green-800 dark:border-green-800 dark:bg-green-900/20 dark:text-green-300' },
  { key: 'fail', value: statusCounts.value.fail, cls: 'border-red-200 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300' },
  { key: 'error', value: statusCounts.value.error, cls: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300' },
  { key: 'skipped', value: statusCounts.value.skipped, cls: 'border-gray-200 bg-gray-50 text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300' },
  { key: 'total', value: results.value.length, cls: 'border-gray-200 text-gray-900 dark:border-dark-600 dark:text-white' }
])

function statusClass(status: CaseStatus): string {
  switch (status) {
    case 'pass':
      return 'bg-green-50 text-green-700 ring-green-200 dark:bg-green-900/20 dark:text-green-300 dark:ring-green-800'
    case 'fail':
      return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800'
    case 'error':
      return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800'
    default:
      return 'bg-gray-50 text-gray-600 ring-gray-200 dark:bg-dark-800 dark:text-gray-400 dark:ring-dark-600'
  }
}

function assertionClass(status: AssertionStatus): string {
  switch (status) {
    case 'pass':
      return statusClass('pass')
    case 'fail':
      return statusClass('fail')
    case 'warn':
      return statusClass('error')
    default:
      return 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-900/20 dark:text-blue-300 dark:ring-blue-800'
  }
}

function checkSummary(r: CaseResult): string {
  const passed = r.assertions.filter((a) => a.status === 'pass').length
  const judged = r.assertions.filter((a) => a.status !== 'info').length
  return r.assertions.length ? `${passed}/${judged}` : '-'
}

function requestPreview(step: StepRecord): string {
  return JSON.stringify(sanitize(step.request, props.conn.apiKey), null, 2)
}

function responsePreview(step: StepRecord): string {
  const reply = step.reply
  if (!reply) return '-'
  if (reply.body !== null && reply.body !== undefined) {
    return JSON.stringify(sanitize(reply.body, props.conn.apiKey), null, 2)
  }
  return truncateString(reply.text, 20000) || '-'
}

async function copyCurl(caseId: string, index: number, step: StepRecord): Promise<void> {
  try {
    await navigator.clipboard.writeText(buildMessagesCurl(props.conn, step.request, step.beta))
    const key = `${caseId}-${index}`
    copiedKey.value = key
    setTimeout(() => {
      if (copiedKey.value === key) copiedKey.value = ''
    }, 1200)
  } catch {
    // ignore clipboard failures (e.g. insecure context)
  }
}

async function start(): Promise<void> {
  confirmOpen.value = false
  if (running.value) return
  const conn: ReasoningConn = { ...props.conn, model: props.conn.model.trim() }
  const ids = [...selected.value]
  const startedAt = new Date()
  controller = new AbortController()
  running.value = true
  error.value = ''
  report.value = null
  expanded.value = new Set()
  results.value = []
  doneCount.value = 0
  totalCount.value = orderCases(ids).length
  try {
    const all = await runFunctionalTests(conn, ids, {
      signal: controller.signal,
      onCaseStart: (id) => {
        currentId.value = id
      },
      onCaseDone: (res, index) => {
        doneCount.value = index + 1
        results.value = [...results.value, res]
      }
    })
    results.value = all
    report.value = buildReport(
      { model: conn.model, baseUrl: conn.baseUrl.trim(), isFable: isFableModel(conn.model), startedAt, finishedAt: new Date() },
      all
    )
    expanded.value = new Set(all.filter((r) => r.status === 'fail' || r.status === 'error').map((r) => r.id))
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    running.value = false
    currentId.value = ''
    controller = null
  }
}

function stop(): void {
  controller?.abort()
}

onBeforeUnmount(stop)

function download(content: string, ext: string, mime: string): void {
  const stamp = (report.value?.startedAt ?? new Date().toISOString()).replace(/[:.]/g, '-')
  const model = (report.value?.model ?? 'model').replace(/[^\w.-]+/g, '_')
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = `functional-test-${model}-${stamp}.${ext}`
  a.click()
  URL.revokeObjectURL(url)
}

function exportMarkdown(): void {
  if (!report.value) return
  const translate = (key: string, params?: Record<string, unknown>) => t(key, params ?? {})
  download(toMarkdown(report.value, props.conn.apiKey, translate), 'md', 'text/markdown;charset=utf-8')
}

function exportJSON(): void {
  if (!report.value) return
  download(toJSON(report.value, props.conn.apiKey), 'json', 'application/json')
}
</script>
