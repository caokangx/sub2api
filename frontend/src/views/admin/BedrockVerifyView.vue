<template>
  <AppLayout>
    <UpstreamVerifyPanel
      i18n-prefix="admin.bedrockVerify"
      icon="cloud"
      :reference-models="REFERENCE_MODELS"
      :default-select="selectClaude"
      :list-models="adminBedrockVerifyAPI.listModels"
      :probe="adminBedrockVerifyAPI.probe"
      :is-match="(r) => r.is_bedrock"
      match-kind="bedrock"
      counter-kind="non_bedrock"
    >
      <template #result-extra="{ result }">
        <div
          v-if="result.status === 'ok' || result.notes.length"
          class="mt-2 flex flex-wrap items-center gap-2 text-xs"
        >
          <span
            v-if="result.status === 'ok'"
            class="rounded px-1.5 py-0.5 font-medium"
            :class="sourceClass(result.detected_source)"
          >
            {{ t('admin.bedrockVerify.results.source', { source: t(`admin.bedrockVerify.results.sources.${result.detected_source}`) }) }}
          </span>
          <span
            v-for="note in result.notes"
            :key="note"
            class="rounded bg-amber-50 px-1.5 py-0.5 text-amber-700 ring-1 ring-inset ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800"
          >
            {{ t(`admin.bedrockVerify.results.notes.${note}`) }}
          </span>
        </div>
      </template>
    </UpstreamVerifyPanel>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UpstreamVerifyPanel from '@/components/admin/UpstreamVerifyPanel.vue'
import { adminBedrockVerifyAPI, type BedrockVerifySource } from '@/api/admin/bedrockVerify'

const { t } = useI18n()

// Newer Claude models a genuine Bedrock reseller is expected to carry; missing ones are flagged.
const REFERENCE_MODELS = [
  'claude-opus-5-5',
  'claude-sonnet-5-5',
  'claude-haiku-5-5',
  'claude-fable-5-1',
  'claude-fable-5',
  'claude-opus-5',
  'claude-sonnet-5',
  'claude-opus-4-8'
]

function selectClaude(models: string[]): string[] {
  return models.filter((m) => m.toLowerCase().includes('claude'))
}

function sourceClass(source: BedrockVerifySource): string {
  if (source === 'bedrock') return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
  if (source === 'unknown') return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
}
</script>
