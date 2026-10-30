"""Emit only test identifiers, status and duration, never application log text."""
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
summary = {}
with (root / 'whitelist.jsonl').open('w') as out:
    for name in ('packages', 'integration'):
        counts = dict(passed=0, failed=0, skipped=0, smart=0)
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
                action = item.get('Action')
                if action not in ('pass', 'fail', 'skip'):
                    continue
                safe = {k: item[k] for k in ('Action', 'Package', 'Test', 'Elapsed') if k in item}
                out.write(json.dumps(safe) + '\n')
                print(json.dumps(safe))
                if 'Test' in item:
                    counts[{'pass': 'passed', 'fail': 'failed', 'skip': 'skipped'}[action]] += 1
                    if action == 'pass' and item['Test'].startswith('TestSmartProtocol'):
                        counts['smart'] += 1
                elif action == 'fail':
                    counts['failed'] += 1
        summary[name] = counts
(root / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
if any(v['passed'] == 0 or v['failed'] or v['skipped'] for v in summary.values()):
    raise SystemExit('acceptance incomplete: missing, failed, or skipped tests')
