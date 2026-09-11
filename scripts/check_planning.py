#!/usr/bin/env python3
"""Validate the planning record. Does not validate application behavior."""
import copy
import json
from pathlib import Path
import re
import sys
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]


def check_records(definitions, state, acceptance_ids):
    errors = []
    tasks = definitions['tasks']
    by_id = {task['id']: task for task in tasks}
    if len(by_id) != len(tasks):
        errors.append('duplicate task IDs')
    if set(state['tasks']) != set(by_id):
        errors.append('state task IDs differ from definitions')
    covered = set()
    for task in tasks:
        unknown = set(task['dependsOn']) - set(by_id)
        if unknown:
            errors.append(f"{task['id']}: unknown dependencies {sorted(unknown)}")
        if not task['ownedPaths'] or not task['deliverable'] or not task['acceptance']:
            errors.append(f"{task['id']}: incomplete assignment")
        covered.update(task['acceptance'])
        if set(task['acceptance']) - acceptance_ids:
            errors.append(f"{task['id']}: undefined acceptance case")
    if acceptance_ids - covered:
        errors.append(f'unassigned acceptance cases: {sorted(acceptance_ids - covered)}')
    visiting, visited = set(), set()

    def visit(task_id):
        if task_id in visiting:
            errors.append(f'dependency cycle at {task_id}')
            return
        if task_id in visited or task_id not in by_id:
            return
        visiting.add(task_id)
        for dependency in by_id[task_id]['dependsOn']:
            visit(dependency)
        visiting.remove(task_id)
        visited.add(task_id)

    for task_id in by_id:
        visit(task_id)
    allowed = {'planned', 'assigned', 'in_progress', 'blocked', 'in_review', 'done'}
    for task_id, record in state['tasks'].items():
        if record['status'] not in allowed:
            errors.append(f'{task_id}: invalid status')
        if record['status'] == 'done':
            review = record.get('review') or {}
            if not re.fullmatch(r'[0-9a-f]{40}', record.get('integratedSha') or ''):
                errors.append(f'{task_id}: completion missing integrated commit')
            if not record.get('evidence') or review.get('decision') != 'approved':
                errors.append(f'{task_id}: completion missing evidence/review')
            if not review.get('reviewer') or review.get('reviewer') == record.get('owner'):
                errors.append(f'{task_id}: completion missing independent reviewer')
    if not state['implementationAuthorized'] and any(
        record['status'] != 'planned' for record in state['tasks'].values()
    ):
        errors.append('implementation progress recorded without authorization')
    return errors


def main():
    definitions = json.loads((ROOT / 'docs/execution/tasks.json').read_text())
    state = json.loads((ROOT / 'docs/execution/state.json').read_text())
    acceptance = (ROOT / 'docs/verification/acceptance.md').read_text()
    acceptance_ids = set(re.findall(r'^\| (A-\d+) \|', acceptance, re.M))
    errors = check_records(definitions, state, acceptance_ids)
    plan = (ROOT / 'docs/plans/implementation.md').read_text()
    for task in definitions['tasks']:
        if f"### {task['id']}: {task['title']}" not in plan:
            errors.append(f"{task['id']}: missing readable task definition")
    for path in ROOT.rglob('*.md'):
        if '.git' in path.parts:
            continue
        for target in re.findall(r'\]\(([^\s]+?)\)', path.read_text()):
            if re.match(r'[a-zA-Z][\w+.-]*:', target) or target.startswith('#'):
                continue
            local = unquote(target.split('#')[0].strip('<>'))
            if local and not (path.parent / local).exists():
                errors.append(f'{path.relative_to(ROOT)}: broken link {local}')
    if '--self-test' in sys.argv:
        broken = copy.deepcopy(definitions)
        broken['tasks'][0]['dependsOn'] = [broken['tasks'][0]['id']]
        assert any('cycle' in e for e in check_records(broken, state, acceptance_ids))
        broken['tasks'][0]['dependsOn'] = ['MISSING']
        assert any('unknown dependencies' in e for e in check_records(broken, state, acceptance_ids))
        broken_state = copy.deepcopy(state)
        broken_state['tasks'][definitions['tasks'][0]['id']]['status'] = 'done'
        assert any('completion missing' in e for e in check_records(definitions, broken_state, acceptance_ids))
        print('Self-checks passed: cycles, unknown dependencies, unsupported completion.')
    if errors:
        print('\n'.join(errors), file=sys.stderr)
        return 1
    print(f"Planning valid: {len(definitions['tasks'])} tasks, {len(acceptance_ids)} acceptance cases; local links resolve.")
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
