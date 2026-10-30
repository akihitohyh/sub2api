"""Emit only test identifiers, status and duration, never application log text."""
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
summary = {}
package_coverage = {}
with (root / 'whitelist.jsonl').open('w') as out:
    for name in ('packages', 'repeated', 'integration'):
        counts = dict(passed=0, failed=0, skipped=0, smart=0)
        seen_packages = set()
        source = root / (name + '.jsonl')
        if source.exists():
            for line in source.read_text().splitlines():
                try:
                    item = json.loads(line)
                except ValueError:
                    if re.match(r"^(?:[^\s]+/)*[^\s]+\.go:\d+:\d+: (?:undefined:|cannot |not enough|too many|declared and not used|.*has no field or method|.*imported and not used)", line):
                        safe = {"compiler": line}
                        out.write(json.dumps(safe) + '\n')
                        print(json.dumps(safe))
                    continue
                if item.get('Action') == 'output':
                    output = item.get('Output', '')
                    location = re.search(r'(?:/[^\s:]+/)*([A-Za-z0-9_]+\.go):(\d+)', output)
                    diagnostic = None
                    if 'WARNING: DATA RACE' in output or 'race detected during execution' in output:
                        diagnostic = 'race_detected'
                    elif 'panic:' in output:
                        diagnostic = 'panic'
                    if diagnostic or location:
                        safe = {'Test': item.get('Test'), 'diagnostic': diagnostic}
                        if location:
                            safe['location'] = location.group(1) + ':' + location.group(2)
                        out.write(json.dumps(safe) + '\n')
                        print(json.dumps(safe))
                action = item.get('Action')
                if action not in ('pass', 'fail', 'skip'):
                    continue
                safe = {k: item[k] for k in ('Action', 'Package', 'Test', 'Elapsed') if k in item}
                out.write(json.dumps(safe) + '\n')
                print(json.dumps(safe))
                if 'Test' in item:
                    if action == 'pass':
                        seen_packages.add(item['Package'])
                    counts[{'pass': 'passed', 'fail': 'failed', 'skip': 'skipped'}[action]] += 1
                    if action == 'pass' and item['Test'].startswith('TestSmartProtocol'):
                        counts['smart'] += 1
                elif action == 'fail':
                    counts['failed'] += 1
        summary[name] = counts
        package_coverage[name] = sorted(seen_packages)
(root / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
if any(v['passed'] == 0 or v['smart'] == 0 or v['failed'] or v['skipped'] for v in summary.values()):
    raise SystemExit('acceptance incomplete: missing, failed, or skipped tests')

required = {'github.com/Wei-Shaw/sub2api/internal/' + path for path in ('service', 'handler', 'repository', 'pkg/requesttiming', 'service/openai_ws_v2')}
if not required.issubset(set(package_coverage['packages'])):
    raise SystemExit('acceptance incomplete: actual package test coverage missing')
