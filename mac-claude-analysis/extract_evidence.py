#!/usr/bin/env python3
"""Extract reproducible Claude Code telemetry evidence from a macOS Mach-O.

The script only reads the selected executable. It does not launch Claude Code,
read Claude configuration, or make network requests.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import mmap
import re
import struct
import subprocess
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable


MH_MAGIC_64 = 0xFEEDFACF
LC_SEGMENT_64 = 0x19


@dataclass(frozen=True)
class SourceModule:
    start: int
    end: int
    data: bytes

    @property
    def size(self) -> int:
        return self.end - self.start

    @property
    def filename(self) -> str:
        return f"module-{self.start}.js"


ROLE_RULES: dict[str, tuple[bytes, ...]] = {
    "build_metadata": (
        b'VERSION:"',
        b"BUILD_TIME:",
        b"GIT_SHA:",
        b"DD_SOURCEMAP_GROUP:",
    ),
    "traffic_opt_out": (
        b"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
        b"DISABLE_TELEMETRY",
        b"DO_NOT_TRACK",
        b"essential-traffic",
        b"no-telemetry",
    ),
    "internal_event_gating": (
        b"function Kg()",
        b"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST",
        b"CLAUDE_CODE_CUSTOM_OAUTH_URL",
        b"po()!==null",
    ),
    "runtime_environment_schema": (
        b"CLAUDE_CODE_ENABLE_TELEMETRY",
        b"OTEL_LOG_USER_PROMPTS",
        b"OTEL_LOG_ASSISTANT_RESPONSES",
        b"OTEL_LOG_TOOL_CONTENT",
        b"OTEL_LOG_TOOL_DETAILS",
        b"BETA_TRACING_ENDPOINT",
    ),
    "internal_events": (
        b"transformLogsToEvents",
        b"/api/event_logging/v2/batch",
        b"GrowthbookExperimentEvent",
        b"ClaudeCodeInternalEvent",
        b"1p_failed_events",
        b"additional_metadata",
    ),
    "event_sampling_wrapper": (
        b"logEvent reentered while collecting metadata",
        b"sample_rate",
        b"l9n(e)",
        b"Vft(e,r)",
        b"zE(e,r)",
    ),
    "otel_configuration": (
        b"CLAUDE_CODE_ENABLE_TELEMETRY",
        b"OTEL_LOGS_EXPORTER",
        b"OTEL_TRACES_EXPORTER",
        b"BETA_TRACING_ENDPOINT",
        b"OTEL_EXPORTER_OTLP",
    ),
    "otel_log_records": (
        b"event.name",
        b"event.sequence",
        b"OTEL_LOG_USER_PROMPTS",
        b"OTEL_LOG_ASSISTANT_RESPONSES",
        b"<REDACTED>",
    ),
    "internal_metrics": (
        b"/api/claude_code/metrics",
        b"metrics_enabled",
        b"transformMetricsForInternal",
    ),
    "plugin_telemetry": (
        b"/api/event_logging/v2/batch",
        b"snake_case token",
        b"Choice",
    ),
    "api_event_call_sites": (
        b'tengu_api_success"',
        b'tengu_api_error"',
        b"clientRequestId",
        b"inputTokens",
        b"outputTokens",
        b"cache_creation",
    ),
}


ANCHORS = (
    b"VERSION:",
    b"BUILD_TIME:",
    b"GIT_SHA:",
    b"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
    b"DISABLE_TELEMETRY",
    b"DO_NOT_TRACK",
    b"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST",
    b"CLAUDE_CODE_CUSTOM_OAUTH_URL",
    b"function Kg()",
    b"tengu_frond_boric",
    b"tengu_event_sampling_config",
    b"tengu_1p_event_batch_config",
    b"logEvent reentered while collecting metadata",
    b"tengu_api_success",
    b"tengu_api_error",
    b"core_metadata",
    b"user_metadata",
    b"transformLogsToEvents",
    b"ClaudeCodeInternalEvent",
    b"GrowthbookExperimentEvent",
    b"additional_metadata",
    b"1p_failed_events",
    b"retryPreviousBatches",
    b"/api/event_logging/v2/batch",
    b"accepted_count",
    b"CLAUDE_CODE_ENABLE_TELEMETRY",
    b"OTEL_METRICS_EXPORTER",
    b"OTEL_LOGS_EXPORTER",
    b"OTEL_TRACES_EXPORTER",
    b"OTEL_LOG_USER_PROMPTS",
    b"OTEL_LOG_ASSISTANT_RESPONSES",
    b"OTEL_LOG_TOOL_CONTENT",
    b"OTEL_LOG_TOOL_DETAILS",
    b"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA",
    b"ENABLE_ENHANCED_TELEMETRY_BETA",
    b"BETA_TRACING_ENDPOINT",
    b"event.name",
    b"event.sequence",
    b"/api/claude_code/metrics",
    b"metrics_enabled",
    b"transformMetricsForInternal",
    b"snake_case token",
)


def sha256(data: bytes | mmap.mmap) -> str:
    return hashlib.sha256(data).hexdigest()


def c_string(raw: bytes) -> str:
    return raw.split(b"\0", 1)[0].decode("ascii", "replace")


def find_macho_section(data: mmap.mmap, segment: str, section: str) -> tuple[int, int, int]:
    if len(data) < 32:
        raise ValueError("File is too small to be a Mach-O executable")

    magic, cpu_type, _cpu_subtype, _file_type, ncmds, _sizeofcmds, _flags, _reserved = (
        struct.unpack_from("<IiiIIIII", data, 0)
    )
    if magic != MH_MAGIC_64:
        raise ValueError(f"Unsupported Mach-O magic: 0x{magic:08x}")

    command_offset = 32
    for _ in range(ncmds):
        command, command_size = struct.unpack_from("<II", data, command_offset)
        if command_size < 8:
            raise ValueError("Invalid Mach-O load command size")

        if command == LC_SEGMENT_64:
            (
                _command,
                _command_size,
                raw_segment_name,
                _vm_address,
                _vm_size,
                _file_offset,
                _file_size,
                _max_protection,
                _initial_protection,
                section_count,
                _segment_flags,
            ) = struct.unpack_from("<II16sQQQQiiII", data, command_offset)
            segment_name = c_string(raw_segment_name)
            section_offset = command_offset + 72
            for index in range(section_count):
                current = section_offset + index * 80
                raw_section_name, raw_parent_name = struct.unpack_from(
                    "<16s16s", data, current
                )
                section_name = c_string(raw_section_name)
                parent_name = c_string(raw_parent_name)
                if parent_name == segment and section_name == section:
                    _address, size, file_offset = struct.unpack_from(
                        "<QQI", data, current + 32
                    )
                    return file_offset, size, cpu_type

        command_offset += command_size

    raise ValueError(f"Mach-O section {segment},{section} was not found")


def discover_build_metadata(data: mmap.mmap) -> dict[str, str]:
    patterns = {
        "version": rb'VERSION:"([^"]+)"',
        "build_time": rb'BUILD_TIME:"([^"]+)"',
        "git_sha": rb'GIT_SHA:"([0-9a-f]{40})"',
        "source_map_group": rb'DD_SOURCEMAP_GROUP:"([^"]+)"',
    }
    result: dict[str, str] = {}
    for key, pattern in patterns.items():
        match = re.search(pattern, data)
        if match:
            result[key] = match.group(1).decode("ascii")
    if "version" not in result:
        raise ValueError("Could not discover the embedded Claude Code version")
    return result


def discover_source_modules(
    data: mmap.mmap, section_start: int, section_size: int, version: str
) -> list[SourceModule]:
    marker = f"// Version: {version}".encode()
    section_end = section_start + section_size
    position = section_start
    ranges: set[tuple[int, int]] = set()

    while True:
        match = data.find(marker, position, section_end)
        if match < 0:
            break
        start = data.rfind(b"\0", section_start, match) + 1
        end = data.find(b"\0", match, section_end)
        if end < 0:
            end = section_end
        ranges.add((start, end))
        position = match + len(marker)

    return [
        SourceModule(start=start, end=end, data=bytes(data[start:end]))
        for start, end in sorted(ranges)
    ]


def find_all(haystack: bytes | mmap.mmap, needle: bytes) -> Iterable[int]:
    position = 0
    while True:
        match = haystack.find(needle, position)
        if match < 0:
            return
        yield match
        position = match + len(needle)


def select_modules(modules: list[SourceModule]) -> dict[SourceModule, list[str]]:
    selected: dict[SourceModule, list[str]] = {}
    for role, needles in ROLE_RULES.items():
        matches = [
            module
            for module in modules
            if all(needle in module.data for needle in needles)
        ]
        if not matches:
            continue

        # Build constants are inlined into many modules. The smallest matching
        # module is sufficient evidence for their values; retain every match for
        # behavioral roles because duplicate entry points can be meaningful.
        if role == "build_metadata":
            matches = [min(matches, key=lambda module: (module.size, module.start))]

        for module in matches:
            selected.setdefault(module, []).append(role)
    return selected


def read_codesign_identity(binary: Path) -> dict[str, str]:
    try:
        result = subprocess.run(
            ["codesign", "-dv", "--verbose=4", str(binary)],
            check=False,
            capture_output=True,
            text=True,
        )
    except FileNotFoundError:
        return {}

    parsed: dict[str, str] = {}
    authorities: list[str] = []
    for line in (result.stdout + result.stderr).splitlines():
        if line.startswith("Authority="):
            authorities.append(line.partition("=")[2])
        elif "=" in line:
            key, _, value = line.partition("=")
            if key in {
                "Identifier",
                "TeamIdentifier",
                "CDHash",
                "Timestamp",
                "Runtime Version",
            }:
                parsed[key] = value
    if authorities:
        parsed["Authorities"] = " | ".join(authorities)
    verification = subprocess.run(
        ["codesign", "--verify", "--strict", "--verbose=4", str(binary)],
        check=False,
        capture_output=True,
        text=True,
    )
    parsed["Verification"] = (
        "valid on disk; satisfies designated requirement"
        if verification.returncode == 0
        else "verification failed"
    )
    return parsed


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()

    binary = args.binary.expanduser().resolve()
    output = args.output.expanduser().resolve()
    module_directory = output / "evidence" / "modules"
    module_directory.mkdir(parents=True, exist_ok=True)
    for stale_module in module_directory.glob("module-*.js"):
        stale_module.unlink()

    with binary.open("rb") as binary_file, mmap.mmap(
        binary_file.fileno(), 0, access=mmap.ACCESS_READ
    ) as data:
        section_start, section_size, cpu_type = find_macho_section(
            data, "__BUN", "__bun"
        )
        build = discover_build_metadata(data)
        modules = discover_source_modules(
            data, section_start, section_size, build["version"]
        )
        selected = select_modules(modules)

        manifest_modules: list[dict[str, object]] = []
        index_entries: list[dict[str, object]] = []
        for module, roles in sorted(selected.items(), key=lambda item: item[0].start):
            destination = module_directory / module.filename
            destination.write_bytes(module.data)
            manifest_modules.append(
                {
                    "file": str(destination.relative_to(output)),
                    "file_offset_start": module.start,
                    "file_offset_end": module.end,
                    "length": module.size,
                    "sha256": sha256(module.data),
                    "roles": sorted(roles),
                }
            )
            for anchor in ANCHORS:
                for module_offset in find_all(module.data, anchor):
                    index_entries.append(
                        {
                            "anchor": anchor.decode("ascii"),
                            "module": str(destination.relative_to(output)),
                            "module_offset": module_offset,
                            "file_offset": module.start + module_offset,
                        }
                    )

        manifest = {
            "analysis": {
                "method": "Static extraction of NUL-delimited UTF-8 modules from Mach-O __BUN,__bun; executable not launched",
                "generated_at": datetime.now(timezone.utc).isoformat(),
            },
            "binary": {
                "path": str(binary),
                "size": len(data),
                "sha256": sha256(data),
                "cpu_type": cpu_type,
                "architecture": "arm64" if cpu_type == 0x0100000C else "unknown",
                "code_signature": read_codesign_identity(binary),
                **build,
            },
            "container": {
                "format": "Mach-O 64-bit",
                "source_section": "__BUN,__bun",
                "section_file_offset": section_start,
                "section_size": section_size,
                "source_module_count": len(modules),
            },
            "whole_binary_searches": {
                "accepted_count": len(list(find_all(data, b"accepted_count"))),
                "empty_log_success": len(
                    list(find_all(data, b"logSuccess(e,n,r){}"))
                ),
            },
            "modules": manifest_modules,
        }
        evidence_index = {
            "binary_sha256": manifest["binary"]["sha256"],
            "entries": sorted(
                index_entries,
                key=lambda entry: (
                    entry["file_offset"],
                    entry["anchor"],
                ),
            ),
        }

    output.mkdir(parents=True, exist_ok=True)
    (output / "evidence-manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    (output / "evidence-index.json").write_text(
        json.dumps(evidence_index, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )

    print(
        f"Extracted {len(manifest_modules)} modules and "
        f"{len(index_entries)} anchor occurrences from {binary}"
    )


if __name__ == "__main__":
    main()
