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

const defaultForm = (): ClaudeAntiBanSettings => ({
  probe_interception_enabled: true,
  environment: {
    mode: "off",
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

onMounted(load);
</script>
