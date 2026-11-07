# Load-bearing claims → evidence

**Status:** living · **Reviewed at:** every tag
**Purpose:** keep the claims a reader relies on traceable to a test, gate, or
demo. A claim without a proof is either removed or marked as a target.

| Claim | Where stated | Proof | Status |
|---|---|---|---|
| No tool execution in `internal/`; no raw `syscall` outside the path library | README, ARCHITECTURE §25 | `internal/gate/gate_test.go` (Gate G1) | proven |
| Every internal route is auth-wrapped; every tool handler consults the envelope | ARCHITECTURE §3.2, §25 | `internal/gate/gate_g2_test.go` | proven |
| Job Pods are rootless, read-only-rootfs, `--network=none`, with no bearer-token mount by default | ARCHITECTURE §21.2 | `internal/jobpod/args_common.go`, `args_linux.go`, argv gate, behavioral probes | proven (pod hardening); the read-only token mount is opt-in (`Spec.MountToken`) |
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
| A chosen direction can produce three AI-authored plans, and the operator selects one before work starts | ADR-0097 | `internal/planning` (`Validate`, `Score`, `ToTaskSpecs`, `Store`); `TestProjectOnboardingAIPlanFlow`, `TestParseValidateAndScore`, `TestScoreClampsConfidenceAndStaysBounded` | **proven with a fake generator; a live-model run is the demo** |
| A plan generated for one direction is never shown or approved for another | ADR-0097 | option-scoped sessions and handler guard; `TestProjectOnboardingIgnoresStalePlanAfterDirectionChange` | **proven** |
| Job Pods are sized from the task capability within an adaptive ceiling | ADR-0098 | `jobpod.AdaptiveLimits`; `TestAdaptiveLimitsScaleByCapability`, `TestPodLifecycleAdaptsLimitsToTaskCapability` | **proven** |
| Onboarding distinguishes container-runtime memory from host memory | ADR-0098 | `TestRecommendedResourcesIsAdaptive`, `TestBudgetForSeparatesHostInferenceAndRuntimeSandbox` | **proven** |
| A configured model is accepted only when its measured resident size fits the host budget | ADR-0099 | `athanor models -audit`; `model-fit.json`; `TestRunModelAuditMeasuresLoadedSize`, `TestCheckModelAuditFailsOnMeasuredNonFit`, `TestCheckModelAuditRejectsStaleConfig` | **proven with a fake Ollama; the live audit is the host check** |
| A cost tie cannot overturn a failed hard verifier | ADR-0100 | `internal/engine/compare.go` (`costAwareEligible`); `TestCostAwareEligibleRefusesHardVerifierFailure` | **proven** |
| A single-family install can accept a follow-up verifier-passed code artifact | ADR-0100 | `internal/engine/compare.go` (verifier pass at/above previous; no-cross-family fallback); `TestVerifierFirst_SameFamilyPreviousArtifactAccepted` | **proven** |
| Candidate tests never see another candidate's work tree | ADR-0100 | `cmd/athanor/pod_executor.go` (`resetWorkDir`); `TestPodExecutor_RunCode_ResetsWorkDirPerCandidate` | **proven structurally** |
| A skipped `execute_code` never falls through to tests on a pristine tree | ADR-0100 | `internal/engine/verify.go` (staged flag); `TestCodeJob_NoTestRunWhenExecuteCodeDisallowed` | **proven** |
| Evaluation/test/security/comparison/regulator rejections capture CorrectionRecords | ADR-0100 | `internal/engine/feedback.go`; `TestEvaluationFailureRecordsCorrection` | **proven for the engine sources; `hallucinated_path` still needs a detector** |
| Code prompts follow the planner/repository language | ADR-0101 | `internal/engine/language.go`, `internal/project/language.go`; `TestParseLanguageHint`, `TestDetectLanguageFromCommand`, `TestCodeInstructionNamesLanguage` | **proven for the deterministic paths; a live multi-language run is follow-up** |
| A fixture must live under `fixture_root` or the repository | ADR-0101 | `internal/engine/fixture.go`; `TestReadFixtureTreeRejectsEscapeFromAllowedRoot` | **proven** |
| Multibyte prompts are not under-counted by the token estimator | P0 review | `internal/prompt/prompt.go`; `TestEstimateTokensDeterministic` | **proven** |
| Qualification runs inside the setup wizard with visible percent/progress | first-use review | `internal/ui/onboarding.go`, `internal/ui/onboarding_templates.go`, `internal/ui/setup.go`; `TestWelcomeQualificationShowsInlineProgress`, `TestWelcomeQualificationStartsInline` | **proven** |
| Discovery exposes its live stage, model, bytes, elapsed time, and raw tail | first-use review | `internal/ui/project_onboarding.go`, `internal/discovery/discovery.go`, `cmd/athanor/discovery.go`; `TestProjectOnboardingShowsDiscoveryTransparency`, `TestGenerateReportsDurableProgress` | **proven** |
| Discovery tolerates real local-model output shapes and a bounded 12k output budget | first-use review | `internal/discovery.Parse`; `TestParseSalvagesTruncatedArray`, `TestParseAcceptsConcatenatedObjects`; live clean-Core run 2026-11-05 | **proven** |
| Memory budgets separate host inference from the Job Pod runtime and count only configured personas | ADR-0102 | `internal/ui.budgetFor`; `TestBudgetForSeparatesHostInferenceAndRuntimeSandbox` | **proven** |
| Deep/Full/Lite/Stopped is a persisted household mode with honest resource expectations | ADR-0103 | `internal/operator` (`Status`, `Decide`, `Ceiling`); `TestModeLimits`, `TestTemporaryHoldExpiresToAuto`, `TestCeilingComposition`; dashboard `TestModeControls` | **proven** |
| Stopped pauses active work (resumable), refuses queued work, and resumes explicitly | ADR-0103 | `internal/engine` (`PauseAll`, `ResumeAll`, operator gate); `TestOperatorGateHoldsQueuedWork`, `TestOperatorGateRejectsEnqueue`, `TestOperatorPauseAllAndResumeAll` | **proven** |
| Daydreaming runs only in Full | ADR-0103 | `internal/operator.DaydreamAllowed`; `cmd/athanor/daydream.go` mode gate; `TestModeLimits` | **proven structurally** |
| The model repertoire is a measured, machine-local pool with scoped evidence | ADR-0104 | `internal/composition` (`Pool`, `Score`, `Request`); `TestChooseUsesMeasuredScore`, `TestScopedEvidenceOutvotesOnlyWithEnoughSamples`, `TestPersistenceRoundTrip` | **proven** |
| Phase casting and divergence come from the pool when one exists | ADR-0104 | `internal/engine` (`personaFor`, `SetModelSelector`); `TestSelectorResolvesPhaseAndSyntheticRole`, `TestSelectorCastsDivergenceAcrossModels` | **proven with a fake selector** |
| First use manages pool membership and ranks every model per capability | ADR-0104 | `/ui/welcome?step=models`; `startPoolQualification`; `TestWelcomeModelsSelfSelectAndApply`, `TestWelcomeQualificationStartsInline` | **proven with a fake Ollama** |
| The model pool and ranking remain manageable after onboarding | ADR-0104 | `GET /ui/models`, `POST /ui/models/pool`, `POST /ui/models/qualify`; `TestModelsPageManagesPoolAfterOnboarding` | **proven** |
| Job Pod resources are operator-editable and the Core restarts itself | ADR-0104, ADR-0078 | Resources step; `UpdateJobPodResources`; `Restart` seam; `deploy/core-entrypoint.sh`; `TestWelcomeAppliesResourceLimitsAndRestarts` | **proven in tests; live Core restart is the host check** |
| Deep qualification and a measured machine score are visible | ADR-0104 | `onboard.DeepLadder`, `composition.ScoreMachine`; `/ui/models`; `TestDeepLadderExtendsSmoke`, `TestMachineScoreIsBoundedAndVisible` | **proven** |
| Missing legacy persona models warn instead of blocking a fresh install | ADR-0104 | `doctor.checkOllama` (warn); environment step; `TestRunMissingModel` | **proven** |
| The Models step offers an optional, size-aware core set before qualification | ADR-0104 | `UI.poolSuggestions`; `TestWelcomeModelsSelfSelectAndApply` | **proven** |
| Qualification runs a 41-case smoke screen or 53-case deep screen per model | ADR-0104 | `onboard.DefaultLadder`, `onboard.DeepLadder`; `TestDefaultLadderIsWellFormed`, `TestDeepLadderExtendsSmoke`, `TestDefaultLadderIsBroadEnough` | **proven structurally** |
| A prompt field can be refined against its own contract and reverted | ADR-0105 | `internal/assist`; `POST /ui/assist`; wand/revert in `app.js`; `TestRefineParsesStructuredProposal`, `TestAssistHandler` | **proven in tests; the browser wand is the live check** |
| A field can report “Idealized” or ask clarification instead of guessing | ADR-0105 | `assist.Result.Unchanged/NeedsClarification/Questions`; `TestRefineMarksIdealTextUnchanged`, `TestRefineAsksForClarificationWhenEmpty`, `TestRefineAsksForClarificationWhenVague` | **proven** |
| Low-risk work can explore a runner-up and a measured panel can win | ADR-0104 | `composition.Pool.PreferPanel`, `engine.personaFor` exploration; `TestPreferPanelNeedsEvidence` | **proven structurally** |
| Accepted work exports as a portable fine-tuning dataset | ADR-0104 | `internal/finetune.Export`; `athanor finetune`; `TestExportAcceptedWorkAsJSONL` | **proven** |
| Embedding models are never persona candidates | ADR-0049, ADR-0099 | `onboard.IsChatModel`; `TestEmbeddingModelsAreNotPersonaCandidates` | **proven** |
| `limits.max_concurrent_jobs` is a real ceiling subject to the power profile | ADR-0096 | `power.Ceiling`; `TestCeilingRespectsInteractiveAndOperatorMax` | **proven** |
| Doctor reports a host GPU and does not claim one from inside the Core Pod | ADR-0096, `docs/linux-gpu.md` | `TestRunReportsGPU`, `TestRunReportsGPUNotVisibleFromCore`, `TestRunReportsNoGPU` | **proven with fake probes; host `nvidia-smi` is the live check** |
| Discovery is a portfolio, not a single-winner loop | ADR-0094 | `internal/discovery` (no engine comparison); `TestScoreIsDeterministicAndOrdered` | **proven by structure** |
| A short, honest goal is accepted | ADR-0094 | `goals` 3-character floor; `TestCreateValidation`, `TestSubmitGoal` | **proven** |
| The UI serves embedded assets with a strict CSP and no inline scripts/styles | ADR-0095 | `/ui/assets/*`; `TestAssetsAreEmbeddedAndCached`, `TestCSPAndNoInlineScripts` | **proven** |
| Artifacts and plans are readable Markdown without raw HTML | ADR-0095 | `internal/ui/markdown.go`; `TestRenderMarkdownEscapesHTML`, `TestRenderMarkdownRejectsUnsafeLinks` | **proven** |
| A queued cancel commits `cancelled` and does not underflow the engine cap | ADR-0087, this pass | `internal/engine/control_race_test.go`; `TestCancelQueuedJobCommitsCancelled`, `TestAbandonedWaitDoesNotUnderflowInFlight` | **proven** |
| An external pause during a phase is honored, not failed | this pass | `TestExternalPauseDuringPhaseDoesNotFailJob` | **proven** |
| A UI handler panic is contained to one request | 2026-11-06 UX pass | `internal/server/recover.go`; `TestRecoverMiddlewareContainsPanic`, `TestRecoverMiddlewarePreservesFlusher` | **proven** |
| Request errors use honest statuses and a truthful error page | 2026-11-06 UX pass | `internal/ui/ui.go` (`fail`); `TestMissingArtifactIs404` | **proven** |
| A failed run explains its error and offers recovery | 2026-11-06 UX pass | `internal/ui/ui.go` (`failureReason`); `TestWatchShowsFailure`; illegal pause pinned by `TestWatchHidesIllegalPause` | **proven** |
| The live stream catches events written during a reconnect gap | 2026-11-06 UX pass | `/ui/jobs/{id}/stream?after=`; `TestWatchCarriesLastEventID` | **proven** |
| Approvals render as decisions with fields and expiry, not raw JSON | 2026-11-06 UX pass | `internal/ui/hitl_view.go`; `TestDashboardApprovalIsReadable`, `TestHITLFieldsOrdersKnownKeysFirst`, `TestExpiryLabel` | **proven** |
| Artifacts are presented by kind; code candidates are source, not Markdown | 2026-11-06 UX pass | `internal/ui/artifact_view.go`; `TestCodeProposalRendersAsSource`, `TestArtifactModeFor` | **proven** |
| A multi-file artifact is readable as files and downloadable byte-exactly | 2026-11-06 UX pass | `TestMultiFileArtifactShowsFilesAndBranch`, `TestArtifactDownloadServesExactBytes`; `GET /ui/artifacts/{id}/download` | **proven** |
| Two artifacts can be compared as a structured file diff | 2026-11-06 UX pass | `GET /ui/compare`; `TestCompareArtifacts`, `TestFileDiffsSplitsTrees` | **proven** |
| Search covers tasks and artifacts by project, not only their own labels | 2026-11-06 UX pass | `internal/ui/site.go`; `TestSearchFindsTasksAndArtifactsByProject` | **proven** |
| Repository indexing runs in the background and reports status | 2026-11-06 UX pass | `internal/ui/index.go`; `TestProjectIndexIsAsync` | **proven** |
| Runs and the digest are first-class UI surfaces | 2026-11-06 UX pass | `/ui/jobs`, `/ui/digest`; `TestDailySurfaces` | **proven** |
| Persona routing is editable after onboarding with installed-model validation | 2026-11-06 UX pass | `POST /ui/setup/personas`; `TestSetupPersonasWritesConfig` | **proven** |
| Freeze, active runs, and waiting approvals are visible on every page | 2026-11-06 UX pass | `internal/ui/ui.go` (`statusStrip`); `TestDailySurfaces` | **proven** |
| The installed-model catalog is cached and pooled state is written only on change | 2026-11-06 UX pass | `internal/ui/setup.go` (`modelCatalog`); `TestModelCatalogIsCachedAndInvalidated`, `TestSyncChangedReportsRealChangesOnly` | **proven** |
| Embedded assets revalidate with an ETag instead of downloading every load | 2026-11-06 UX pass | `internal/ui/assets.go`; `TestAssetsUseETag` | **proven** |
| Discovery, planning, pulls, and qualification can be cancelled from the UI | 2026-11-06 backlog pass | `internal/ui` cancel maps; `TestCancelDiscovery` | **proven for discovery; the other three share the same context path and are structurally tested** |
| The watch view reports per-phase duration and token spend | 2026-11-06 backlog pass | `internal/ui/timeline.go`; `TestWatchPhaseTimeline` | **proven** |
| Code is highlighted and line-numbered without injecting markup | 2026-11-06 backlog pass | `internal/ui/highlight.go`; `TestHighlightMarksAndEscapes`, `TestLineNumbers`, `TestHighlightBlockComment` | **proven** |
| Confirmations use a styled dialog, and density/system theme are persisted | 2026-11-06 backlog pass | `internal/ui` shell + Settings; `TestDisplaySettings` | **proven structurally** |
| Recent timestamps are relative and the home surface is Dashboard | 2026-11-06 backlog pass | `internal/ui/ui.go` (`whenLabel`); `TestWhenLabel` | **proven** |
| Artifact bytes are searchable within a bounded scan | 2026-11-06 backlog pass | `internal/ui/site.go` (`contentSnippet`); `TestSearchFindsArtifactContent` | **proven** |
| Per-project execution overrides are editable in the UI | 2026-11-06 backlog pass | `POST /ui/projects/{id}/execution`; `TestProjectExecutionEditor` | **proven** |
| `athanor job watch` streams audit events | 2026-11-06 backlog pass | `cmd/athanor/cli_project.go`; `TestFormatJobEvent` | **proven** |
| The host-reserve warning is fixable on the page that shows it, and returning after restart keeps the step | 2026-11-06 first-use fix | `internal/ui/onboarding_templates.go`; `TestCurrentResourcesWarnsWhenReserveBelowVM`, `TestReserveFixReturnsToModels` | **proven** |
| Qualification is a broader 41-case screen with per-case evidence and a 10-minute bound | `docs/qualification-v2.md` | `internal/onboard/ladder.go`; `TestDefaultLadderIsBroadEnough`, `TestQualDetailsGroupCases` | **proven structurally** |
| The measured pool derives a small/medium/big baseline for wide/main/tall | `docs/qualification-v2.md` | `internal/ui/role_pick.go`; `TestRolePicksUseSizeTiers` | **proven** |
| Resource fields can be fine-tuned without step-validation errors | 2026-11-06 | `internal/ui/onboarding_templates.go` (step 1/64/0.25/8/32) | **proven structurally** |
| A project can be critical/high/medium/low, and at most one project is critical | ADR-0108 | migration 0037; `Project.SetPriority`; `TestProjectPrioritySingleCritical`, `TestProjectPriority` | **proven** |
| Field refinement retries an over-eager clarification and leaves usable text unchanged rather than blocking | ADR-0105 amendment | `internal/assist` (`retryRefine`, `usableFieldText`); `TestRefineRetriesOverEagerClarification`, `TestRefineLeavesUsableTextUnchangedWhenRetryStillClarifies` | **proven** |
| Refinement improves a usable field, forces a second pass when the model echoes, and uses Athanor project/form context | ADR-0105 second amendment | `internal/assist` (`RefineContext`, `forceImprove`, `contextBlock`); `TestRefineForcesImprovementWhenFirstPassEchoes`, `TestRefineContextReachesPrompt`, `TestAssistHandlerPassesFormAndSystemContext` | **proven in tests; the browser wand is the live check** |
| Every advertised theme has a distinct palette/background | 2026-11-06 theme pass | `internal/ui/theme_test.go` (`TestThemeBackgroundsAreUnique`) | **proven** |
| A discrete GPU's model budget is bounded by VRAM, not host RAM, and spill is reported | ADR-0107 | `internal/ui/onboarding.go` (`budgetFor`, `inferenceFitBytes`); `TestBudgetForUsesVRAMOnDiscreteGPU`, `TestInferenceFitBytesClampsToVRAM` | **proven** |
| An Apple unified GPU keeps the host budget and is labeled as unified | ADR-0107 | `internal/ui/onboarding.go`; `TestBudgetForUnifiedGPUUsesHostBudget` | **proven** |
| An NPU is shown as inventory and is never used for inference budgeting | ADR-0107 | `internal/doctor` GPU check; UI accelerator card | **proven** |
| The host accelerator probe reaches the Core Pod | ADR-0107 | `cmd/athanor/start.go`, `coreRunArgs`; `TestCoreRunArgs` | **proven** |
| A host reserve below the detected Podman VM floor is clamped, and the UI cannot save below it | ADR-0106 | `internal/ui/onboarding.go` (`currentResources`, `apply_resources`); `TestReserveFixReturnsToModels`, `TestCurrentResourcesWarnsWhenReserveBelowVM` | **proven** |
| Data array checks are enforced by the engine, not only the offline scorer | ADR-0070 amendment | `internal/verify/data_checks.go`; `TestDataChecksPassthroughAndFailures`, `TestDataChecksAndSchemaBothApply`; `tools/bench/checks_test.go` | **proven** |
| Declared checks are prompt-visible in pinned tier 2 | ADR-0070 amendment | `internal/prompt/assemble.go` (`declaredChecksSection`); `TestDeclaredChecksArePinned`; `TestRun_DeclaredChecksReachPrompt` | **proven** |
| `must_contain_any` is enforced by the engine and carried from the benchmark | ADR-0070 amendment | `internal/verify/checks.go`; `TestChecksVerifier_MustContainAny`; focused v2 refusal task passes | **proven** |
| A job carries a bounded, engine-written notebook into every phase | ADR-0109 | `internal/notebook`; `TestAppendAndRecent`, `TestNotebookReachesLaterPhases`; `TestNotebookIsPinnedAndRendered` | **proven** |
| The candidate count can be selected from observed outcomes, with a fallback until evidence exists | ADR-0110 | `internal/policy/learned.go`; `TestChooseCandidatesPrefersStrongArm`, `TestChooseCandidatesKeepsFallbackWhenUnderSampled`; `TestApplyLearnedSelectionChoosesStrongArm` | **proven** |
| A position-biased pairwise judge cannot decide; its disagreement falls back to pointwise | ADR-0111 | `internal/engine/pairwise.go`; `TestPairwiseJudgeAgreesNew`, `TestPairwiseJudgeDetectsPositionBias` | **proven** |
| A model-initiated clarification parks the job and the answer precedes resume | ADR-0112 | `internal/engine/clarify.go`; `TestPlanningQuestionParksTheJob`; `internal/hitl` `TestPreApproverRunsBeforeResume` | **proven** |
| A code repair can request one repository file, read-only and bounded | ADR-0113 | `read_file` route + envelope; `TestReadFileRejectsTraversal`, `TestReadFileAllowedReturnsBoundedContent`; `TestObserveForRepairReadsOneFileIntoNotebook` | **proven** |
| A completed job leaves a deterministic, project-scoped memo later jobs can retrieve | ADR-0114 | `internal/mce/remember.go`; `TestRememberJobSummaryIsRetrievableAndIdempotent`; `TestCompletedJobWritesMemorySummary` | **proven** |
| A regulator reframe records a new current plan in the notebook | ADR-0115 | `internal/engine/reflect.go` (`replanned`); `internal/prompt` `TestNotebookIsPinnedAndRendered`; adr/0115 | **proven structurally** |
| The benchmark reports whether the new loop mechanisms actually ran | `docs/probes/ceiling-diagnostic.md` | `tools/bench/telemetry.go`; `TestFillTelemetry`; `make diagnostic` | **instrumented; a live diagnostic run is the next measurement** |
| A diagnostic task can be a multi-goal sequence with checkpoint progress | ADR-0116 | `tools/bench/sequence.go`; `diag-sequence-memory`; `sequence[]` / `progress` in results | **measured: full 0/3, single 1/3 over 3 passes each; after the Markdown contract fix, full 3/3** |
| Extra compute may cost more but must not lower quality | ADR-0117 | `bestPassingCandidate` best-score fallback; `shouldFallbackToCandidate`; `TestBestPassingCandidateFallsBackToBestScore`, `TestShouldFallbackToCandidate`; paired-report quality flag | **proven structurally; paired report flags regressions** |
| A job can try one candidate first and escalate only after failure | ADR-0118 | `oneShotFirst`; `TestOneShotFirstOnlyOnFirstCycle`; benchmark `cascade` arm / `make diagnostic-cascade`; sequence run 3/3 at 17.1k tokens vs full 37.8k | **measured: same quality at 2.2× lower cost** |
| Every job self-evaluates its whole process, not only its artifact | ADR-0119 | `internal/process`; `process_eval` audit row; `TestEvaluate`, `TestRunWritesProcessEvaluation` | **proven** |
| v2 is a one-shot-friendly floor, not evidence that iteration beats one-shot | ADR-0116, v3 plan | saturated tasks are separated; paired single-shot control is required in v3 | **documented; v3 harness is planned** |
| The five-task document regression is closed on the current prompt/check contract | [bench-v2-doc-rerun-20261106](probes/bench-v2-doc-rerun-20261106.md) | focused v2 rerun, one pass, 5/5 | **measured (one pass, host path)** |
| A 24/7 Core bounds in-memory run history instead of retaining every session | 2026-11-06 hardening pass | `internal/ui/runs.go` (`pruneRuns`); `TestPruneRunsDropsOldestCompleted`, `TestPruneRunsNeverDropsRunning` | **proven** |
| Every daemon request body is capped before a handler decodes it | 2026-11-06 hardening pass | `internal/server/body_limit.go`; `TestBodyLimitMiddlewareRejectsOversize` | **proven** |

