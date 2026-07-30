#!/usr/bin/env python3
import json, pathlib, sys
root=pathlib.Path(__file__).resolve().parents[1]
data=json.loads((root/'e2e/coverage.json').read_text())
errors=[]
for capability,evidence in data.items():
    for client,relative in evidence.items():
        base=root/'ui/web/e2e' if client=='web' else root/'ui/desktop/tests'
        path=base/relative
        if not path.is_file(): errors.append(f'{capability}: missing {client} evidence {path.relative_to(root)}')
        elif 'test' not in path.read_text(): errors.append(f'{capability}: {path.relative_to(root)} contains no test')
if errors:
    print('\n'.join(errors));sys.exit(1)
print(f'{len(data)} capabilities have web and Rust test evidence')
