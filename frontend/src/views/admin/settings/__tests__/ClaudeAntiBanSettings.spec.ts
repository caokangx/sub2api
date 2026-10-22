import { beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import ClaudeAntiBanSettings from "../ClaudeAntiBanSettings.vue";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock("@/api/admin/settings", () => ({
  getClaudeAntiBanSettings: mocks.get,
  updateClaudeAntiBanSettings: mocks.update,
}));

vi.mock("@/stores", () => ({
  useAppStore: () => ({
    showError: mocks.showError,
    showSuccess: mocks.showSuccess,
  }),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (key: string) => key }),
}));

const ToggleStub = defineComponent({
  props: { modelValue: { type: Boolean, default: false } },
  emits: ["update:modelValue"],
  setup(props, { attrs, emit }) {
    return () => h("input", {
      ...attrs,
      type: "checkbox",
      checked: props.modelValue,
      onChange: (event: Event) =>
        emit("update:modelValue", (event.target as HTMLInputElement).checked),
    });
  },
});

const response = () => ({
  probe_interception_enabled: true,
  environment: {
    mode: "rewrite" as const,
    on_unmatched: "pass" as const,
    profile: {
      working_directory: "/Users/developer/project",
      git_repository: true,
      platform: "darwin" as const,
      shell: "zsh" as const,
      os_version: "Darwin 24.5.0",
      additional_working_directories: [],
      memory_directory: "/Users/developer/.claude/memory/",
      git_user: "developer",
      current_branch: "main",
      main_branch: "main",
      git_status: "clean" as const,
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

beforeEach(() => {
  vi.clearAllMocks();
  mocks.get.mockResolvedValue(response());
  mocks.update.mockImplementation(async (payload) => payload);
});

describe("ClaudeAntiBanSettings", () => {
  it("loads the macOS profile with probe interception enabled", async () => {
    const wrapper = mount(ClaudeAntiBanSettings, {
      global: { stubs: { Toggle: ToggleStub } },
    });
    await flushPromises();

    expect(wrapper.get<HTMLInputElement>('[data-testid="anti-ban-probe"]').element.checked).toBe(true);
    expect(wrapper.get<HTMLSelectElement>('[data-testid="anti-ban-mode"]').element.value).toBe("rewrite");
    expect(wrapper.text()).toContain("admin.settings.claudeAntiBan.alwaysOnHint");
    expect(wrapper.get<HTMLInputElement>('[data-testid="anti-ban-working-directory"]').element.value)
      .toBe("/Users/developer/project");
    expect(wrapper.get<HTMLInputElement>('[data-testid="anti-ban-os-version"]').element.value)
      .toBe("Darwin 24.5.0");
  });

  it("tolerates a legacy null additional directory list", async () => {
    const settings = response();
    (settings.environment.profile as { additional_working_directories: string[] | null })
      .additional_working_directories = null;
    mocks.get.mockResolvedValue(settings);
    const wrapper = mount(ClaudeAntiBanSettings, {
      global: { stubs: { Toggle: ToggleStub } },
    });
    await flushPromises();

    expect(wrapper.find("textarea").element.value).toBe("");
    expect(mocks.showError).not.toHaveBeenCalled();
  });

  it("saves probe and environment settings", async () => {
    const wrapper = mount(ClaudeAntiBanSettings, {
      global: { stubs: { Toggle: ToggleStub } },
    });
    await flushPromises();

    await wrapper.get('[data-testid="anti-ban-probe"]').setValue(false);
    await wrapper.get('[data-testid="anti-ban-mode"]').setValue("rewrite");
    await wrapper.get('[data-testid="anti-ban-save"]').trigger("click");
    await flushPromises();

    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({
      probe_interception_enabled: false,
      environment: expect.objectContaining({ mode: "rewrite" }),
    }));
  });
});
