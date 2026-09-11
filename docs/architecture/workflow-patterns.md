# Workflow pattern selection

Selected for v0.0.1: an API-owned durable process manager with ordered steps and
approval gates, persisted in SQLite. See [data and recovery](../specs/spec-001-media-reconciliation/data-and-recovery.md)
for the authoritative state and crash contract.

The BFF submits a composition and renders progress. It does not execute a series
of upstream requests itself. Closing the browser does not abandon accepted work.
Independent actions use the same execution path as composed workflows.

Approval and queued work commit together. Workers poll action_runs, so a separate
outbox containing identical work would add no guarantee. A transactional outbox
becomes relevant for future broker/webhook delivery, not for making Arr calls
atomic with SQLite. See the [outbox pattern](https://microservices.io/patterns/data/transactional-outbox.html).

External effects can partially succeed. Keep their evidence, reconcile lost
responses, and require explicit follow-up actions instead of automatic destructive
compensation. This uses the long-running coordination ideas of a
[saga](https://microservices.io/patterns/data/saga.html), without claiming rollback
or exactly-once behavior across upstream APIs.

No general DAG engine, event broker, or user-programmable workflow language is
needed by the current ordered recipes. Add one only when actual branching and
join requirements exceed this model.
