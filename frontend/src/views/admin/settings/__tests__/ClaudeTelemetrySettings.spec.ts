import { beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import ClaudeTelemetrySettings from "../ClaudeTelemetrySettings.vue";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock("@/api", () => ({
  adminAPI: {
    settings: {
      getClaudeTelemetrySettings: mocks.get,
      updateClaudeTelemetrySettings: mocks.update,
    },
  },
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
  profile: "2.1.284",
  mode: "record" as const,
  setup_token_enabled: false,
  record_file: "./data/telemetry/telemetry-batches.ndjson",
  first_party: {
    enabled: true,
    url: "https://api.anthropic.com/api/event_logging/v2/batch",
  },
  datadog: {
    enabled: true,
    url: "https://http-intake.logs.us5.datadoghq.com/api/v2/logs",
    api_key_configured: true,
  },
  batch: { delay_ms: 10_000, size: 400, queue_size: 8192 },
  timeout_ms: 15_000,
  retry: { max_attempts: 3, base_delay_ms: 500, max_delay_ms: 10_000 },
  sampling: { default_rate: 1, event_rates: {} },
  flush_on_exit: false,
  runtime: {
    available: false,
    profile: "2.1.284",
    queue_length: 0,
    queue_capacity: 8192,
    dropped_events: 0,
    last_flush_at: null,
    first_party: { successes: 0, failures: 0, last_success_at: null, last_error: "" },
    datadog: { successes: 0, failures: 0, last_success_at: null, last_error: "" },
  },
});

function mountComponent() {
  return mount(ClaudeTelemetrySettings, {
    global: { stubs: { Toggle: ToggleStub } },
  });
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  mocks.get.mockResolvedValue(response());
  mocks.update.mockImplementation(async (payload) => ({
    ...response(),
    ...payload,
    datadog: {
      ...payload.datadog,
      api_key: undefined,
      api_key_configured: !payload.datadog.clear_api_key,
      clear_api_key: undefined,
    },
  }));
});

describe("ClaudeTelemetrySettings", () => {
  it("shows the fixed profile and never renders a configured secret", async () => {
    const wrapper = mountComponent();
    await flushPromises();

    expect(wrapper.text()).toContain("2.1.284");
    expect(wrapper.text()).toContain("admin.settings.claudeTelemetry.scopeHint");
    const key = wrapper.get<HTMLInputElement>('[data-testid="claude-telemetry-datadog-key"]');
    expect(key.element.value).toBe("");
    expect(key.attributes("placeholder")).toContain("keyKeepPlaceholder");
    expect(wrapper.html()).not.toContain("dd-secret");
  });

  it("submits an empty key as preserve semantics", async () => {
    const wrapper = mountComponent();
    await flushPromises();

    await wrapper.get('[data-testid="claude-telemetry-setup-token"]').setValue(true);
    await wrapper.get('[data-testid="claude-telemetry-save"]').trigger("click");
    await flushPromises();

    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({
      setup_token_enabled: true,
      datadog: expect.objectContaining({
        api_key: "",
        clear_api_key: false,
      }),
    }));
  });

  it("only clears the key with the explicit clear flag", async () => {
    const wrapper = mountComponent();
    await flushPromises();

    await wrapper.get('[data-testid="claude-telemetry-datadog-enabled"]').setValue(false);
    await wrapper.get('[data-testid="claude-telemetry-clear-key"]').setValue(true);
    await wrapper.get('[data-testid="claude-telemetry-save"]').trigger("click");
    await flushPromises();

    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({
      datadog: expect.objectContaining({
        enabled: false,
        api_key: "",
        clear_api_key: true,
      }),
    }));
  });

  it("rejects invalid event sampling before calling the API", async () => {
    const wrapper = mountComponent();
    await flushPromises();

    await wrapper.get('[data-testid="claude-telemetry-event-rates"]').setValue(
      '{"tengu_api_success": 2}',
    );
    await wrapper.get('[data-testid="claude-telemetry-save"]').trigger("click");
    await flushPromises();

    expect(mocks.update).not.toHaveBeenCalled();
    expect(mocks.showError).toHaveBeenCalledWith(
      "admin.settings.claudeTelemetry.invalidSampling",
    );
  });
});
