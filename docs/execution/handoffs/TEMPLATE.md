# Task handoff template

Copy to the assigned task's handoff path. Replace placeholders with evidence;
do not retain invented values or claim checks that did not run.

## Assignment

- Task ID and title:
- Owner/agent and independent reviewer:
- Base commit:
- Branch/worktree or shared-checkout ownership, repository-relative public notation:
- Owned files and generated outputs:
- Dependencies verified at commits:
- Required acceptance IDs and exact planned commands:

## Contract and work

- Linked specification sections and relevant invariants:
- Intended result and capability limits:
- Changes completed / remaining:
- Shared contract changes requested from coordinator:
- Changed paths and tested commit:

## Verification

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |

Record actual effects, fault-injection boundary and per-file outcomes where relevant.
Explicitly list skipped/blocked/not-run checks. Do not place secrets or real media here.

## Review and integration

- Reviewer identity/role and reviewed commit:
- Findings with severity, reproduction and contract reference:
- Fix commit and regression evidence:
- Final reviewer decision:
- Integrated commit, recorded by coordinator:

## Resume checkpoint

- Current state and outstanding uncertainty:
- Active process or agent ownership, sanitized:
- Next safe action:
- Blocker and exact input/evidence needed:
- No conflicting writes or unknown files removed:

Coordinator updates state.json. Workers do not declare integration by editing state.
