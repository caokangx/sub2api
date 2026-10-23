#!/usr/bin/env python3
"""Persistent, local-only OAuth body replay through the real gateway."""
import argparse
import collections
import datetime
import difflib
import fcntl
import base64
import hashlib
import hmac
import json
import os
import random
import re
import secrets
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

from common import encoded, reconstruct, redact, sha, walk, write_json

REPO = Path(__file__).resolve().parents[2]
ROOT = REPO / "test-results/claude-profile-replay"
STATE = ROOT / "channel.json"
KEY = ROOT / "api-key.secret"
MANIFEST = ROOT / "manifest.json"
GATEWAY = "http://127.0.0.1:8080"
UPSTREAM = "http://claude-profile-mock:8090"
UUID = "11111111-2222-4333-8444-555555555555"
NAME = "Claude 复写回放测试"
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def http(url, data=None, headers=None, method=None, timeout=120):
    req = urllib.request.Request(url, None if data is None else encoded(data), headers or {}, method=method)
    try:
        with OPENER.open(req, timeout=timeout) as response:
            return response.status, response.read(), dict(response.headers)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read(), dict(exc.headers)


def control(path, data=None):
    # Docker internal networks deliberately have no host port publishing.
    # Control travels through docker exec to container loopback; messages still
    # travel exclusively through the actual gateway's HTTP upstream client.
    script = ("import json,sys,urllib.request; raw=sys.stdin.buffer.read(); "
              "req=urllib.request.Request('http://127.0.0.1:8091'+sys.argv[1], "
              "raw or None, {'Content-Type':'application/json'}); "
              "r=urllib.request.urlopen(req,timeout=10); sys.stdout.buffer.write(r.read())")
    result = subprocess.run(["docker", "exec", "-i", "claude-profile-mock", "python", "-c", script, path],
                            input=b"" if data is None else encoded(data), capture_output=True, timeout=20)
    if result.returncode:
        raise RuntimeError("mock control unavailable/busy; inspect docker logs; no gateway request sent")
    return json.loads(result.stdout)


def local_session():
    """Explicit development-only bootstrap using the existing persisted signer."""
    import time
    query = ("SELECT json_build_object('email',u.email,'password_hash',u.password_hash,'role',u.role,"
             "'secret',s.value)::text FROM users u CROSS JOIN security_secrets s "
             "WHERE u.id=1 AND u.deleted_at IS NULL AND s.key='jwt_secret'")
    result = subprocess.run(["docker", "exec", "sub2api-postgres-dev", "psql", "-U", "sub2api", "-d", "sub2api", "-At", "-c", query],
                            capture_output=True, check=True, timeout=20)
    user = json.loads(result.stdout)
    if user["role"] != "admin":
        raise ValueError("local ID 1 is not an administrator")
    material = user["email"].strip().lower() + "\n" + user["password_hash"]
    version = int.from_bytes(hashlib.sha256(material.encode()).digest()[:8], "big") & 0x7fffffffffffffff
    now = int(time.time())
    claims = {"user_id": 1, "email": user["email"], "role": "admin", "token_version": version,
              "sid": secrets.token_hex(8), "iat": now, "nbf": now - 30, "exp": now + 7200}
    b64 = lambda value: base64.urlsafe_b64encode(value).rstrip(b"=")
    signed = b64(encoded({"alg": "HS256", "typ": "JWT"})) + b"." + b64(encoded(claims))
    token = signed + b"." + b64(hmac.new(user["secret"].strip().encode(), signed, hashlib.sha256).digest())
    path = ROOT / "admin-session.secret"
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, "wb") as output:
        output.write(token + b"\n")
    Admin()  # Validate through the existing API before reporting success.
    print("Saved local admin ID 1 session (0600, expires in two hours); credentials and settings unchanged.")


def client_headers(body):
    return {"x-api-key": KEY.read_text().strip(), "Content-Type": "application/json",
            "anthropic-version": "2023-06-01", "User-Agent": "claude-profile-replay/1.0",
            "Host": "127.0.0.1:8080", "Content-Length": str(len(encoded(body))),
            "Accept-Encoding": "identity", "Connection": "close"}


