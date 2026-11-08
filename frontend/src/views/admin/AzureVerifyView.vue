<template>
  <AppLayout>
    <UpstreamVerifyPanel
      i18n-prefix="admin.azureVerify"
      icon="shield"
      :reference-models="REFERENCE_MODELS"
      :default-select="selectGpt6"
      :list-models="adminAzureVerifyAPI.listModels"
      :probe="adminAzureVerifyAPI.probe"
      :is-match="(r) => r.is_azure"
      match-kind="azure"
      counter-kind="non_azure"
      warn-kind="relay_tamper"
    >
      <template #result-extra="{ result }">
        <p v-if="result.relay_tampered" class="mt-2 text-xs text-amber-700 dark:text-amber-300">
          {{ t('admin.azureVerify.results.relayTampered') }}
        </p>
      </template>
    </UpstreamVerifyPanel>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UpstreamVerifyPanel from '@/components/admin/UpstreamVerifyPanel.vue'
import { adminAzureVerifyAPI } from '@/api/admin/azureVerify'

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

function selectGpt6(models: string[]): string[] {
  return models.filter((m) => m.startsWith('gpt-6'))
}
</script>
