<template>
  <section class="card" data-testid="claude-anti-ban-settings">
    <header class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t("admin.settings.claudeAntiBan.title") }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t("admin.settings.claudeAntiBan.description") }}
      </p>
    </header>

    <div v-if="loading" class="p-8 text-center text-gray-500">
      {{ t("common.loading") }}
    </div>
    <div v-else class="space-y-6 p-6">
      <div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        {{ t("admin.settings.claudeAntiBan.alwaysOnHint") }}
      </div>

      <div class="flex items-center justify-between rounded-lg border border-gray-200 p-4 dark:border-dark-600">
        <div>
          <p class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t("admin.settings.claudeAntiBan.probe") }}
          </p>
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.claudeAntiBan.probeHint") }}
          </p>
        </div>
        <Toggle v-model="form.probe_interception_enabled" data-testid="anti-ban-probe" />
      </div>

      <section class="rounded-xl border border-gray-200 dark:border-dark-600" data-testid="anti-ban-module-prompt">
        <header class="rounded-t-xl border-b border-gray-100 bg-gray-50 px-5 py-4 dark:border-dark-700 dark:bg-dark-800/60">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t("admin.settings.claudeAntiBan.modules.promptOverride.title") }}
          </h3>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.claudeAntiBan.modules.promptOverride.description") }}
          </p>
        </header>
        <div class="space-y-6 p-5">
          <fieldset class="space-y-3" :disabled="groupsLoading || groupsSaving">
            <legend class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t("admin.settings.claudeAntiBan.promptOverride") }}
            </legend>
            <p class="text-sm text-gray-600 dark:text-gray-300">
              {{ t("admin.settings.claudeAntiBan.promptOverrideHint") }}
            </p>
            <ul class="list-disc space-y-1 pl-5 text-xs text-gray-500 dark:text-gray-400">
              <li>{{ t("admin.settings.claudeAntiBan.promptOverrideSystem") }}</li>
              <li>{{ t("admin.settings.claudeAntiBan.promptOverrideTools") }}</li>
              <li>{{ t("admin.settings.claudeAntiBan.promptOverrideParameters") }}</li>
            </ul>
            <p v-if="groupsLoading" class="text-sm text-gray-500" role="status">
              {{ t("common.loading") }}
            </p>
            <div v-else-if="groupsLoadFailed" class="flex items-center justify-between gap-3" role="alert">
              <p class="text-sm text-red-600 dark:text-red-400">{{ t("admin.settings.claudeAntiBan.promptGroupsLoadFailed") }}</p>
              <button type="button" class="btn btn-secondary btn-sm" data-testid="prompt-groups-retry" @click="loadPromptGroups">
                {{ t("admin.settings.claudeAntiBan.retryPromptGroups") }}
              </button>
            </div>
            <GroupSelector
              v-else
              v-model="promptGroupIds"
              :groups="promptGroups"
              :searchable="true"
              data-testid="prompt-override-groups"
            />
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t("admin.settings.claudeAntiBan.promptOverrideSelectionHint") }}
            </p>
            <div class="flex justify-end">
              <button type="button" class="btn btn-primary btn-sm" :disabled="groupsLoadFailed || !promptGroupsDirty" data-testid="prompt-groups-save" @click="savePromptGroups">
                {{ groupsSaving ? t("common.saving") : t("admin.settings.claudeAntiBan.savePromptGroups") }}
              </button>
            </div>
          </fieldset>

          <fieldset class="space-y-4 border-t border-gray-100 pt-5 dark:border-dark-700">
            <legend class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t("admin.settings.claudeAntiBan.environment") }}
            </legend>
            <div class="grid gap-4 md:grid-cols-2">
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.mode") }}</span>
                <select v-model="form.environment.mode" class="input" data-testid="anti-ban-mode">
                  <option value="off">{{ t("admin.settings.claudeAntiBan.modes.off") }}</option>
                  <option value="observe">{{ t("admin.settings.claudeAntiBan.modes.observe") }}</option>
                  <option value="rewrite">{{ t("admin.settings.claudeAntiBan.modes.rewrite") }}</option>
                </select>
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.onUnmatched") }}</span>
                <select v-model="form.environment.on_unmatched" class="input">
                  <option value="pass">{{ t("admin.settings.claudeAntiBan.unmatched.pass") }}</option>
                  <option value="reject">{{ t("admin.settings.claudeAntiBan.unmatched.reject") }}</option>
                </select>
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.workingDirectory") }}</span>
                <input v-model.trim="form.environment.profile.working_directory" class="input font-mono" data-testid="anti-ban-working-directory" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.memoryDirectory") }}</span>
                <input v-model.trim="form.environment.profile.memory_directory" class="input font-mono" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.homeDirectory") }}</span>
                <input v-model.trim="form.environment.profile.home_directory" class="input font-mono" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.osVersion") }}</span>
                <input v-model.trim="form.environment.profile.os_version" class="input font-mono" data-testid="anti-ban-os-version" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.platform") }}</span>
                <select v-model="form.environment.profile.platform" class="input">
                  <option value="darwin">darwin</option>
                  <option value="linux">linux</option>
                  <option value="win32">win32</option>
                </select>
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.shell") }}</span>
                <select v-model="form.environment.profile.shell" class="input">
                  <option value="zsh">zsh</option>
                  <option value="bash">bash</option>
                  <option value="fish">fish</option>
                  <option value="powershell">powershell</option>
                  <option value="cmd">cmd</option>
                </select>
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.arch") }}</span>
                <input v-model.trim="form.environment.profile.arch" class="input font-mono" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.runtime") }}</span>
                <input v-model.trim="form.environment.profile.runtime" class="input font-mono" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.runtimeVersion") }}</span>
                <input v-model.trim="form.environment.profile.runtime_version" class="input font-mono" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.packageVersion") }}</span>
                <input v-model.trim="form.environment.profile.package_version" class="input font-mono" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.gitUser") }}</span>
                <input v-model.trim="form.environment.profile.git_user" class="input" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.userIdentity") }}</span>
                <input v-model.trim="form.environment.profile.user_identity" class="input" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.currentBranch") }}</span>
                <input v-model.trim="form.environment.profile.current_branch" class="input" />
              </label>
              <label>
                <span class="input-label">{{ t("admin.settings.claudeAntiBan.mainBranch") }}</span>
                <input v-model.trim="form.environment.profile.main_branch" class="input" />
              </label>
            </div>
            <label class="block">
              <span class="input-label">{{ t("admin.settings.claudeAntiBan.additionalDirectories") }}</span>
              <textarea v-model="additionalDirectoriesText" rows="3" class="input font-mono text-xs" />
              <span class="input-hint">{{ t("admin.settings.claudeAntiBan.additionalDirectoriesHint") }}</span>
            </label>
          </fieldset>
        </div>
      </section>

      <section class="rounded-xl border border-gray-200 dark:border-dark-600" data-testid="anti-ban-module-thinking-strip">
        <header class="rounded-t-xl border-b border-gray-100 bg-gray-50 px-5 py-4 dark:border-dark-700 dark:bg-dark-800/60">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t("admin.settings.claudeAntiBan.modules.thinkingStrip.title") }}
          </h3>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.claudeAntiBan.modules.thinkingStrip.description") }}
          </p>
        </header>
        <div class="space-y-4 p-5">
          <div class="flex items-center justify-between gap-4">
            <div>
              <p class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t("admin.settings.claudeAntiBan.thinkingStrip.enable") }}
              </p>
              <p class="text-xs text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.claudeAntiBan.thinkingStrip.hint") }}
              </p>
            </div>
            <Toggle v-model="form.thinking_strip.enabled" data-testid="anti-ban-thinking-strip" />
          </div>
          <label class="block border-t border-gray-100 pt-4 dark:border-dark-700">
            <span class="input-label">{{ t("admin.settings.claudeAntiBan.thinkingStrip.forcePercent") }}</span>
            <input
              v-model.number="form.thinking_strip.force_strip_percent"
              type="number"
              min="0"
              max="100"
              step="1"
              class="input w-40"
              :disabled="!form.thinking_strip.enabled"
              data-testid="anti-ban-thinking-strip-percent"
            />
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
              {{ t("admin.settings.claudeAntiBan.thinkingStrip.forcePercentHint") }}
            </span>
          </label>
        </div>
      </section>

      <div class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700">
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="saving || !dirty"
          data-testid="anti-ban-save"
          @click="save"
        >
          {{ saving ? t("common.saving") : t("common.save") }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import Toggle from "@/components/common/Toggle.vue";
import GroupSelector from "@/components/common/GroupSelector.vue";
import { getAllIncludingInactive, update as updateGroup } from "@/api/admin/groups";
import type { AdminGroup } from "@/types";
import { useAppStore } from "@/stores";
import { extractApiErrorMessage } from "@/utils/apiError";
import {
  getClaudeAntiBanSettings,
  updateClaudeAntiBanSettings,
  type ClaudeAntiBanSettings,
} from "@/api/admin/settings";

const { t } = useI18n();
const appStore = useAppStore();
const loading = ref(true);
const saving = ref(false);
const baseline = ref("");
const additionalDirectoriesText = ref("");
const promptGroups = ref<AdminGroup[]>([]);
const promptGroupIds = ref<number[]>([]);
const groupsLoading = ref(true);
const groupsSaving = ref(false);
const groupsLoadFailed = ref(false);
const promptGroupsDirty = computed(() => {
  const selected = new Set(promptGroupIds.value);
  return promptGroups.value.some((group) => selected.has(group.id) !== Boolean(group.force_claude_code_profile));
});

async function loadPromptGroups(): Promise<void> {
  groupsLoading.value = true;
  groupsLoadFailed.value = false;
  try {
    promptGroups.value = (await getAllIncludingInactive()).filter((group) => group.platform === "anthropic");
    promptGroupIds.value = promptGroups.value.filter((group) => group.force_claude_code_profile).map((group) => group.id);
  } catch (error) {
    groupsLoadFailed.value = true;
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.claudeAntiBan.promptGroupsLoadFailed")));
  } finally {
    groupsLoading.value = false;
  }
}

async function savePromptGroups(): Promise<void> {
  if (groupsLoading.value || groupsSaving.value || groupsLoadFailed.value || !promptGroupsDirty.value) return;
  groupsSaving.value = true;
  const selected = new Set(promptGroupIds.value);
  const changes = promptGroups.value.filter((group) => selected.has(group.id) !== Boolean(group.force_claude_code_profile));
  try {
    const results = await Promise.allSettled(changes.map((group) => updateGroup(group.id, { force_claude_code_profile: selected.has(group.id) })));
    const failed: string[] = [];
    results.forEach((result, index) => {
      const group = changes[index];
      if (result.status === "fulfilled") {
        group.force_claude_code_profile = result.value.force_claude_code_profile;
      } else {
        failed.push(group.name);
      }
    });
    if (failed.length) {
      appStore.showError(t("admin.settings.claudeAntiBan.promptGroupsSaveFailed", { groups: failed.join(", ") }));
    } else {
      appStore.showSuccess(t("common.saved"));
    }
  } finally {
    groupsSaving.value = false;
  }
}

const defaultForm = (): ClaudeAntiBanSettings => ({
  probe_interception_enabled: true,
  environment: {
    mode: "rewrite",
    on_unmatched: "pass",
    profile: {
      working_directory: "/Users/developer/project",
      git_repository: true,
      platform: "darwin",
      shell: "zsh",
      os_version: "Darwin 24.5.0",
      additional_working_directories: [],
      memory_directory: "/Users/developer/.claude/memory/",
      git_user: "developer",
      current_branch: "main",
      main_branch: "main",
      git_status: "clean",
      recent_commits: [],
      home_directory: "/Users/developer",
      user_identity: "developer",
      arch: "arm64",
      runtime: "node",
      runtime_version: "v26.3.0",
      package_version: "0.112.1",
    },
  },
  thinking_strip: {
    enabled: false,
    force_strip_percent: 0,
  },
});
const form = reactive<ClaudeAntiBanSettings>(defaultForm());

function snapshot(): string {
  return JSON.stringify({ form, additionalDirectoriesText: additionalDirectoriesText.value });
}
const dirty = computed(() => snapshot() !== baseline.value);

async function load(): Promise<void> {
  loading.value = true;
  try {
    const settings = await getClaudeAntiBanSettings();
    Object.assign(form, JSON.parse(JSON.stringify(settings)) as ClaudeAntiBanSettings);
    additionalDirectoriesText.value =
      (settings.environment.profile.additional_working_directories ?? []).join("\n");
    baseline.value = snapshot();
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t("common.error")));
  } finally {
    loading.value = false;
  }
}

async function save(): Promise<void> {
  saving.value = true;
  try {
    form.environment.profile.additional_working_directories =
      additionalDirectoriesText.value
        .split(/\r?\n/)
        .map((item) => item.trim())
        .filter(Boolean);
    const percent = Math.round(Number(form.thinking_strip.force_strip_percent) || 0);
    form.thinking_strip.force_strip_percent = Math.min(100, Math.max(0, percent));
    const updated = await updateClaudeAntiBanSettings(
      JSON.parse(JSON.stringify(form)) as ClaudeAntiBanSettings,
    );
    Object.assign(form, updated);
    additionalDirectoriesText.value =
      (updated.environment.profile.additional_working_directories ?? []).join("\n");
    baseline.value = snapshot();
    appStore.showSuccess(t("common.saved"));
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t("common.error")));
  } finally {
    saving.value = false;
  }
}

onMounted(() => { void load(); void loadPromptGroups(); });
</script>