class Admin:
    def __init__(self):
        token_file = os.environ.get("CLAUDE_REPLAY_ADMIN_TOKEN_FILE")
        if not token_file and (ROOT / "admin-session.secret").exists():
            token_file = str(ROOT / "admin-session.secret")
        if token_file:
            self.token = Path(token_file).read_text().strip()
        else:
            env = {}
            for line in (REPO / "deploy/.env").read_text().splitlines():
                if "=" in line and not line.lstrip().startswith("#"):
                    k, v = line.split("=", 1)
                    env[k.strip()] = v.strip().strip("\"'")
            result = self.call("/auth/login", {"email": env["ADMIN_EMAIL"], "password": env["ADMIN_PASSWORD"]})
            self.token = result["access_token"]
        me = self.call("/auth/me")
        if me.get("id") != 1 or me.get("role") != "admin":
            raise ValueError("initialization requires existing local admin ID 1")

    def call(self, path, data=None, method=None):
        headers = {"Content-Type": "application/json"}
        if hasattr(self, "token"):
            headers["Authorization"] = "Bearer " + self.token
        status, raw, _ = http(GATEWAY + "/api/v1" + path, data, headers, method)
        result = json.loads(raw)
        if status // 100 != 2 or result.get("code", 0) != 0:
            # Never echo request bodies, credentials or returned secrets on errors.
            raise RuntimeError(f"management API {path}: HTTP {status}: {result.get('message', 'failed')}")
        return result.get("data", result)


def items(value):
    return value if isinstance(value, list) else value.get("items", [])


