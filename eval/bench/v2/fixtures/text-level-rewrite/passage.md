The scheduler dispatches each ready leaf task to the job runner under a strict
per-phase wall-time budget. When a task's dependencies have all reached the
terminal `completed` state, the scheduler marks the task `ready`; otherwise it
remains `blocked`. A failed dependency propagates `blocked` to every descendant,
and the graph reconciler re-evaluates readiness after each terminal transition.
The budget is enforced per phase, not per job, because a planning phase and a
full integration-test run have very different legitimate costs. If a phase
exceeds its budget the job is failed, its task retried up to the configured
maximum, and — once retries are exhausted — the task is blocked and escalated
for human review. The reconciler is idempotent: running it twice produces the
same set of ready tasks.
