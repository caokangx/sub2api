#!/usr/bin/env python3
"""Replay local profile cases. Set PROFILE_TEST_API_KEY to an enabled test group's key."""
import json
import os
from pathlib import Path
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent
key = os.environ['PROFILE_TEST_API_KEY']
base = os.environ.get('PROFILE_TEST_BASE_URL', 'http://127.0.0.1:8080')
results = []
for case in json.loads((ROOT / 'cases.json').read_text()):
    name = case['name']
    body = json.loads((ROOT / name / 'client.body.json').read_text())
    body['messages'][0]['content'] += ' Case: ' + name
    (ROOT / (name + '.sent.json')).write_text(json.dumps(body, indent=2) + '\n')
    req = urllib.request.Request(base + case['path'], json.dumps(body).encode(), {
        'x-api-key': key,
        'content-type': 'application/json',
        'anthropic-version': '2023-06-01',
        'user-agent': case['user_agent'],
        'x-client-request-id': 'profile-test-' + name,
    })
    try:
        with urllib.request.urlopen(req, timeout=15) as response:
            status, data = response.status, response.read()
    except urllib.error.HTTPError as exc:
        status, data = exc.code, exc.read()
    except Exception as exc:
        status, data = 0, str(exc).encode()
    (ROOT / (name + '.response.txt')).write_bytes(data)
    results.append({'case': name, 'path': case['path'], 'http_status': status})
    print(json.dumps(results[-1]), flush=True)
(ROOT / 'http-results.json').write_text(json.dumps(results, indent=2) + '\n')
