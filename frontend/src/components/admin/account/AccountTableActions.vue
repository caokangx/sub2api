<template>
  <div class="flex flex-wrap items-center gap-3">
    <slot name="before"></slot>
    <button @click="$emit('refresh')" :disabled="loading" class="btn btn-secondary">
      <Icon name="refresh" size="md" :class="[loading ? 'animate-spin' : '']" />
    </button>
    <slot name="after"></slot>
    <slot name="beforeCreate"></slot>
    <button
      @click="$emit('create')"
      :disabled="readonly"
      :title="readonly ? t('admin.accounts.readonlyHint') : undefined"
      class="btn btn-primary disabled:cursor-not-allowed disabled:opacity-50"
    >
      {{ t('admin.accounts.createAccount') }}
    </button>
    <slot name="afterCreate"></slot>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps<{ loading?: boolean; readonly?: boolean }>()
defineEmits(['refresh', 'create'])

const { t } = useI18n()
</script>
