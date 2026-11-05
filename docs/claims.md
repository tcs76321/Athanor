# Load-bearing claims → evidence

**Status:** living · **Reviewed at:** every tag
**Purpose:** keep the claims a reader relies on traceable to a test, gate, or
demo. A claim without a proof is either removed or marked as a target.

| Claim | Where stated | Proof | Status |
|---|---|---|---|
| No tool execution in `internal/`; no raw `syscall` outside the path library | README, ARCHITECTURE §25 | `internal/gate/gate_test.go` (Gate G1) | proven |
| Every internal route is auth-wrapped; every tool handler consults the envelope | ARCHITECTURE §3.2, §25 | `internal/gate/gate_g2_test.go` | proven |
| Job Pods are rootless, read-only-rootfs, `--network=none`, token-only mount | ARCHITECTURE §21.2 | `internal/jobpod/args_common.go`, `args_linux.go`, argv gate, behavioral probes | proven (pod hardening); token-only mount is the current truth |
| Gateway is default-deny, allowlist-only, pinned-IP, redirect-bounded | ARCHITECTURE §21.5 | `internal/gateway` corpus + integration probes | proven |
| Path containment rejects traversal/absolute/NULL and opens with `O_NOFOLLOW` (final component) | README, ARCHITECTURE §21.3 | `internal/airlock/paths` tests + fuzz | proven |
| Event log is append-only | ARCHITECTURE §28 | `events_no_update`/`events_no_delete` triggers; `internal/store` | proven |
| State is persisted at every job transition; crash recovery resumes | ARCHITECTURE §8.2, §23.6 | `internal/engine` transition/recovery tests; demo-m1 | proven |
| Evaluation is deterministic-first; the LLM judge is advisory | ARCHITECTURE §19.3 | `internal/verify`, `internal/engine/decide.go`, F4 closeout | proven for code; text/document depend on declared checks |
| Compaction is Temperature 0.0 and content-addressed | ARCHITECTURE §10.3 | `internal/mce/compact.go` + determinism tests | proven |
| Context floors are never silently reduced | ARCHITECTURE §12.3 | `internal/llm/feasibility.go`, KV pressure tests | proven |
| The model cannot invoke tools; the engine executes them | ARCHITECTURE §25, ADR-0080 | `internal/prompt/render_test.go`; no tool-call parser | proven |
| A code project's tasks carry the execution tools | ARCHITECTURE §25, ADR-0081 | `internal/project/project_test.go` | proven |
| The Core Pod is the only supported runtime | README, ADR-0078 | `install.sh` / `athanor start` target the container; `backup`/`restore -container` and host `doctor`/`models` are wired; a repo-backed Core Pod job completed end to end (0.0.0 baseline) | **proven for install/start/backup/restore and a Core Pod job**; the timed fresh-install demo remains |
| A repository working set reaches Job Pods | README coding workflow | staged snapshot at `/workspace/repo`; `/tmp/work` overlay; verified by a Core Pod `pytest` asserting the mount (ADR-0082) | **proven** |
| A project has inferred capabilities, not a user-chosen archetype | ADR-0093 | `internal/project/capability.go` (`InferCapabilities`, `CreateInferred`); `TestInferCapabilities`, `TestCreateInferredPersistsCapabilities` | **proven** |
| Tasks in one project can have different capabilities | ADR-0093 | `tasks.capability`; `InferTaskCapability`; `CreateDAG`; `TestInferTaskCapability` | **proven** |
| The code test command is repository-derived, not user-entered | ADR-0093 | `DetectTestCommand`; `Project.TestCommand`; `TestDetectTestCommand` | **proven for common manifests; unknown repos audit `tests_skipped`** |
| The frozen benchmark is the controlled study | AGENTS, ADR-0075 | v2 corpus and digest are pinned; the pre-P0 slice ran 2026-10-25 (30/41, [findings](probes/bench-v2-findings.md)); the post-P0 full slice ran 2026-11-02 on the Core Pod (**32/41**, [findings](probes/bench-v2-p0-findings.md)); the focused 2026-10-29 X-code diagnostic is 4/6 host / 3/6 Core; the 6-task smoke runs on engine changes | **proven for the instrument; post-P0 full slice measured** |
| A benchmark code row is labeled by the recorded frozen-test outcome, not engine acceptance | ADR-0092 | `tools/bench/collect.go` (`loadCodeTestOutcome`), `tools/bench/report.go` (`jobPassed`); `TestCodeTestOutcomeIsTheLabel`, `TestLoadCodeTestOutcome` | **proven** |
| Engine acceptance is reported separately from the benchmark label | ADR-0092 | `jobMetrics.EngineAccepted`; report code test-vs-accept table; `TestReportSurfacesCodeTestMismatch` | **proven** |
| A v2 corpus schema/fixture path resolves to the version-local copy when one exists | ADR-0092 | `tools/bench/corpus.go` (`versionLocalPath`); `TestCorpusSchemaPathsResolveWithinVersion`, `TestVersionLocalPath` | **proven** |
| Non-code benchmark checks score the best candidate, not only the accepted artifact | ADR-0092 | `tools/bench/collect.go` (`checkArtifactText`); `TestCheckArtifactTextPrefersBestCandidate` | **proven** |
| Onboarding generator measurements use the benchmark label, not engine acceptance | ADR-0092 | `tools/bench/measure.go` (`generatorMeasurements`); `TestGeneratorMeasurementsUseBenchmarkLabel` | **proven** |
| A job panic is contained to one job | ADR-0086 | `internal/engine/panic_test.go`; `job_panicked` event | **proven** |
| The benchmark records comparison-guard diagnostics | AGENTS (honest evidence), P0 plan | `tools/bench/collect.go` (`comparisonRow`); `TestLoadComparisonDiagnostics`; report decision-diagnostics section | **proven** |
| A declared data schema is never silently omitted | P0 plan | `tools/bench/runner.go` (`dataSchemaFor`); `TestDataSchemaForFailsLoudly` | **proven** |
| A data task sees its schema as a pinned requirement | ADR-0070 amendment | `internal/prompt/data_schema_test.go`; `TestRun_DataArchetypePromptIncludesSchema` | **proven** |
| The engine and benchmark agree on a single wrapped JSON value | ADR-0070 amendment | `internal/verify/schema.go` (`extractJSONValue`); `TestDataSchemaExtractsFencedAndProse` | **proven** |
| Synthesis does not discard a verified candidate (all hard-verifier archetypes) | ADR-0068 amendment | `internal/engine/phases.go`; `TestSynthesizeFallsBackForDeclaredChecks` | **proven** |
| A code job's prompt contains ranked repository file bodies | ADR-0083 | `cmd/athanor/context_provider_test.go` (`WorkingSet`); `TestWorkingSetIsAutoForCodeOnly`; `TestRepositoryWorkingSetIsPinnedAndDeduped` | **proven in tests; full post-P0 X-code 4/6, code 16/18** |
| The working-set pack is byte-capped, pinned, and never truncated | ADR-0083 | `TestRepositoryWorkingSetIsPinnedAndDeduped`; `TestContextProviderWorkingSet` (cap/foreign scope) | **proven** |
| Compacted project memory reaches the prompt | ADR-0085 | `internal/engine/engine.go` (`MemorySource`); `TestMemoryReachesEpisodicTier`; `memory_injected` audit | **proven** |
| The active/dormant swap runs in the normal code loop | ADR-0083 amendment | `internal/engine/context_tiers.go` (`ActiveSeeder`); `TestWorkingSetIsAutoForCodeOnly`; `TestContextProviderWorkingSet` (SeedActive persists) | **proven** |
| Cancel stops the phase and commits `cancelled` | ADR-0087 | `internal/engine/engine.go` (`Cancel`, `EnqueueContext`); `TestCancelStopsRunningJob` | **proven** |
| The Core Pod boots and serves in CI | ADR-0078, P1 plan | `scripts/core-smoke.sh` (`make core-smoke`); `.github/workflows/ci.yml` `core-smoke` job; local run 2026-10-28 | **proven locally; CI job added** |
| A loopback `/status` reports jobs, freeze, and alarms | P1 plan | `internal/server/server.go`; `TestStatusSnapshot`, `TestStatusUnconfigured` | **proven** |
| A daemon state dir reduces to the soak report shape | `docs/evidence-pipeline.md` §7.1 | `tools/bench/evidence.go` (`evidence` subcommand); `TestEvidenceRows`; live run on a retained state dir | **proven for the reducer; daemon `athanor evidence` wrapper remains** |
| API and UI share one project-create/pre-index path | P2-0 | `internal/firstuse`; `TestCreateRunsPreindexThenStartsTheJob`; API/UI adapter tests remain green | **proven** |
| A project can be clarified and planned before its first job | ADR-0088 | `internal/interview`; `TestInterviewPlanApprovalStartsWork`; `TestProjectInterviewPage` | **proven** |
| Accepted artifacts can be reviewed with provenance | ADR-0089 | `internal/review`; `TestArtifactReviewRoundTrip`; UI artifact page review form | **proven** |
| A release is checksummed and the installer verifies it | `docs/install-release.md` | `scripts/release.sh`; `scripts/install-release.sh -check-only`; local tarball run 2026-10-29 | **proven for SHA-256; signatures are 1.0.0** |
| The benchmark reports MCE mechanism metrics | ADR-0075 | `tools/bench/telemetry.go` (dormant rows, working-set chunks/bytes, memory hits, compactions, kv warn/critical); `TestFillTelemetry`; reported in both full v2 runs | **instrumented; post-P0 full run reported** |
| The working-set pack improves repo-scale X-code | ADR-0083 | host diagnostic **4/6**; clean Core Pod rerun **3/6**; full post-P0 Core Pod slice **4/6** (vs 0/6 pre-P0), 45 chunks / 11.9 KiB injected; [findings](probes/bench-v2-p0-findings.md) | **directional; one model, one pass** |
| A code prompt includes the verification command | ADR-0065, P0 fairness | `internal/prompt` (`Task.TestCommand`); `TestTaskContextShowsVerificationCommand`; `TestRun_CodeArchetypeUsesConfiguredTestCommand` | **proven** |
| A single-file candidate is staged under the module the test imports | ADR-0065, P0 fairness | `internal/engine/pod_wiring.go` (`candidateModuleName`); `TestCandidateModuleName` | **proven** |
| A fresh install is guided through machine onboarding | ADR-0095 | `/ui/welcome`; `TestFirstRunRedirectsToWelcome`, `TestWelcomeRendersBareShell`, `TestOnboardingAdvancesAndCompletes` | **proven** |
| The first-run environment check runs doctor from the GUI | ADR-0095 | `Deps.Doctor` wired in `cmd/athanor/serve.go`; `TestWelcomeRunsDoctorChecks` | **proven** |
| Onboarding detects RAM/CPU/disk and proposes editable Job Pod limits | ADR-0095 | `/ui/welcome?step=resources`; `TestWelcomeResourcesStep`, `TestWelcomeApplyResources`, `TestUpdateJobPodResources` | **proven** |
| Athanor self-selects a model per persona and can apply it safely | ADR-0095 | `onboard.AutoAssignWithScores` (size/family plus stored qualification scores); `TestAutoAssignPicksLargestAndCrossFamily`, `TestAutoAssignUsesMeasuredScores`, `TestWelcomeModelsSelfSelectAndApply`, `TestUpdatePersonasPreservesAndValidates` | **proven** |
| The restart-required state survives navigation and clears only at boot | ADR-0095 | `internal/ui/restart.go`; `TestRestartRequiredBanner`; boot clear in `cmd/athanor/serve.go` | **proven** |
| An in-flight discovery session survives a restart as failed/interrupted, not silence | ADR-0094 | `discovery.Store.Begin/Complete/Fail`; `TestBeginAndFailAreDurable`; stage logic in `project_onboarding.go` | **proven** |
| The Core Pod mounts its config read-write so onboarding writes land on the host | ADR-0078, ADR-0095 | `cmd/athanor/core.go` (`coreRunArgs`), `TestCoreRunArgs` | **proven structurally** |
| A blocked task can be recovered from the UI | known-issues H8 | `handleTaskReset`; `TestTaskReset`; scheduler `Reset` | **proven with a fake resetter** |
| An accepted artifact can be exported from the reader through egress | ADR-0015, ADR-0039 | `handleArtifactExport`; `TestArtifactExport` | **proven with a fake exporter** |
| A project can be briefed, explored as a portfolio, and planned before work | ADR-0094, ADR-0095 | `internal/discovery`; `TestProjectOnboardingDiscoveryFlow`, `TestStoreRoundTripAndSelect`, `TestDedupeAndValidate` | **proven with a fake generator; a live-model run is the demo** |
| Discovery is a portfolio, not a single-winner loop | ADR-0094 | `internal/discovery` (no engine comparison); `TestScoreIsDeterministicAndOrdered` | **proven by structure** |
| A short, honest goal is accepted | ADR-0094 | `goals` 3-character floor; `TestCreateValidation`, `TestSubmitGoal` | **proven** |
| The UI serves embedded assets with a strict CSP and no inline scripts/styles | ADR-0095 | `/ui/assets/*`; `TestAssetsAreEmbeddedAndCached`, `TestCSPAndNoInlineScripts` | **proven** |
| Artifacts and plans are readable Markdown without raw HTML | ADR-0095 | `internal/ui/markdown.go`; `TestRenderMarkdownEscapesHTML`, `TestRenderMarkdownRejectsUnsafeLinks` | **proven** |
| A queued cancel commits `cancelled` and does not underflow the engine cap | ADR-0087, this pass | `internal/engine/control_race_test.go`; `TestCancelQueuedJobCommitsCancelled`, `TestAbandonedWaitDoesNotUnderflowInFlight` | **proven** |
| An external pause during a phase is honored, not failed | this pass | `TestExternalPauseDuringPhaseDoesNotFailJob` | **proven** |

## Rule

When a code or behavior change falsifies a row, update the row in the same
commit (or remove the claim). Do not let a "target" row silently read as
"proven".
