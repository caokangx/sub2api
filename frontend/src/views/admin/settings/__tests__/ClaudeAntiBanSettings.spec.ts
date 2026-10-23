import { beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import GroupSelector from "@/components/common/GroupSelector.vue";
import ClaudeAntiBanSettings from "../ClaudeAntiBanSettings.vue";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  getGroups: vi.fn(),
  updateGroup: vi.fn(),
}));

vi.mock("@/api/admin/groups", () => ({
  getAllIncludingInactive: mocks.getGroups,
  update: mocks.updateGroup,
}));

vi.mock("@/api/admin/settings", () => ({
  getClaudeAntiBanSettings: mocks.get,
  updateClaudeAntiBanSettings: mocks.update,
}));

vi.mock("@/stores", () => ({
  useAuthStore: () => ({ isSimpleMode: false }),
  useAppStore: () => ({
    showError: mocks.showError,
    showSuccess: mocks.showSuccess,
  }),
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }),
}));

vi.mock("vue-i18n", async () => ({
  ...await vi.importActual<typeof import("vue-i18n")>("vue-i18n"),
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

const mountSettings = () => mount(ClaudeAntiBanSettings, {
  global: { stubs: { Toggle: ToggleStub, GroupBadge: true, Icon: true } },
});

const groups = () => [
  { id: 1, name: "Primary", platform: "anthropic", status: "active", force_claude_code_profile: true },
  { id: 2, name: "Secondary", platform: "anthropic", status: "active", force_claude_code_profile: false },
  { id: 3, name: "Inactive", platform: "anthropic", status: "inactive", force_claude_code_profile: true },
  { id: 4, name: "OpenAI", platform: "openai", status: "active", force_claude_code_profile: false },
  { id: 5, name: "Composite", platform: "composite", status: "active", force_claude_code_profile: false },
];

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
  mocks.getGroups.mockResolvedValue(groups());
  mocks.updateGroup.mockImplementation(async (id, payload) => ({ ...groups().find((group) => group.id === id), ...payload }));
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
    expect(mocks.updateGroup).not.toHaveBeenCalled();
  });

  it("loads existing prompt override selections including inactive Anthropic groups", async () => {
    const wrapper = mountSettings();
    await flushPromises();
    const selector = wrapper.getComponent(GroupSelector);
    expect(selector.props("groups").map((group) => group.id)).toEqual([1, 2, 3]);
    expect(selector.props("modelValue")).toEqual([1, 3]);
    expect(wrapper.get<HTMLButtonElement>('[data-testid="prompt-groups-save"]').element.disabled).toBe(true);
    expect(wrapper.text()).toContain("admin.settings.claudeAntiBan.promptOverrideSystem");
    expect(wrapper.text()).toContain("admin.settings.claudeAntiBan.promptOverrideTools");
  });

  it("saves only changed group policies through the existing group endpoint", async () => {
    const wrapper = mountSettings();
    await flushPromises();
    wrapper.getComponent(GroupSelector).vm.$emit("update:modelValue", [2, 3]);
    await wrapper.vm.$nextTick();
    await wrapper.get('[data-testid="prompt-groups-save"]').trigger("click");
    await flushPromises();
    expect(mocks.updateGroup).toHaveBeenCalledTimes(2);
    expect(mocks.updateGroup).toHaveBeenCalledWith(1, { force_claude_code_profile: false });
    expect(mocks.updateGroup).toHaveBeenCalledWith(2, { force_claude_code_profile: true });
    expect(mocks.update).not.toHaveBeenCalled();
    expect(wrapper.get<HTMLButtonElement>('[data-testid="prompt-groups-save"]').element.disabled).toBe(true);
  });

  it("allows an empty selection to disable all previously enabled groups", async () => {
    const wrapper = mountSettings();
    await flushPromises();
    wrapper.getComponent(GroupSelector).vm.$emit("update:modelValue", []);
    await wrapper.vm.$nextTick();
    await wrapper.get('[data-testid="prompt-groups-save"]').trigger("click");
    await flushPromises();
    expect(mocks.updateGroup).toHaveBeenCalledTimes(2);
    expect(mocks.updateGroup).toHaveBeenCalledWith(1, { force_claude_code_profile: false });
    expect(mocks.updateGroup).toHaveBeenCalledWith(3, { force_claude_code_profile: false });
  });

  it("blocks group saves after a load error and supports retry", async () => {
    mocks.getGroups.mockRejectedValueOnce(new Error("unavailable"));
    const wrapper = mountSettings();
    await flushPromises();
    expect(wrapper.findComponent(GroupSelector).exists()).toBe(false);
    expect(wrapper.get<HTMLButtonElement>('[data-testid="prompt-groups-save"]').element.disabled).toBe(true);
    expect(mocks.updateGroup).not.toHaveBeenCalled();
    await wrapper.get('[data-testid="prompt-groups-retry"]').trigger("click");
    await flushPromises();
    expect(wrapper.getComponent(GroupSelector).props("modelValue")).toEqual([1, 3]);
  });

  it("retains failed changes for retry without resending successful group updates", async () => {
    mocks.updateGroup.mockImplementation(async (id, payload) => {
      if (id === 2) throw new Error("failed");
      return { ...groups().find((group) => group.id === id), ...payload };
    });
    const wrapper = mountSettings();
    await flushPromises();
    wrapper.getComponent(GroupSelector).vm.$emit("update:modelValue", [2, 3]);
    await wrapper.vm.$nextTick();
    await wrapper.get('[data-testid="prompt-groups-save"]').trigger("click");
    await flushPromises();
    expect(mocks.showError).toHaveBeenCalled();
    expect(mocks.showSuccess).not.toHaveBeenCalled();
    expect(wrapper.get<HTMLButtonElement>('[data-testid="prompt-groups-save"]').element.disabled).toBe(false);
    mocks.updateGroup.mockClear();
    mocks.updateGroup.mockResolvedValueOnce({ ...groups()[1], force_claude_code_profile: true });
    await wrapper.get('[data-testid="prompt-groups-save"]').trigger("click");
    await flushPromises();
    expect(mocks.updateGroup).toHaveBeenCalledTimes(1);
    expect(mocks.updateGroup).toHaveBeenCalledWith(2, { force_claude_code_profile: true });
    expect(wrapper.get<HTMLButtonElement>('[data-testid="prompt-groups-save"]').element.disabled).toBe(true);
  });
});
