#!/usr/bin/env python3
"""Validate final request snapshots independently of upstream HTTP success."""
import copy
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[1]
fixture = REPO / 'backend/internal/service/claude_code_profile/2.1.284'
expected_tools = json.loads((fixture / 'tools.json').read_text())
expected_system = (fixture / 'system.txt').read_text().strip()
snapshots = {}
for section in re.split(r'\n========== ', (ROOT / 'gateway_debug.log').read_text()):
    if '--- body ---\n' not in section:
        continue
    body = json.loads(section.split('--- body ---\n', 1)[1])
    content = body.get('messages', [{}])[0].get('content', '')
    if ' Case: ' not in str(content):
        continue
    name = content.rsplit(' Case: ', 1)[1]
    match = re.match(r'\[[^]]+\] (\w+)', section)
    if not match or not match[1].startswith('UPSTREAM_FORWARD'):
        continue
    header_text = section.split('--- headers ---\n', 1)[1].split('--- body ---\n', 1)[0]
    headers = dict(line.strip().split(': ', 1) for line in header_text.splitlines() if ': ' in line)
    snapshots[name] = (body, headers)

def cache_blocks(value):
    if isinstance(value, dict):
        return int('cache_control' in value) + sum(cache_blocks(v) for v in value.values())
    if isinstance(value, list):
        return sum(map(cache_blocks, value))
    return 0

results = []
http = {r['case']: r['http_status'] for r in json.loads((ROOT / 'http-results.json').read_text())}
for case in json.loads((ROOT / 'cases.json').read_text()):
    name = case['name']
    assert name in snapshots, f'{name}: no final outgoing snapshot'
    body, headers = snapshots[name]
    source = json.loads((ROOT / name / 'client.body.json').read_text())
    fable = name == '06-fable'
    checks = {
        'caller_system_removed': 'PROFILE_TEST_CALLER_' not in json.dumps(body['system']),
        'system_block_count': len(body['system']) == (2 if fable else 4),
        'billing_and_identity': body['system'][0]['text'].startswith('x-anthropic-billing-header: cc_version=2.1.284.') and body['system'][1]['text'] == "You are Claude Code, Anthropic's official CLI for Claude.",
        'tools_count_23': len(body['tools']) == 23,
        'tool_choice_removed': 'tool_choice' not in body,
        'messages_preserved': body['messages'] == source['messages'],
        'cache_control_at_most_4': cache_blocks(body) <= 4,
        'fixed_user_agent': headers.get('User-Agent', '').startswith('claude-cli/2.1.284'),
        'auth_redacted': headers.get('authorization') in {'Bearer [redacted]', '[redacted]'},
    }
    actual_tools = copy.deepcopy(body['tools'])
    baseline_tools = copy.deepcopy(expected_tools)
    for tools in [actual_tools, baseline_tools]:
        for tool in tools:
            tool.pop('cache_control', None)
    checks['complete_tool_definitions_match_fixture'] = actual_tools == baseline_tools
    if not fable:
        actual = body['system'][2]['text']
        pattern = re.escape(expected_system).replace(re.escape('{{MEMORY_DIRECTORY}}'), r'[^\n]+?')
        checks['complete_system_matches_fixture'] = re.fullmatch(pattern, actual) is not None
        checks['environment_present'] = body['system'][3]['text'].startswith('# Environment\n')
    if name == '05-count-tokens':
        checks['generation_parameters_removed'] = not any(k in body for k in ['max_tokens', 'stream', 'temperature', 'top_p', 'top_k'])
    (ROOT / name / 'upstream.body.json').write_text(json.dumps(body, ensure_ascii=False, indent=2) + '\n')
    (ROOT / name / 'upstream.headers.json').write_text(json.dumps(headers, indent=2) + '\n')
    failures = [key for key, passed in checks.items() if not passed]
    results.append({'case': name, 'http_status': http[name], 'passed': not failures, 'system_blocks': len(body['system']), 'tool_count': len(body['tools']), 'checks': checks, 'failures': failures})
for result in results:
    (ROOT / result['case'] / 'verification.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
(ROOT / 'verification.json').write_text(json.dumps(results, ensure_ascii=False, indent=2) + '\n')
for result in results:
    print(result['case'], 'PASS' if result['passed'] else 'FAIL', result['failures'])
assert all(r['passed'] for r in results), 'Snapshot validation failed'
