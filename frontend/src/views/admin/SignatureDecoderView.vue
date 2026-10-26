<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-6 pb-8">
      <!-- ==================== Section 1: decode ==================== -->
      <div ref="decodeSection" class="flex items-center gap-2">
        <Icon name="search" size="md" class="text-gray-500 dark:text-gray-400" />
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('admin.signatureDecoder.sections.decode') }}
        </h2>
      </div>

      <div class="card p-4 sm:p-6">
        <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.signatureDecoder.description') }}
        </p>
        <div class="mb-2 flex items-center justify-between">
          <label class="input-label mb-0" for="signature-input">
            {{ t('admin.signatureDecoder.inputLabel') }}
          </label>
          <button v-if="input" type="button" class="btn btn-secondary btn-sm" @click="input = ''">
            {{ t('admin.signatureDecoder.clear') }}
          </button>
        </div>
        <textarea
          id="signature-input"
          v-model="input"
          rows="6"
          spellcheck="false"
          class="input break-all font-mono text-xs"
          :placeholder="t('admin.signatureDecoder.inputPlaceholder')"
        />
        <p v-if="errorKey" class="mt-2 text-sm text-red-600 dark:text-red-400">
          {{ t(`admin.signatureDecoder.errors.${errorKey}`) }}
        </p>
      </div>

      <template v-if="parsed">
        <div class="card p-4 sm:p-6">
          <h3 class="mb-4 text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.signatureDecoder.summary.title') }}
          </h3>
          <dl class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <div v-for="item in summaryItems" :key="item.key">
              <dt class="text-xs text-gray-500 dark:text-gray-400">
                {{ t(`admin.signatureDecoder.summary.${item.key}`) }}
              </dt>
              <dd class="mt-1 break-all font-mono text-sm text-gray-900 dark:text-white">
                {{ item.value || '-' }}
              </dd>
            </div>
            <div v-if="createdAt">
              <dt class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.signatureDecoder.summary.createdAt') }}
              </dt>
              <dd class="mt-1 space-y-1">
                <span class="local-time-badge text-sm">
                  <Icon name="clock" size="sm" />
                  {{ createdAt.local }}
                  <span class="opacity-70">{{ createdAt.zone }}</span>
                </span>
                <div class="font-mono text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.signatureDecoder.time.unix') }}: {{ parsed.summary.createdAtUnix }}
                </div>
                <div class="font-mono text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.signatureDecoder.time.utc') }}: {{ createdAt.utc }}
                </div>
              </dd>
            </div>
          </dl>
        </div>

        <div class="card p-4 sm:p-6">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.signatureDecoder.fields.title') }}
          </h3>
          <p class="mb-4 mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.signatureDecoder.fields.hint') }}
          </p>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[720px] text-left text-sm">
              <thead class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
                <tr>
                  <th class="py-2 pr-4 font-medium">{{ t('admin.signatureDecoder.fields.path') }}</th>
                  <th class="py-2 pr-4 font-medium">{{ t('admin.signatureDecoder.fields.kind') }}</th>
                  <th class="py-2 pr-4 font-medium">{{ t('admin.signatureDecoder.fields.length') }}</th>
                  <th class="py-2 pr-4 font-medium">{{ t('admin.signatureDecoder.fields.value') }}</th>
                  <th class="py-2 font-medium">{{ t('admin.signatureDecoder.fields.meaning') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="field in flatFields" :key="field.path" class="align-top">
                  <td class="whitespace-nowrap py-2 pr-4 font-mono text-xs text-gray-700 dark:text-gray-300">
                    <span :style="{ paddingLeft: `${field.depth * 1.25}rem` }">{{ field.path }}</span>
                  </td>
                  <td class="whitespace-nowrap py-2 pr-4 text-xs text-gray-500 dark:text-gray-400">
                    {{ field.kind }}
                  </td>
                  <td class="whitespace-nowrap py-2 pr-4 text-xs text-gray-500 dark:text-gray-400">
                    {{ field.length ?? '' }}
                  </td>
                  <td class="max-w-md break-all py-2 pr-4 font-mono text-xs text-gray-900 dark:text-white">
                    <div v-if="field.timestamp" class="flex flex-wrap items-center gap-2">
                      <span>{{ field.value }}</span>
                      <span
                        class="local-time-badge text-xs"
                        :title="`${t('admin.signatureDecoder.time.utc')}: ${toLocalTime(field.timestamp).utc}`"
                      >
                        <Icon name="clock" size="xs" />
                        {{ toLocalTime(field.timestamp).local }}
                        <span class="opacity-70">{{ toLocalTime(field.timestamp).zone }}</span>
                      </span>
                    </div>
                    <template v-else>{{ field.value }}</template>
                  </td>
                  <td class="py-2 text-xs text-gray-500 dark:text-gray-400">
                    {{ meaningOf(field.path) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>

      <!-- ==================== Section 2: genuineness check ==================== -->
      <div class="flex items-center gap-2 pt-4">
        <Icon name="key" size="md" class="text-gray-500 dark:text-gray-400" />
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('admin.signatureDecoder.sections.verify') }}
        </h2>
      </div>

      <div class="card p-4 sm:p-6">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.signatureDecoder.verify.description') }}
        </p>
      </div>

      <div class="card p-4 sm:p-6">
        <div class="mb-1 flex items-center gap-2">
          <Icon name="link" size="sm" class="text-gray-500 dark:text-gray-400" />
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.signatureDecoder.verify.connection.title') }}
          </h3>
        </div>
        <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.signatureDecoder.verify.connection.hint') }}
        </p>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <label class="input-label" for="verify-base-url">
              {{ t('admin.signatureDecoder.verify.connection.baseUrl') }}
            </label>
            <input
              id="verify-base-url"
              v-model="conn.baseUrl"
              spellcheck="false"
              class="input font-mono text-xs"
              placeholder="https://api.anthropic.com"
            />
          </div>
          <div>
            <label class="input-label" for="verify-api-key">
              {{ t('admin.signatureDecoder.verify.connection.apiKey') }}
            </label>
            <input
              id="verify-api-key"
              v-model="conn.apiKey"
              type="password"
              autocomplete="off"
              spellcheck="false"
              class="input font-mono text-xs"
              :placeholder="t('admin.signatureDecoder.verify.connection.apiKeyPlaceholder')"
            />
          </div>
          <div>
            <label class="input-label" for="verify-model">
              {{ t('admin.signatureDecoder.verify.connection.model') }}
            </label>
            <select id="verify-model" v-model="conn.model" class="input font-mono text-xs">
              <option v-for="m in verifyModels" :key="m" :value="m">{{ m }}</option>
            </select>
          </div>
        </div>
      </div>

      <div class="card p-4 sm:p-6">
        <div class="mb-4">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.signatureDecoder.verify.secretLabel') }}
          </div>
          <div
            class="mt-1 font-mono text-xl font-semibold tracking-wide"
            :class="secret ? 'text-gray-900 dark:text-white' : 'text-gray-400 dark:text-gray-500'"
          >
            {{ secret || t('admin.signatureDecoder.verify.secretPending') }}
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <button
            type="button"
            class="btn btn-primary btn-sm"
            :disabled="busy || !canConnect"
            @click="startCheck"
          >
            <Icon :name="step === 'harvest' ? 'refresh' : 'play'" size="xs" :class="step === 'harvest' ? 'animate-spin' : ''" />
            {{ t('admin.signatureDecoder.verify.start') }}
          </button>
          <button
            v-if="signature"
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="busy || !canConnect"
            @click="retryReplay"
          >
            <Icon :name="step === 'replay' ? 'refresh' : 'key'" size="xs" :class="step === 'replay' ? 'animate-spin' : ''" />
            {{ t('admin.signatureDecoder.verify.retry') }}
          </button>
          <span v-if="step" class="text-xs text-gray-500 dark:text-gray-400">
            {{ t(step === 'harvest' ? 'admin.signatureDecoder.verify.steps.harvest' : 'admin.signatureDecoder.verify.steps.replay') }}
          </span>
        </div>

        <p v-if="checkError" class="mt-3 text-sm text-red-600 dark:text-red-400">
          {{ checkError }}
        </p>

        <div
          v-if="verdict === 'genuine'"
          class="mt-4 rounded-md border border-green-200 bg-green-50 p-4 text-sm text-green-800 dark:border-green-800 dark:bg-green-900/20 dark:text-green-300"
        >
          <Icon name="check" size="sm" class="mr-1 inline" />
          {{ t('admin.signatureDecoder.verify.verdict.genuine', { secret: checkedSecret }) }}
        </div>
        <div
          v-else-if="verdict === 'notRecovered'"
          class="mt-4 rounded-md border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
        >
          {{ t('admin.signatureDecoder.verify.verdict.notRecovered') }}
        </div>

        <details v-if="signature" class="mt-5">
          <summary class="cursor-pointer select-none text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.signatureDecoder.verify.details.title') }}
          </summary>
          <div class="mt-3 space-y-4">
            <div>
              <div class="mb-1 flex flex-wrap items-center justify-between gap-2">
                <label class="input-label mb-0">
                  {{ t('admin.signatureDecoder.verify.details.signature') }}
                  <span class="ml-2 font-normal text-gray-500 dark:text-gray-400">
                    {{ t('admin.signatureDecoder.verify.details.signatureMeta', { model: boundModel || '-', chars: signature.length, tokens: thinkingTokens }) }}
                  </span>
                </label>
                <button type="button" class="btn btn-secondary btn-sm" @click="openInDecoder">
                  <Icon name="search" size="xs" />
                  {{ t('admin.signatureDecoder.verify.details.openInDecoder') }}
                </button>
              </div>
              <pre class="max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-md bg-gray-50 p-3 font-mono text-xs text-gray-900 dark:bg-dark-800 dark:text-white">{{ signature }}</pre>
            </div>
            <div v-if="replayRaw">
              <div class="mb-1 flex items-center justify-between">
                <label class="input-label mb-0">
                  {{ t('admin.signatureDecoder.verify.details.recovered') }}
                </label>
                <button type="button" class="btn btn-secondary btn-sm" @click="copyText(replayAnswer, 'answer')">
                  <Icon name="copy" size="xs" />
                  {{ copied === 'answer' ? t('admin.signatureDecoder.verify.copied') : t('admin.signatureDecoder.verify.copy') }}
                </button>
              </div>
              <pre class="max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-md bg-gray-50 p-3 font-mono text-xs text-gray-900 dark:bg-dark-800 dark:text-white">{{ replayAnswer || t('admin.signatureDecoder.verify.details.empty') }}</pre>
            </div>
            <div v-if="replayRaw">
              <div class="mb-1 flex items-center justify-between">
                <label class="input-label mb-0">
                  {{ t('admin.signatureDecoder.verify.details.rawResponse') }}
                </label>
                <button type="button" class="btn btn-secondary btn-sm" @click="copyText(replayRaw, 'raw')">
                  <Icon name="copy" size="xs" />
                  {{ copied === 'raw' ? t('admin.signatureDecoder.verify.copied') : t('admin.signatureDecoder.verify.copy') }}
                </button>
              </div>
              <pre class="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-md bg-gray-50 p-3 font-mono text-xs text-gray-900 dark:bg-dark-800 dark:text-white">{{ replayRaw }}</pre>
            </div>
          </div>
        </details>
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
  flattenProtoFields,
  KNOWN_SIGNATURE_FIELDS,
  parseClaudeSignature,
  SignatureParseError,
  toLocalTime,
  type ParsedClaudeSignature
} from '@/utils/claudeSignature'
import {
  buildMemorizePrompt,
  harvest,
  randomSecret,
  ReasoningError,
  replay,
  VERIFY_ELICITORS,
  VERIFY_PRECEDING_USER,
  type ReasoningConn
} from '@/utils/claudeReasoning'
import { claudeModels } from '@/composables/useModelWhitelist'

const { t } = useI18n()

// ==================== Section 1: decode ====================

const decodeSection = ref<HTMLElement | null>(null)
const input = ref('')

const result = computed<{ parsed: ParsedClaudeSignature | null; errorKey: string | null }>(() => {
  if (!input.value.trim()) {
    return { parsed: null, errorKey: null }
  }
  try {
    return { parsed: parseClaudeSignature(input.value), errorKey: null }
  } catch (err) {
    return { parsed: null, errorKey: err instanceof SignatureParseError ? err.message : 'notProtobuf' }
  }
})

const parsed = computed(() => result.value.parsed)
const errorKey = computed(() => result.value.errorKey)

const flatFields = computed(() => (parsed.value ? flattenProtoFields(parsed.value.fields) : []))

const createdAt = computed(() =>
  parsed.value?.summary.createdAt ? toLocalTime(parsed.value.summary.createdAt) : null
)

const summaryItems = computed(() => {
  const p = parsed.value
  if (!p) return []
  return [
    { key: 'model', value: p.summary.model },
    { key: 'requestedModel', value: p.summary.requestedModel },
    { key: 'blockType', value: p.summary.blockType },
    { key: 'id', value: p.summary.id },
    { key: 'byteLength', value: t('admin.signatureDecoder.summary.bytes', { n: p.byteLength }) }
  ]
})

function meaningOf(path: string): string {
  const key = KNOWN_SIGNATURE_FIELDS[path]
  return key ? t(`admin.signatureDecoder.meanings.${key}`) : ''
}

// ==================== Section 2: genuineness check ====================

// The harvest request uses adaptive thinking, which Claude 3.x and the dated
// 4 / 4.1 / 4.5 releases reject (they only take enabled + budget_tokens).
const verifyModels = claudeModels.filter((m) => !/^claude-3|-\d{8}$/.test(m)).sort()

const conn = ref<ReasoningConn>({
  baseUrl: '',
  apiKey: '',
  model: 'claude-sonnet-4-6'
})

const secret = ref('')
// The secret the current signature was harvested with (retries keep checking against it).
const checkedSecret = ref('')
const signature = ref('')
const thinkingTokens = ref(0)
const step = ref<'' | 'harvest' | 'replay'>('')
const verdict = ref<'' | 'genuine' | 'notRecovered'>('')
const checkError = ref('')
const replayAnswer = ref('')
const replayRaw = ref('')
const copied = ref<'answer' | 'raw' | ''>('')

const busy = computed(() => step.value !== '')
const canConnect = computed(
  () => !!conn.value.baseUrl.trim() && !!conn.value.apiKey.trim() && !!conn.value.model.trim()
)

const boundModel = computed(() => {
  if (!signature.value) return ''
  try {
    return parseClaudeSignature(signature.value).summary.model ?? ''
  } catch {
    return ''
  }
})

function describeError(err: unknown): string {
  if (err instanceof ReasoningError) {
    const [code, detail] = err.message.split(/:(.*)/s)
    const known = ['missingBaseUrl', 'missingApiKey', 'missingSignature', 'upstreamError']
    if (known.includes(code)) {
      return t(`admin.signatureDecoder.verify.reqErrors.${code}`)
    }
    if (code === 'proxy') {
      return t('admin.signatureDecoder.verify.reqErrors.proxy', { detail: detail ?? '' })
    }
    if (code === 'nonJson') {
      return t('admin.signatureDecoder.verify.reqErrors.nonJson', { detail: detail ?? '' })
    }
    if (code === 'http') {
      return t('admin.signatureDecoder.verify.reqErrors.http', { status: detail ?? '' })
    }
    return err.message
  }
  return err instanceof Error ? err.message : String(err)
}

/** Replay the current signature, trying each elicitor until the secret comes back. */
async function runReplay(): Promise<void> {
  step.value = 'replay'
  replayAnswer.value = ''
  replayRaw.value = ''
  const replayModel = boundModel.value || conn.value.model
  for (const elicit of VERIFY_ELICITORS) {
    const res = await replay(
      conn.value,
      signature.value,
      { precedingUser: VERIFY_PRECEDING_USER, elicit },
      replayModel
    )
    replayAnswer.value = res.answer
    replayRaw.value = JSON.stringify(res.raw, null, 2)
    if (res.answer.includes(checkedSecret.value)) {
      verdict.value = 'genuine'
      return
    }
  }
  verdict.value = 'notRecovered'
}

async function startCheck(): Promise<void> {
  checkError.value = ''
  verdict.value = ''
  signature.value = ''
  thinkingTokens.value = 0
  replayAnswer.value = ''
  replayRaw.value = ''
  secret.value = randomSecret()
  checkedSecret.value = secret.value
  try {
    step.value = 'harvest'
    const res = await harvest(conn.value, buildMemorizePrompt(secret.value))
    if (!res.signature) {
      checkError.value = t('admin.signatureDecoder.verify.noSignature')
      return
    }
    signature.value = res.signature
    thinkingTokens.value = res.thinkingTokens
    await runReplay()
  } catch (err) {
    checkError.value = describeError(err)
  } finally {
    step.value = ''
  }
}

async function retryReplay(): Promise<void> {
  checkError.value = ''
  verdict.value = ''
  try {
    await runReplay()
  } catch (err) {
    checkError.value = describeError(err)
  } finally {
    step.value = ''
  }
}

function openInDecoder(): void {
  input.value = signature.value
  decodeSection.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function copyText(value: string, which: 'answer' | 'raw'): Promise<void> {
  try {
    await navigator.clipboard.writeText(value)
    copied.value = which
    setTimeout(() => {
      if (copied.value === which) copied.value = ''
    }, 1200)
  } catch {
    // ignore clipboard failures (e.g. insecure context)
  }
}
</script>

<style scoped>
.local-time-badge {
  @apply inline-flex items-center gap-1 whitespace-nowrap rounded-md bg-blue-50 px-2 py-0.5 font-mono font-medium text-blue-700 ring-1 ring-inset ring-blue-200 dark:bg-blue-900/30 dark:text-blue-300 dark:ring-blue-800;
}
</style>