| A candidate cannot modify the test harness that judges it | ADR-0120 | `internal/toolenvelope/harness.go`; `TestIsHarnessPath`, `TestSplitHarnessFilesPreservesOrder`; git filter tests | **proven** |
| Every terminal job uses one finalization path | ADR-0121 | `Engine.FinalizeTerminal`; `internal/hitl` observer test; `recover_pause_test.go` | **proven** |
| Parked jobs do not retain a Job Pod indefinitely | ADR-0121 | `Engine.ReapIdlePods`; `TestReapIdlePodsStopsParkedJobs`; five-minute serve reaper | **proven structurally** |
| Comparison recovers after acceptance without self-superseding | ADR-0121 | `TestPhaseCompareRecoversAfterAcceptance` | **proven** |
| Strategy outcomes record the actual candidate arm, retries, and reflection loops | ADR-0122 | migration 0039; `TestFinalizeCapturesReflectionCandidatesAndRetries`; `TestCandidateStatsUsesActualOutcomeArm` | **proven** |
| Cross-family judgment uses the actual pool cast | ADR-0122 | `internal/engine/pool_family_test.go` | **proven** |
| The Core Pod can reach a host repository | ADR-0123 | `~/athanor/repos` same-path mount; `TestCoreRunArgs`, `TestCoreMountsMatch`; repository path validation | **proven structurally** |
| A stale Core container is recreated for a changed image or mount contract | ADR-0123 | `cmd/athanor/core.go`; `TestCoreMountsMatch` | **proven structurally** |
| The machine plan names solo/swarm slots and refuses a swarm that does not fit | ADR-0124 | `internal/capacity`; `TestM2MaxPlansSoloAndSwarm`, `TestNoModelFits`, `TestLightSwarmOnSmallMachine` | **proven structurally** |
| One-model and missing-component machines degrade honestly | ADR-0124 | `TestOneModelMachineIsHonest`, `TestCoverageRequiresFitAtFloor`, `TestLiteModeUsesLowerFloors` | **proven structurally** |
| A swarm only accepts tasks smaller than the solo capability | ADR-0124 | `EligibleForSwarm`; `TestEligibleForSwarmRequiresSmallerTasks`, `TestMissionTooLargeDisablesSwarm`, `TestMissionReservesOneSlotTotal` | **proven structurally** |
| Reads proceed while a job writes; writes stay serialized | ADR-0125 | `internal/store` read pool; `TestReadPoolProceedsDuringWrite`, `TestReadPoolRefusesWrites`, `TestConcurrentWritersSerialize` | **proven** |
| A benchmark headline cannot be a host-binary run | ADR-0126 | `tools/bench/runmeta.go`; `TestResolveRuntime`, `TestRunHeaderLabelsHostAsDiagnostic`; `report -headline` | **proven structurally** |
| Division spans the full generation language set | ADR-0128 | `internal/mce/division`; P1–P5 corpus over ten languages; `TestDivideNewLanguagesUseASTAndTile` | **proven** |
| Superseded architecture wording and tool/ADR drift fail the build | 2026-11-07 consistency gate | `internal/docs/consistency_test.go` | **proven** |
| Old audit events are archived before deletion under a guarded trigger | ADR-0127 | `internal/retention`; `TestArchiveEventsMovesOldRows`, `TestGuardStillBlocksNormalDelete`, `TestPruneArchivesByAge` | **proven** |
| `models -audit` reports solo/swarm capacity and coverage reasons | ADR-0124 | `internal/onboard/capacity.go`; `TestCapacityReportFromAudit`, `TestCapacityReportDegradesWithOneModel` | **proven structurally** |

## Rule

When a code or behavior change falsifies a row, update the row in the same
commit (or remove the claim). Do not let a "target" row silently read as
"proven".
