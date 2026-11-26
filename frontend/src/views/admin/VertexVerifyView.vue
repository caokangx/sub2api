<template>
  <AppLayout>
    <UpstreamVerifyPanel
      i18n-prefix="admin.vertexVerify"
      icon="cloud"
      :reference-models="REFERENCE_MODELS"
      :default-select="selectGemini"
      :list-models="adminVertexVerifyAPI.listModels"
      :probe="adminVertexVerifyAPI.probe"
      :is-match="(r) => r.is_vertex"
      match-kind="vertex"
      counter-kind="non_vertex"
      warn-kind="vertex_weak"
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
            {{ t('admin.vertexVerify.results.source', { source: t(`admin.vertexVerify.results.sources.${result.detected_source}`) }) }}
          </span>
          <span
            v-for="note in result.notes"
            :key="note"
            class="rounded bg-amber-50 px-1.5 py-0.5 text-amber-700 ring-1 ring-inset ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800"
          >
            {{ t(`admin.vertexVerify.results.notes.${note}`) }}
          </span>
        </div>
        <p v-if="result.thinking" class="mt-2 text-xs text-gray-600 dark:text-gray-400">
          {{
            t('admin.vertexVerify.results.thinking', {
              thoughts: result.thinking.thoughts_tokens,
              output: result.thinking.candidates_tokens,
              parts: result.thinking.thought_parts,
              seconds: Math.round(result.thinking.duration_ms / 1000),
              answer: t(
                result.thinking.answer_correct
                  ? 'admin.vertexVerify.results.answerCorrect'
                  : 'admin.vertexVerify.results.answerWrong'
              )
            })
          }}
        </p>
      </template>
    </UpstreamVerifyPanel>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UpstreamVerifyPanel from '@/components/admin/UpstreamVerifyPanel.vue'
import { adminVertexVerifyAPI, type VertexVerifySource } from '@/api/admin/vertexVerify'

const { t } = useI18n()

// Newer Gemini models a genuine Vertex reseller is expected to carry; missing ones are flagged.
const REFERENCE_MODELS = [
  'gemini-4-hydrogen',
  'gemini-4-carbon',
  'gemini-4-argon',
  'gemini-4-neon',
  'gemini-3.1-pro-preview',
  'gemini-3.8-flash',
  'gemini-3.5-flash'
]

function selectGemini(models: string[]): string[] {
  return models.filter((m) => {
    const name = m.toLowerCase()
    return name.startsWith('gemini-4') || name.startsWith('gemini-3.1-pro')
  })
}

function sourceClass(source: VertexVerifySource): string {
  if (source === 'vertex') return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
  if (source === 'unknown') return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
}
</script>