def initialize(admin):
    state = json.loads(STATE.read_text()) if STATE.exists() else {}
    if not state.get("group_id"):
        matches = [g for g in items(admin.call("/admin/groups?page_size=100")) if g["name"] == NAME]
        if len(matches) > 1:
            raise ValueError("ambiguous existing test groups")
        group = matches[0] if matches else admin.call("/admin/groups", {
            "name": NAME, "platform": "anthropic", "rate_multiplier": 0,
            "is_exclusive": True, "subscription_type": "standard", "force_claude_code_profile": True,
            "fallback_group_id": None, "fallback_group_id_on_invalid_request": None,
            "description": "Local record-only mock OAuth replay. Fake credentials. No fallback."})
        state["group_id"] = group["id"]
        write_json(STATE, state)
    if not state.get("account_id"):
        matches = [a for a in items(admin.call("/admin/accounts?page_size=100&search=profile-replay-mock-oauth"))
                   if a["name"] == "profile-replay-mock-oauth"]
        if len(matches) > 1:
            raise ValueError("ambiguous existing test accounts")
        account = matches[0] if matches else admin.call("/admin/accounts", {
            "name": "profile-replay-mock-oauth", "platform": "anthropic", "type": "oauth",
            "credentials": {"access_token": "sk-ant-oat01-LOCAL-MOCK-ONLY-NOT-A-REAL-TOKEN",
                            "account_uuid": UUID, "expires_at": "2099-01-01T00:00:00Z"},
            "extra": {"synthetic_ui_test": True, "custom_base_url_enabled": True,
                      "custom_base_url": UPSTREAM, "claude_user_id": "a" * 64},
            "proxy_id": None, "concurrency": 1, "rate_multiplier": 0,
            "group_ids": [state["group_id"]], "upstream_billing_probe_enabled": False})
        state["account_id"] = account["id"]
        write_json(STATE, state)
    if not KEY.exists():
        if state.get("key_id"):
            raise ValueError("saved key file missing; refusing silent key replacement")
        fd = os.open(KEY, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write("sk-local-profile-replay-" + secrets.token_hex(24) + "\n")
    if not state.get("key_id"):
        user = admin.call("/admin/users/1")
        allowed = user.get("allowed_groups", []) or []
        if state["group_id"] not in allowed:
            admin.call("/admin/users/1", {"allowed_groups": allowed + [state["group_id"]]}, "PUT")
        matches = [k for k in items(admin.call("/keys?page_size=100")) if k["name"] == NAME]
        if len(matches) > 1:
            raise ValueError("ambiguous existing replay keys")
        key = matches[0] if matches else admin.call("/keys", {
            "name": NAME, "group_id": state["group_id"], "custom_key": KEY.read_text().strip()})
        state["key_id"] = key["id"]
        write_json(STATE, state)
    verify_channel(admin)
    return state


def verify_channel(admin):
    state = json.loads(STATE.read_text())
    group = admin.call(f"/admin/groups/{state['group_id']}")
    account = admin.call(f"/admin/accounts/{state['account_id']}")
    # Normal account DTOs omit access_token. Export exactly this synthetic
    # account in memory so a real token or refresh credential cannot pass checks.
    exported = admin.call(f"/admin/accounts/data?ids={state['account_id']}&include_proxies=false")
    exported_accounts = exported.get("accounts", [])
    key = admin.call(f"/keys/{state['key_id']}")
    members = admin.call(f"/admin/accounts?group={state['group_id']}&page_size=100")
    checks = {
        "group": group.get("name") == NAME and group.get("platform") == "anthropic",
        "exclusive_force": group.get("is_exclusive") and group.get("force_claude_code_profile"),
        "fallbacks_empty": all(group.get(k) is None for k in ("fallback_group_id", "fallback_group_id_on_invalid_request")),
        "only_mock_member": [a["id"] for a in items(members)] == [state["account_id"]] and members.get("total", 1) == 1,
        "oauth": account.get("platform") == "anthropic" and account.get("type") == "oauth",
        "fake_credentials": len(exported_accounts) == 1 and exported_accounts[0].get("credentials") == {
            "access_token": "sk-ant-oat01-LOCAL-MOCK-ONLY-NOT-A-REAL-TOKEN", "account_uuid": UUID,
            "expires_at": "2099-01-01T00:00:00Z"},
        "synthetic": account.get("extra", {}).get("synthetic_ui_test") is True,
        "upstream": account.get("extra", {}).get("custom_base_url_enabled") is True and
                    account.get("extra", {}).get("custom_base_url") == UPSTREAM,
        "no_proxy": account.get("proxy_id") is None,
        "only_test_group": account.get("group_ids") == [state["group_id"]],
        "zero_rates": group.get("rate_multiplier") == 0 and account.get("rate_multiplier") == 0,
        "no_probe": not account.get("extra", {}).get("upstream_billing_probe_enabled", False),
        "key_owner_group": key.get("user_id") == 1 and key.get("group_id") == state["group_id"],
        "key_matches": key.get("key") == KEY.read_text().strip(),
        "key_permissions": KEY.stat().st_mode & 0o777 == 0o600,
        "active": group.get("status") == "active" and account.get("status") == "active" and key.get("status") == "active",
    }
    if not all(checks.values()):
        raise ValueError("channel drift: " + ", ".join(k for k, v in checks.items() if not v))
    return {"resources": state, "checks": checks,
            "account": {"id": account["id"], "type": account["type"], "extra": account["extra"]}, "group": group}


def select_sources(sources, count, seed):
    if MANIFEST.exists():
        existing = json.loads(MANIFEST.read_text())
        print(f"Reusing manifest: {len(existing['cases'])} cases")
        return
    selected, rejected, populations = [], [], {}
    rng = random.Random(seed)
    for source in sources:
        candidates = []
        for path in sorted(source.glob("*.hjson")):
            try:
                _body, notes, features = reconstruct(path)
                candidates.append({"source": str(path.resolve()), "source_group": source.name,
                                   "restoration": notes, "features": features})
            except Exception as exc:
                rejected.append({"source": str(path), "sha256": sha(path.read_bytes()), "reason": str(exc)})
        populations[source.name] = {"valid": len(candidates), "rejected": sum(Path(r['source']).parent == source for r in rejected)}
        if len(candidates) < count:
            write_json(ROOT / "selection-rejections.json", rejected)
            raise ValueError(f"{source}: only {len(candidates)} reconstructable records; need {count}")
        # Greedy coverage of marginal strata with diminishing returns. Seeded
        # shuffle breaks ties; session and system/tools diversity count equally.
        rng.shuffle(candidates)
        seen = collections.Counter()
        for _ in range(count):
            choice = max(candidates, key=lambda c: sum(1 / (1 + seen[(k, str(v))]) for k, v in c["features"].items()))
            candidates.remove(choice)
            seen.update((k, str(v)) for k, v in choice["features"].items())
            choice["case_id"] = f"case-{len(selected)+1:03d}"
            selected.append(choice)
    write_json(ROOT / "selection-rejections.json", rejected)
    write_json(MANIFEST, {"seed": seed, "per_source": count, "population": populations, "cases": selected,
                          "selection": "seeded greedy marginal coverage across all saved features"})
    print(json.dumps({"selected": len(selected), "population": populations}, ensure_ascii=False))


def snapshot(admin, directory):
    root = REPO / "backend/internal/service"
    source = (root / "gateway_claude_code_profile.go").read_text()
    version = re.search(r"claude_code_profile/([^/]+)/system.txt", source)[1]
    resources = root / "claude_code_profile" / version
    constants = (REPO / "backend/internal/pkg/claude/constants.go").read_text()
    expected = {"profile_version": version, "cli_version": re.search(r'CLICurrentVersion = "([^"]+)"', constants)[1],
                "settings": admin.call("/admin/settings/claude-anti-ban"),
                "required_betas": [re.search(k + r'\s*= "([^"]+)"', constants)[1] for k in ("BetaOAuth", "BetaClaudeCode")],
                "system": (resources / "system.txt").read_text(),
                "tools": json.loads((resources / "tools.json").read_text()),
                "output_budget_source": (REPO / "backend/internal/pkg/claude/model_capabilities.go").read_text()}
    telemetry = admin.call("/admin/settings/claude-telemetry")
    write_json(directory / "telemetry-before.json", {"mode": telemetry["mode"], "runtime": telemetry["runtime"]})
    expected["telemetry_mode"] = telemetry["mode"]
    write_json(directory / "provenance.json", {
        "binary_sha256": sha((ROOT / "runtime/sub2api").read_bytes()),
        "profile_system_sha256": sha((resources / "system.txt").read_bytes()),
        "profile_tools_sha256": sha((resources / "tools.json").read_bytes()),
        "replay_tool_sha256": sha(Path(__file__).read_bytes()),
        "mock_sha256": sha((Path(__file__).parent / "mock.py").read_bytes())})
    write_json(directory / "expected.json", expected)
    return expected


def strip_cache(value):
    if isinstance(value, dict):
        return {k: strip_cache(v) for k, v in value.items() if k != "cache_control"}
    if isinstance(value, list):
        return [strip_cache(v) for v in value]
    return value


def field_diff(before, after, path="$"):
    if isinstance(before, dict) and isinstance(after, dict):
        changes = []
        for key in sorted(before.keys() | after.keys()):
            p = path + "." + key
            if key not in before:
                changes.append({"path": p, "kind": "added", "after": after[key]})
            elif key not in after:
                changes.append({"path": p, "kind": "removed", "before": before[key]})
            else:
                changes.extend(field_diff(before[key], after[key], p))
        return changes
    if isinstance(before, list) and isinstance(after, list) and len(before) == len(after):
        return [d for i, (a, b) in enumerate(zip(before, after)) for d in field_diff(a, b, path + f"[{i}]")]
    return [] if before == after else [{"path": path, "kind": "changed", "before": before, "after": after}]


def validate_wire(before, after, headers, expected):
    profile = expected["settings"]["environment"]["profile"]
    system = after.get("system", [])
    texts = [b.get("text", "") for b in system] if isinstance(system, list) else []
    fable = "fable" in after.get("model", "")
    wanted = expected["system"].strip().replace("{{MEMORY_DIRECTORY}}", profile["memory_directory"])
    h = {k.lower(): v for k, v in headers.items()}
    try:
        identity = json.loads(after.get("metadata", {}).get("user_id", ""))
    except (ValueError, TypeError):
        identity = {}
    budgets = expected["output_budget_source"].split("func OutputBudget", 1)[1].split("func SupportsThinking", 1)[0]
    default, upper = (32000, 128000)
    for models, lo, hi in re.findall(r"case (.*?):\s*return (\d+), (\d+)", budgets):
        if after.get("model") in re.findall(r'"([^"]+)"', models):
            default, upper = int(lo), int(hi)
            break
    thinking = after.get("thinking", {})
    budget = thinking.get("budget_tokens", 0) if thinking.get("type") == "enabled" else 0
    # Historical tool names and IDs are independent of replacement declarations.
    def history(body):
        result = []
        for msg in body.get("messages", []):
            result.append({"role": msg.get("role"), "control": {k: v for k, v in msg.items() if k not in {"content", "role"}},
                           "tools": [{k: n.get(k) for k in ("type", "id", "name", "tool_use_id", "input", "is_error") if k in n}
                                     for n in walk(msg.get("content")) if n.get("type") in ("tool_use", "tool_result")]})
        return result
    checks = {
        "system_profile": fable and len(texts) == 2 or not fable and len(texts) == 4 and texts[2] == wanted,
        "system_identity": len(texts) >= 2 and "Claude Code" in texts[1],
        "billing_version": bool(texts) and "cc_version=" + expected["cli_version"] in texts[0],
        "environment": fable or len(texts) == 4 and all(str(profile[k]) in texts[3] for k in ("working_directory", "platform", "shell", "os_version")),
        "tools_profile": strip_cache(after.get("tools")) == strip_cache(expected["tools"]),
        "tool_choice_removed": "tool_choice" not in after,
        "metadata_identity": identity.get("account_uuid") == UUID and identity.get("device_id") == "a" * 64 and bool(identity.get("session_id")),
        "ua": h.get("user-agent") == "claude-cli/" + expected["cli_version"] + " (external, cli)",
        "beta": set(expected["required_betas"]).issubset(h.get("anthropic-beta", "").split(",")),
        "output_budget": after.get("max_tokens") == max(default, budget + 1) and after.get("max_tokens", 0) <= upper,
        "cache_limit": sum("cache_control" in n for n in walk(after)) <= 4,
        "history_order_names_ids_controls": history(before) == history(after),
        # normalizeClaudeMessagesBody defaults an omitted stream to true.
        # Explicit false must still be preserved.
        "stream_policy": before.get("stream", True) == after.get("stream"),
        "images_preserved": [strip_cache(n) for n in walk(before.get("messages")) if n.get("type") == "image"] ==
                            [strip_cache(n) for n in walk(after.get("messages")) if n.get("type") == "image"],
        "authorization_redacted": h.get("authorization") == "[REDACTED]" and "x-api-key" not in h,
    }
    return checks


def verify_run(directory):
    expected = json.loads((directory / "expected.json").read_text())
    results = []
    manifest = json.loads((directory / "manifest.json").read_text())
    for case in manifest["cases"]:
        case_dir = directory / "cases" / case["case_id"]
        if not (case_dir / "response.json").exists():
            result = {"case_id": case_dir.name, "passed": False, "attempts": [],
                      "failure": "run interrupted before receiving/saving a gateway response"}
            write_json(case_dir / "verification.json", result)
            results.append(result)
            continue
        response = json.loads((case_dir / "response.json").read_text())
        captures = sorted((case_dir / "mock").glob("*.request.json"))
        result = {"case_id": case_dir.name, "http_status": response["status"], "attempts": [], "failure": response.get("error")}
        before = json.loads((case_dir / "client.request.json").read_text())
        response_data = (case_dir / "response.txt").read_text()
        try:
            if before.get("stream", True):
                events = [json.loads(line[6:]) for line in response_data.splitlines() if line.startswith("data: ")]
                response_ok = bool(events) and events[-1].get("type") == "message_stop" and not any(e.get("type") == "error" for e in events)
            else:
                message = json.loads(response_data)
                response_ok = message.get("type") == "message" and message.get("id") == "msg_local_profile_mock"
        except (ValueError, TypeError):
            response_ok = False
        result["response_complete"] = response_ok
        for capture in captures:
            after = json.loads(capture.read_text())
            prefix = capture.name.removesuffix(".request.json")
            metadata = json.loads(capture.with_name(prefix + ".headers.json").read_text())
            checks = validate_wire(before, after, metadata["headers"], expected)
            checks["correlation"] = metadata["run_id"] == directory.name and metadata["case_id"] == case_dir.name
            checks["capture_complete"] = capture.with_name(prefix + ".complete.json").exists()
            changes = field_diff(before, after)
            write_json(case_dir / (prefix + ".fields.diff.json"), changes)
            (case_dir / (prefix + ".body.diff.txt")).write_text("".join(difflib.unified_diff(
                encoded(before).decode().splitlines(True), encoded(after).decode().splitlines(True),
                fromfile="client.request.json", tofile="mock.request.json")))
            result["attempts"].append({"capture": str(capture.relative_to(directory)), "checks": checks,
                                       "changed_paths": [d["path"] for d in changes],
                                       "history_changes": [d["path"] for d in changes if d["path"].startswith("$.messages")]})
        result["passed"] = bool(captures) and response["status"] == 200 and response_ok and all(
            all(a["checks"].values()) for a in result["attempts"])
        if not captures:
            if response["status"] == 400:
                try:
                    result["failure"] = "gateway rejected before mock: " + json.loads(response_data)["error"]["message"]
                except (ValueError, KeyError, TypeError):
                    result["failure"] = "HTTP 400 before mock; see response.txt"
            elif response["status"] == 200 and "Hi! How can I help you today?" in response_data and expected["settings"].get("probe_interception_enabled"):
                result["failure"] = "local probe interception (matches enabled interceptor's fixed greeting); no mock evidence, not a rewrite pass"
            else:
                result["failure"] = result.get("failure") or "no mock capture; inspect gateway response"
        if captures and not result["passed"]:
            result["failure"] = {"response_complete": response_ok, "attempts": {
                a["capture"]: [k for k, v in a["checks"].items() if not v] for a in result["attempts"]}}
        write_json(case_dir / "verification.json", result)
        results.append(result)
    summary = {"run_id": directory.name, "total": len(results), "passed": sum(r["passed"] for r in results),
               "failed": sum(not r["passed"] for r in results), "results": results,
               "scope": "Local routing and transformation only; does not establish Anthropic acceptance."}
    write_json(directory / "summary.json", summary)
    counts = collections.Counter()
    for case in results:
        for attempt in case["attempts"]:
            counts.update({path.split(".")[1].split("[")[0] for path in attempt["changed_paths"]})
    lines = [f"# Claude OAuth 本地回放 {directory.name}", "",
             f"共 {summary['total']} 条；通过 {summary['passed']} 条；未通过 {summary['failed']} 条。", "",
             "通过要求：实际 mock 捕获、完整响应、版本化 system/tools、tool_choice 移除、身份、UA/beta、输出预算、缓存数量、消息顺序/控制字段、工具名称/ID/参数、图片一致性均满足断言。", "",
             "历史消息文本仍会经过现有环境/记忆路径净化；所有实际变化见每条 fields.diff.json 和 body.diff.txt。工具不宣称历史文本逐字不变，也不把 HTTP 200 单独算作通过。", "",
             "| 案例 | HTTP | 捕获次数 | 结果 |", "|---|---:|---:|---|"]
    for case in results:
        status = "通过" if case["passed"] else str(case["failure"])
        lines.append(f"| {case['case_id']} | {case.get('http_status', 0)} | {len(case['attempts'])} | {status.replace('|', '/')} |")
    lines += ["", "每个出站 attempt 中发生变化的顶层字段：", "",
              *[f"- `{key}`：{value} 次" for key, value in sorted(counts.items())], "",
              "这是请求体还原；源录制没有原始 HTTP 请求头。模拟测试证明本地路由和请求转换结果，不代表 Anthropic 会接受这些历史请求。", ""]
    (directory / "REPORT.md").write_text("\n".join(lines))
    print(json.dumps({k: summary[k] for k in ("run_id", "total", "passed", "failed")}))
    return summary


def replay(admin, limit):
    channel = verify_channel(admin)
    control("/health")
    manifest = json.loads(MANIFEST.read_text())
    cases = manifest["cases"][:limit] if limit else manifest["cases"]
    # Validate every source and attachment before sending the first request.
    for case in cases:
        _, notes, _ = reconstruct(case["source"])
        if notes != case["restoration"]:
            raise ValueError("manifest/source/attachment drift: " + case["source"])
    run_id = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + secrets.token_hex(3)
    directory = ROOT / run_id
    directory.mkdir()
    started_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
    write_json(directory / "channel.json", channel)
    write_json(directory / "manifest.json", dict(manifest, cases=cases))
    snapshot(admin, directory)
    for case in cases:
        case_dir = directory / "cases" / case["case_id"]
        body, notes, _ = reconstruct(case["source"])
        write_json(case_dir / "client.request.json", body)
        write_json(case_dir / "restoration.json", notes)
        headers = client_headers(body)
        write_json(case_dir / "client.headers.json", redact(headers))
        control("/arm", {"run_id": run_id, "case_id": case["case_id"]})
        try:
            status, data, returned_headers = http(GATEWAY + "/v1/messages", body, headers)
            result = {"status": status, "headers": redact(returned_headers)}
        except Exception as exc:
            status, data = 0, str(exc).encode()
            result = {"status": 0, "error": str(exc)}
        (case_dir / "response.txt").write_bytes(data)
        write_json(case_dir / "response.json", result)
        if status == 0:
            # A client timeout does not prove that the gateway stopped working.
            # Keep the case armed and stop, so a late retry cannot land in the
            # next case. An operator can inspect and explicitly disarm later.
            print(f"{case['case_id']}: network error; run stopped with mock still armed", flush=True)
            break
        control("/disarm", {})
        print(f"{case['case_id']}: HTTP {status}", flush=True)
    # Container logging is supplementary evidence, with credentials scrubbed.
    log = subprocess.run(["docker", "logs", "--since", started_at, "sub2api-dev"], capture_output=True)
    text = (log.stdout + log.stderr).decode(errors="replace")
    text = text.replace(KEY.read_text().strip(), "[REDACTED]")
    (directory / "gateway.log").write_text(text)
    write_json(directory / "gateway-log-status.json", {"exit_code": log.returncode, "since": started_at})
    telemetry = admin.call("/admin/settings/claude-telemetry")
    write_json(directory / "telemetry-after.json", {"mode": telemetry["mode"], "runtime": telemetry["runtime"]})
    return verify_run(directory)


def upstream_evidence(admin, request_id, account_id):
    errors = admin.call(f"/admin/ops/upstream-errors?q={request_id}&account_id={account_id}&page_size=100")
    events = []
    for error in items(errors):
        if error.get("request_id") != request_id:
            continue
        detail = admin.call(f"/admin/ops/upstream-errors/{error['id']}")
        raw = detail.get("upstream_errors", [])
        events.extend(json.loads(raw) if isinstance(raw, str) else raw)
    return [{k: e.get(k) for k in ("kind", "message", "account_id", "upstream_url", "proxy_name")} for e in events]


def isolation_check(admin):
    channel = verify_channel(admin)
    control("/health")
    directory = ROOT / ("isolation-" + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ"))
    directory.mkdir()
    write_json(directory / "channel-before.json", channel)
    body = {"model": "claude-sonnet-4-6", "stream": False, "max_tokens": 128,
            "messages": [{"role": "user", "content": "Local mock stop/restart verification."}]}
    headers = client_headers(body)
    write_json(directory / "client.request.json", body)
    write_json(directory / "client.headers.json", redact(headers))
    started_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
    subprocess.run(["docker", "stop", "claude-profile-mock"], check=True, capture_output=True)
    try:
        status, data, returned = http(GATEWAY + "/v1/messages", body, headers, timeout=45)
        write_json(directory / "stopped-response.json", {"status": status, "headers": redact(returned)})
        (directory / "stopped-response.txt").write_bytes(data)
        failed_locally = status >= 400
        stopped_request_id = next((v for k, v in returned.items() if k.lower() == "x-request-id"), "")
    finally:
        subprocess.run(["docker", "start", "claude-profile-mock"], check=True, capture_output=True)
    # Wait for loopback readiness only; never send probes to a provider.
    for _ in range(10):
        try:
            control("/health")
            break
        except RuntimeError:
            import time
            time.sleep(0.3)
    control("/arm", {"run_id": directory.name, "case_id": "restart-json"})
    try:
        status, data, returned = http(GATEWAY + "/v1/messages", body, headers)
        (directory / "restart-response.txt").write_bytes(data)
        write_json(directory / "restart-response.json", {"status": status, "headers": redact(returned)})
    finally:
        control("/disarm", {})
    after = verify_channel(admin)
    write_json(directory / "channel-after.json", after)
    log = subprocess.run(["docker", "logs", "--since", started_at, "sub2api-dev"], capture_output=True)
    logs = (log.stdout + log.stderr).decode(errors="replace").replace(KEY.read_text().strip(), "[REDACTED]")
    (directory / "gateway.log").write_text(logs)
    events = upstream_evidence(admin, stopped_request_id, channel["resources"]["account_id"])
    write_json(directory / "upstream-errors.json", events)
    captures = list((directory / "cases/restart-json/mock").glob("*.request.json"))
    try:
        message = json.loads(data)
        restart_ok = status == 200 and message.get("id") == "msg_local_profile_mock" and message["usage"]["output_tokens"] == 0
    except (ValueError, KeyError):
        restart_ok = False
    checks = {"stopped_request_failed": failed_locally, "restart_json_ok": restart_ok,
              "restart_capture_exists": bool(captures), "same_resources": channel["resources"] == after["resources"],
              "local_upstream_failure_evidence": bool(events) and all(
                  e["upstream_url"] == UPSTREAM + "/v1/messages" and e["account_id"] == channel["resources"]["account_id"]
                  and e["kind"] == "request_error" for e in events)}
    write_json(directory / "summary.json", {"checks": checks, "passed": all(checks.values()),
                                           "scope": "No fallback groups or alternate accounts; local upstream failure then same-channel restart."})
    print(json.dumps({"directory": str(directory), "checks": checks}))
    return all(checks.values())


def compose(action):
    cmd = ["docker", "compose", "-f", str(REPO / "deploy/docker-compose.dev.yml"),
           "-f", str(REPO / "deploy/docker-compose.claude-profile-replay.yml")]
    cmd += ["up", "-d", "--no-build", "sub2api", "claude-profile-mock"] if action == "start" else ["stop", "claude-profile-mock"]
    subprocess.run(cmd, cwd=REPO / "deploy", check=True)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("init", "check", "start", "stop", "disarm", "isolation-check", "local-session"):
        sub.add_parser(name)
    select = sub.add_parser("select")
    select.add_argument("sources", nargs=2, type=Path)
    select.add_argument("--count", type=int, default=40)
    select.add_argument("--seed", type=int, default=42)
    run = sub.add_parser("run")
    run.add_argument("--limit", type=int)
    verify = sub.add_parser("verify")
    verify.add_argument("run_id")
    args = parser.parse_args()
    ROOT.mkdir(parents=True, exist_ok=True)
    # Keep one controller across initialization, selection and complete serial runs.
    with (ROOT / "controller.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.command in ("start", "stop"):
            compose(args.command)
        elif args.command == "disarm":
            print(json.dumps(control("/disarm", {})))
        elif args.command == "local-session":
            local_session()
        elif args.command == "select":
            if args.count <= 0:
                parser.error("count must be positive")
            select_sources(args.sources, args.count, args.seed)
        elif args.command == "verify":
            if not re.fullmatch(r"[a-zA-Z0-9_-]+", args.run_id):
                parser.error("invalid run_id")
            return int(verify_run(ROOT / args.run_id)["failed"] != 0)
        else:
            admin = Admin()
            if args.command == "init":
                print(json.dumps(initialize(admin)))
            elif args.command == "check":
                print(json.dumps(verify_channel(admin)["checks"]))
            elif args.command == "run":
                if args.limit is not None and args.limit <= 0:
                    parser.error("limit must be positive")
                return int(replay(admin, args.limit)["failed"] != 0)
            elif args.command == "isolation-check":
                return int(not isolation_check(admin))
    return 0


if __name__ == "__main__":
    sys.exit(main())
