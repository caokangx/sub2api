import base64
import json
import tempfile
import threading
import unittest
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path

import mock
from common import reconstruct, sha, write_json
from replay import field_diff, verify_run


class ReconstructionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.record = self.root / "record.hjson"

    def test_hjson_multiline_and_request_controls(self):
        self.record.write_text("""{
          // HJSON, including literal // in text
          model: claude-sonnet-4-6
          messages: [{role: "user", content: '''
            hello // literal
            second line
          '''}]
          request: {
            stream: true
            thinking: {type: "enabled", budget_tokens: 2048}
            system: original
          }
        }""")
        body, notes, _ = reconstruct(self.record)
        self.assertIn("hello // literal\nsecond line", body["messages"][0]["content"])
        self.assertTrue(body["stream"])
        self.assertEqual(body["thinking"]["budget_tokens"], 2048)
        self.assertFalse(notes["original_http_headers_available"])

    def image_record(self, data, **fields):
        write_json(self.record, {"model": "claude-sonnet-4-6", "messages": [{"role": "user", "content": [
            {"type": "image", "source": dict(type="base64", data=data, **fields)}]}]})

    def test_attachment_flattened_path_and_hash(self):
        content = b"image-content"
        (self.root / "image.png").write_bytes(content)
        self.image_record("[attachment:media/image.png]", size_bytes=len(content), sha256=sha(content))
        body, notes, _ = reconstruct(self.record)
        self.assertEqual(base64.b64decode(body["messages"][0]["content"][0]["source"]["data"]), content)
        self.assertEqual(notes["attachments"][0]["sha256"], sha(content))
        self.assertEqual(notes["attachments"][0]["size"], len(content))

    def test_missing_bad_hash_size_ambiguity(self):
        self.image_record("[attachment:media/image.png]")
        with self.assertRaisesRegex(ValueError, "missing"):
            reconstruct(self.record)
        (self.root / "image.png").write_bytes(b"x")
        self.image_record("[attachment:media/image.png]", sha256="bad")
        with self.assertRaisesRegex(ValueError, "SHA-256"):
            reconstruct(self.record)
        self.image_record("[attachment:media/image.png]", size=8)
        with self.assertRaisesRegex(ValueError, "size mismatch"):
            reconstruct(self.record)
        (self.root / "media").mkdir()
        (self.root / "media/image.png").write_bytes(b"different")
        with self.assertRaisesRegex(ValueError, "ambiguous"):
            reconstruct(self.record)

    def test_conflicts_and_duplicates_rejected(self):
        write_json(self.record, {"model": "a", "request": {"model": "b"}})
        with self.assertRaisesRegex(ValueError, "conflicting"):
            reconstruct(self.record)
        self.record.write_text('{"model":"a","model":"b"}')
        with self.assertRaisesRegex(ValueError, "duplicate"):
            reconstruct(self.record)

    def test_tool_history_and_message_controls_unchanged(self):
        messages = [{"role": "assistant", "output_config": {"effort": "high"}, "content": [
            {"type": "tool_use", "id": "1", "name": "old-tool", "input": {"a": 1}}]},
            {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "1", "content": "done"}]}]
        write_json(self.record, {"model": "a", "request": {"messages": messages, "stream": False}})
        body, _, features = reconstruct(self.record)
        self.assertEqual(body["messages"], messages)
        self.assertTrue(features["tool_history"])


class MockTests(unittest.TestCase):
    def test_json_and_sse_zero_usage(self):
        for stream in (False, True):
            mime, body = mock.mock_response({"model": "test", "stream": stream})
            if stream:
                self.assertEqual(mime, "text/event-stream")
                frames = [json.loads(line[6:]) for line in body.decode().splitlines() if line.startswith("data: ")]
                self.assertEqual(frames[-1]["type"], "message_stop")
                self.assertEqual(frames[0]["message"]["usage"]["input_tokens"], 0)
                self.assertEqual(frames[-2]["usage"]["output_tokens"], 0)
            else:
                self.assertEqual(mime, "application/json")
                self.assertEqual(json.loads(body)["usage"]["output_tokens"], 0)

    def test_http_captures_before_response_and_redacts(self):
        with tempfile.TemporaryDirectory() as directory:
            old_root = mock.ROOT
            mock.ROOT = Path(directory)
            server = ThreadingHTTPServer(("127.0.0.1", 0), mock.Handler)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}/v1/messages?beta=true",
                    b'{"model":"test","stream":true}', {"Authorization": "Bearer fake-secret"})
                with urllib.request.urlopen(req) as response:
                    self.assertEqual(response.status, 200)
                    self.assertIn(b"message_stop", response.read())
                captures = list(Path(directory).rglob("*.request.json"))
                self.assertEqual(len(captures), 1)
                headers = next(Path(directory).rglob("*.headers.json")).read_text()
                self.assertNotIn("fake-secret", headers)
                self.assertIn("[REDACTED]", headers)
                self.assertEqual(len(list(Path(directory).rglob("*.complete.json"))), 1)
            finally:
                server.shutdown()
                server.server_close()
                mock.ROOT = old_root

    def test_diff_reports_nested_history(self):
        diff = field_diff({"messages": [{"content": "a"}]}, {"messages": [{"content": "b"}]})
        self.assertEqual(diff[0]["path"], "$.messages[0].content")

    def test_interrupted_manifest_cannot_report_success(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_json(root / "expected.json", {})
            write_json(root / "manifest.json", {"cases": [{"case_id": "one"}, {"case_id": "two"}]})
            summary = verify_run(root)
            self.assertEqual(summary["total"], 2)
            self.assertEqual(summary["failed"], 2)
            self.assertEqual(summary["passed"], 0)
            self.assertTrue(all("interrupted" in r["failure"] for r in summary["results"]))


if __name__ == "__main__":
    unittest.main()
