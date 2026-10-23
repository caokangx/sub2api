"""Strict local body reconstruction; recording contents are data, never commands."""
import base64
import copy
import hashlib
import json
import re
from pathlib import Path


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(encoded(value))


def redact(headers):
    return {k: ("[REDACTED]" if k.lower() in {
        "authorization", "x-api-key", "cookie", "set-cookie", "proxy-authorization"
    } else v) for k, v in headers.items()}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate HJSON field: " + key)
        result[key] = value
    return result


def walk(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk(child)


def reconstruct(path):
    import hjson
    path = Path(path)
    raw = path.read_bytes()
    record = hjson.loads(raw.decode("utf-8-sig"), object_pairs_hook=unique_object)
    if not isinstance(record, dict) or not isinstance(record.get("request", {}), dict):
        raise ValueError("record/request must be an object")
    body = copy.deepcopy(record.get("request", {}))
    notes = {"kind": "请求体还原", "original_http_headers_available": False,
             "source_sha256": sha(raw), "filled_fields": [], "attachments": []}
    for key in ("messages", "tools", "model"):
        if key in body and key in record and body[key] != record[key]:
            raise ValueError("conflicting request and top-level " + key)
        if key not in body and key in record:
            body[key] = copy.deepcopy(record[key])
            notes["filled_fields"].append(key)
    if not isinstance(body.get("messages"), list) or not body["messages"]:
        raise ValueError("missing/non-array/empty messages")
    if not isinstance(body.get("model"), str) or not body["model"]:
        raise ValueError("missing/non-string model")
    for node in walk(body):
        data = node.get("data")
        if not isinstance(data, str):
            continue
        match = re.fullmatch(r"\[attachment:([^\]]+)\]", data)
        if match:
            declared = Path(match[1])
            candidates = [declared if declared.is_absolute() else path.parent / declared,
                          path.parent / declared.name]
            candidates = list(dict.fromkeys(p.resolve() for p in candidates if p.is_file()))
            if not candidates:
                raise ValueError("attachment missing: " + match[1])
            contents = [p.read_bytes() for p in candidates]
            if len({sha(b) for b in contents}) > 1:
                raise ValueError("ambiguous attachment paths: " + match[1])
            content = contents[0]
            if not content or len(content) > 32 * 1024 * 1024:
                raise ValueError("attachment empty or exceeds 32 MiB: " + match[1])
            for key in ("size", "size_bytes", "byte_length"):
                if key in node and int(node[key]) != len(content):
                    raise ValueError("attachment size mismatch: " + match[1])
            if "sha256" in node and node["sha256"].lower() != sha(content):
                raise ValueError("attachment SHA-256 mismatch: " + match[1])
            notes["attachments"].append({"declared": match[1], "path": str(candidates[0]),
                                          "size": len(content), "sha256": sha(content)})
            node["data"] = base64.b64encode(content).decode()
        elif data.startswith("[attachment:"):
            raise ValueError("malformed attachment placeholder")
        elif node.get("type") == "base64":
            try:
                base64.b64decode(data, validate=True)
            except Exception as exc:
                raise ValueError("invalid base64 attachment") from exc
    blob = encoded(body)
    if len(blob) > 256 * 1024 * 1024:
        raise ValueError("restored body exceeds gateway 256 MiB limit")
    nodes = list(walk(body))
    features = {"model": body["model"], "session": record.get("session_id", path.stem),
                "system": sha(encoded(body.get("system")))[:16],
                "tools": sha(encoded(body.get("tools")))[:16],
                "images": any(n.get("type") == "image" for n in nodes),
                "tool_history": any(n.get("type") in ("tool_use", "tool_result") for n in nodes),
                "thinking": body.get("thinking", {}).get("type", "absent"),
                "size": "small" if len(blob) < 65536 else "medium" if len(blob) < 524288 else "large",
                "stream": body.get("stream", False)}
    notes["body_sha256"] = sha(blob)
    return body, notes, features
