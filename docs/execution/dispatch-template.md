# Bounded worker dispatch

Implement task TASK-ID from tasks.json at base BASE-SHA. Ownership is the listed
paths plus docs/execution/handoffs/TASK-ID.md. You are not alone in this repository.
Preserve other agents' changes and do not edit shared contracts, module dependencies,
migrations or generated shared files unless explicitly assigned.

Read AGENTS.md, RESUME.md, task definition, linked specs and acceptance cases.
Deliver the stated result and focused checks. Runtime tests use synthetic data and
disposable services only. Report unsupported upstream behavior rather than weakening
approval, identity, no-overwrite or recovery guarantees.

Before writing outside ownership or relying on a changed shared contract, send the
coordinator the exact request and affected dependencies. Checkpoint the handoff at
meaningful boundaries and before compaction. Record commands, exit statuses, tested
commit, effects and remaining work. Do not edit coordinator state.json.

Independent reviewer receives task ID, authored commit/diff, contract, acceptance
IDs and evidence. Reviewer checks the implementation and reproduces critical checks;
reviewer did not author the batch. Findings return to the owner. Coordinator alone
integrates and records completion after evidence and review pass.
