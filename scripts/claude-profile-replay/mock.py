#!/usr/bin/env python3
"""Record-only Anthropic mock. No HTTP client or forwarding implementation."""
import json
import os
import re
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

from common import encoded, redact, write_json

ROOT = Path(os.environ.get("CAPTURE_ROOT", "/captures"))
LOCK = threading.Lock()
ACTIVE = None
SAFE = re.compile(r"^[a-zA-Z0-9_-]{1,100}$")


def mock_response(body):
    usage = {"input_tokens": 0, "output_tokens": 0,
             "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
    message = {"id": "msg_local_profile_mock", "type": "message", "role": "assistant",
               "model": body.get("model", "mock"), "content": [{"type": "text", "text": "local mock"}],
               "stop_reason": "end_turn", "stop_sequence": None, "usage": usage}
    if not body.get("stream", False):
        return "application/json", encoded(message)
    start = dict(message, content=[], stop_reason=None)
    events = [
        ("message_start", {"message": start}),
        ("content_block_start", {"index": 0, "content_block": {"type": "text", "text": ""}}),
        ("content_block_delta", {"index": 0, "delta": {"type": "text_delta", "text": "local mock"}}),
        ("content_block_stop", {"index": 0}),
        ("message_delta", {"delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": usage}),
        ("message_stop", {}),
    ]
    return "text/event-stream", "".join(
        "event: " + name + "\ndata: " + json.dumps(dict(data, type=name)) + "\n\n"
        for name, data in events).encode()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def reply(self, status, value, mime="application/json"):
        data = value if isinstance(value, bytes) else encoded(value)
        self.send_response(status)
        self.send_header("Content-Type", mime)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *_args):
        pass  # HTTP request metadata is persisted without logging credentials.

    def do_GET(self):
        if self.path == "/health":
            return self.reply(200, {"mock": True})
        self.reply(404, {"error": "unsupported"})

    def do_POST(self):
        global ACTIVE
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0 or length > 256 * 1024 * 1024:
            return self.reply(413, {"error": "invalid body size"})
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw)
        except ValueError:
            return self.reply(400, {"error": "invalid JSON"})
        control = self.server.server_port == 8091
        if control:
            with LOCK:
                if self.path == "/arm":
                    if ACTIVE is not None:
                        return self.reply(409, {"error": "another case is armed; disarm explicitly"})
                    if not all(SAFE.fullmatch(str(body.get(k, ""))) for k in ("run_id", "case_id")):
                        return self.reply(400, {"error": "invalid control identifiers"})
                    ACTIVE = {k: body[k] for k in ("run_id", "case_id")}
                    return self.reply(200, ACTIVE)
                if self.path == "/disarm":
                    previous, ACTIVE = ACTIVE, None
                    return self.reply(200, {"previous": previous})
            return self.reply(404, {"error": "unsupported control"})
        if urlsplit(self.path).path != "/v1/messages":
            return self.reply(404, {"error": "mock never forwards"})
        with LOCK:
            tag = dict(ACTIVE) if ACTIVE else {"run_id": "unassociated", "case_id": "orphan"}
            directory = ROOT / tag["run_id"] / "cases" / tag["case_id"] / "mock"
            directory.mkdir(parents=True, exist_ok=True)
            attempt = str(time.time_ns()) + "-" + uuid.uuid4().hex[:8]
            mime, response = mock_response(body)
            (directory / (attempt + ".request.json")).write_bytes(raw)
            write_json(directory / (attempt + ".headers.json"), {
                "method": self.command, "path": self.path, "headers": redact(self.headers),
                "raw_headers": [[k, redact({k: v})[k]]
                                for k, v in self.headers.raw_items()], **tag})
            (directory / (attempt + ".response.txt")).write_bytes(response)
            write_json(directory / (attempt + ".complete.json"), {"status": 200, "content_type": mime, **tag})
        self.reply(200, response, mime)


if __name__ == "__main__":
    os.umask(0o077)
    control = ThreadingHTTPServer(("127.0.0.1", 8091), Handler)
    threading.Thread(target=control.serve_forever, daemon=True).start()
    ThreadingHTTPServer(("0.0.0.0", 8090), Handler).serve_forever()
