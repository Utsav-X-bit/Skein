# Graph Report - Skein  (2026-11-12)

## Corpus Check
- 429 files · ~680,926 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 3020 nodes · 9619 edges · 35 communities detected
- Extraction: 46% EXTRACTED · 54% INFERRED · 0% AMBIGUOUS · INFERRED: 5231 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 19|Community 19]]
- [[_COMMUNITY_Community 20|Community 20]]
- [[_COMMUNITY_Community 21|Community 21]]
- [[_COMMUNITY_Community 22|Community 22]]
- [[_COMMUNITY_Community 23|Community 23]]
- [[_COMMUNITY_Community 24|Community 24]]
- [[_COMMUNITY_Community 25|Community 25]]
- [[_COMMUNITY_Community 26|Community 26]]
- [[_COMMUNITY_Community 27|Community 27]]
- [[_COMMUNITY_Community 28|Community 28]]
- [[_COMMUNITY_Community 29|Community 29]]
- [[_COMMUNITY_Community 30|Community 30]]
- [[_COMMUNITY_Community 35|Community 35]]
- [[_COMMUNITY_Community 37|Community 37]]
- [[_COMMUNITY_Community 70|Community 70]]
- [[_COMMUNITY_Community 78|Community 78]]
- [[_COMMUNITY_Community 79|Community 79]]

## God Nodes (most connected - your core abstractions)
1. `New()` - 209 edges
2. `Daemon` - 74 edges
3. `writeFile()` - 62 edges
4. `do()` - 62 edges
5. `Generate()` - 61 edges
6. `dashboard` - 59 edges
7. `Open()` - 53 edges
8. `Load()` - 51 edges
9. `Server` - 49 edges
10. `Engine` - 47 edges

## Surprising Connections (you probably didn't know these)
- `run()` --calls--> `GrantUserRoleRoot()`  [INFERRED]
  cmd/skein-helper/main.go → internal/app/grant_windows.go
- `fuzzyScore()` --calls--> `min()`  [INFERRED]
  gui/frontend/src/components/layout/palette.ts → internal/cli/dashboard_render.go
- `main()` --calls--> `Load()`  [INFERRED]
  gui/main.go → internal/identity/identity.go
- `main()` --calls--> `Store`  [INFERRED]
  gui/main.go → internal/configstore/configstore.go
- `run()` --calls--> `ApplyUpdateDetached()`  [INFERRED]
  cmd/skein-helper/main.go → internal/daemon/update.go

## Communities

### Community 0 - "Community 0"
Cohesion: 0.02
Nodes (78): Cache, cacheKey, Sample, Transport, Hub, syncRecorder, newControlHandoff(), TestBootstrapHandoffFailureReturns503() (+70 more)

### Community 1 - "Community 1"
Cohesion: 0.02
Nodes (210): StatusError, accessGrant, RevokeOutcome, serviceHandler, mintBootstrapToken(), TestCancelBootstrapTokenIsLockedAndTokenBound(), TestGrantSecretPreservesOtherGrants(), TestMintBootstrapTokenAllowsPreservedIdentity() (+202 more)

### Community 2 - "Community 2"
Cohesion: 0.03
Nodes (127): label(), activityRow, canvas, cell, dashboard, dashboardFocus, dashboardLayout, dashboardMode (+119 more)

### Community 3 - "Community 3"
Cohesion: 0.02
Nodes (147): adminSIDs(), dirAdminOnly(), getDacl(), noWorldAccess(), walkDacl(), dedupeBootstrapOverlays(), checkImportParents(), loadImportSource() (+139 more)

### Community 4 - "Community 4"
Cohesion: 0.03
Nodes (80): peerCredConn, peerCredListener, TestBootstrapPipeReadsOneEnvelope(), waitBootstrapPlane(), TestLocalCandidatesExcludeLoopback(), chanErr(), encodeToken(), TestConnErrorKeepsControlPlaneAlive() (+72 more)

### Community 5 - "Community 5"
Cohesion: 0.04
Nodes (73): setFilter(), AuditEvent, Caller, callerKey, callerLimiter, capRecorder, fakeGUI, fakeProber (+65 more)

### Community 6 - "Community 6"
Cohesion: 0.02
Nodes (113): hubEvent, hubSub, New(), spawnDetached(), spawnDetached(), openPipeSession(), openPipeSession(), validateInterfaceName() (+105 more)

### Community 7 - "Community 7"
Cohesion: 0.05
Nodes (117): onKeyDown(), run(), errFakeType, fixture, punchCall, punchRequest, sentFrame, mustIdentity() (+109 more)

### Community 8 - "Community 8"
Cohesion: 0.03
Nodes (97): BackupResult, BootstrapResult, grantBootstrapTransportAccess(), ExcludePrefixes(), activityDTO, allCandidates(), client, clientFor() (+89 more)

### Community 9 - "Community 9"
Cohesion: 0.05
Nodes (75): Record, NewManager(), IsLocked(), TestConcurrentGoroutineWritersProduceOneValidConfig(), TestConfigstoreHelperProcess(), TestLockHeldAcrossProcesses(), TestLockHeldBlocksSecondWriter(), TestWriteRoundTripsAndRejectsInvalid() (+67 more)

### Community 10 - "Community 10"
Cohesion: 0.03
Nodes (44): bootstrapCall(), ControlClient(), stubDiagnosticsBundle(), stubSaveDialog(), TestExportBundleCancelledWritesNothing(), TestExportBundleIncludesRedactions(), TestExportBundleTightensExistingFileMode(), TestExportBundleWriteFailureIsAnError() (+36 more)

### Community 11 - "Community 11"
Cohesion: 0.05
Nodes (76): handlerFunc, cmdReset(), fakeSystemDaemon, setupRunner, joinLine(), nodeID(), resetFixture(), runCLI() (+68 more)

### Community 12 - "Community 12"
Cohesion: 0.05
Nodes (76): itoa(), degraded(), ensure(), ensureInterface(), ensurePort(), expectedRules(), firewalldActive(), firewalldRepair() (+68 more)

### Community 13 - "Community 13"
Cohesion: 0.04
Nodes (49): parseBootstrapToken(), createBootstrapPipe(), currentProcessElevated(), mintBootstrapViaPipe(), verifyBootstrapClient(), mintBootstrapViaHelper(), mintBootstrapViaHelper(), expectedDaemonImage() (+41 more)

### Community 15 - "Community 15"
Cohesion: 0.06
Nodes (51): fakeTUN, fakeUpdateService, realUpdateService, semver, UpdateManifest, updateService, NetworkInvite, attachEngine() (+43 more)

### Community 16 - "Community 16"
Cohesion: 0.04
Nodes (43): Eligible(), Family(), Filter(), LocalCandidates(), TestEligible(), TestExcludePrefixesDropsOverlayAddresses(), TestFilterNormalizesAndDeduplicates(), TestProberBoundedConcurrency() (+35 more)

### Community 17 - "Community 17"
Cohesion: 0.06
Nodes (44): Role, checkSystemDevTCP(), Run(), runWithService(), TestAccessGrantsParsing(), bootstrapAccessPath(), grantBootstrapTransportAccess(), BootstrapStatePath() (+36 more)

### Community 18 - "Community 18"
Cohesion: 0.1
Nodes (38): fakeTUN, hub, node, testNet, BenchmarkEngineThroughput(), ipv4Packet(), ipv6Packet(), merge() (+30 more)

### Community 19 - "Community 19"
Cohesion: 0.05
Nodes (29): tick(), errorLine(), elevationCancelled(), errDisplay(), errKind(), errText(), isOffline(), CommandBar() (+21 more)

### Community 20 - "Community 20"
Cohesion: 0.07
Nodes (42): TestSetupConfiguresTheFirewall(), call, classifyInterfaceError(), Config, Ensure(), EnsureInterface(), EnsurePort(), fakeRunner (+34 more)

### Community 21 - "Community 21"
Cohesion: 0.07
Nodes (29): oneShotTransport, pipeAddr, pipeConn, pipeListener, createPipeInstance(), driveStripped(), filepathBase(), matchServerImage() (+21 more)

### Community 22 - "Community 22"
Cohesion: 0.1
Nodes (14): MDNS, peerSpec, build(), containsLine(), moduleRoot(), peersUp(), receivedPackets(), run() (+6 more)

### Community 23 - "Community 23"
Cohesion: 0.11
Nodes (17): AccessGrantsView, APIError, CallerSecurity, Capabilities, ConfigPatch, ConfigPatchResult, Connection, DiagnosticBundle (+9 more)

### Community 24 - "Community 24"
Cohesion: 0.18
Nodes (6): usePeerParam(), useWideLayout(), InspectorFrame(), nameOf(), shortID(), routeSummary()

### Community 25 - "Community 25"
Cohesion: 0.2
Nodes (4): log(), formatBytes(), formatRate(), timeAgo()

### Community 26 - "Community 26"
Cohesion: 0.18
Nodes (10): ImportResult, JoinResult, LeaveResult, ResetOptions, ResetResult, RotateResult, SystemOperator, UpdateApplyResult (+2 more)

### Community 27 - "Community 27"
Cohesion: 0.2
Nodes (1): fakeSystemState

### Community 28 - "Community 28"
Cohesion: 0.22
Nodes (8): Activity, AddPeerRequest, ConfigView, Event, Peer, Route, SetNameRequest, Status

### Community 29 - "Community 29"
Cohesion: 0.39
Nodes (5): allow(), grantUser(), restrict(), restrictDACL(), root()

### Community 30 - "Community 30"
Cohesion: 0.53
Nodes (4): Fail(), Install-PinnedWails(), Install-WingetPackage(), Refresh-Path()

### Community 35 - "Community 35"
Cohesion: 0.67
Nodes (2): record(), sample()

### Community 37 - "Community 37"
Cohesion: 0.5
Nodes (1): securityEnv()

### Community 70 - "Community 70"
Cohesion: 1.0
Nodes (1): Link

### Community 78 - "Community 78"
Cohesion: 1.0
Nodes (1): connectionRequest

### Community 79 - "Community 79"
Cohesion: 1.0
Nodes (1): logPage

## Knowledge Gaps
- **154 isolated node(s):** `options`, `bootstrapEnvelope`, `PingResult`, `helperEnvelope`, `OfflineIdentity` (+149 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **Thin community `Community 27`** (10 nodes): `fakeSystemState`, `.LeaveSystem()`, `.ReloadGrants()`, `.ResetSystem()`, `.RotateSecret()`, `.SystemImport()`, `.SystemInit()`, `.SystemJoin()`, `.UpdateApply()`, `.UpdateCheck()`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 35`** (4 nodes): `useSessionHistory.test.ts`, `record()`, `sample()`, `samples()`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 37`** (4 nodes): `securityEnv()`, `env_release.go`, `env_release.go`, `env_release.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 70`** (2 nodes): `link.go`, `Link`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 78`** (2 nodes): `connectionRequest`, `handlers_connection.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 79`** (2 nodes): `logPage`, `handlers_logs.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `Community 6` to `Community 0`, `Community 1`, `Community 2`, `Community 3`, `Community 4`, `Community 5`, `Community 7`, `Community 8`, `Community 9`, `Community 10`, `Community 11`, `Community 12`, `Community 13`, `Community 15`, `Community 16`, `Community 17`, `Community 18`, `Community 20`, `Community 21`, `Community 22`?**
  _High betweenness centrality (0.096) - this node is a cross-community bridge._
- **Why does `do()` connect `Community 10` to `Community 0`, `Community 1`, `Community 2`, `Community 4`, `Community 5`, `Community 6`, `Community 17`, `Community 21`, `Community 22`?**
  _High betweenness centrality (0.037) - this node is a cross-community bridge._
- **Why does `NewTable()` connect `Community 7` to `Community 0`, `Community 18`, `Community 6`?**
  _High betweenness centrality (0.015) - this node is a cross-community bridge._
- **Are the 204 inferred relationships involving `New()` (e.g. with `openPipeSession()` and `.readRequest()`) actually correct?**
  _`New()` has 204 INFERRED edges - model-reasoned connections that need verification._
- **Are the 53 inferred relationships involving `writeFile()` (e.g. with `TestGenerateOfflineRefusesExistingConfig()` and `TestHistorySamplesHideRowsPastRetention()`) actually correct?**
  _`writeFile()` has 53 INFERRED edges - model-reasoned connections that need verification._
- **Are the 57 inferred relationships involving `do()` (e.g. with `bootstrapCall()` and `.Logs()`) actually correct?**
  _`do()` has 57 INFERRED edges - model-reasoned connections that need verification._
- **Are the 59 inferred relationships involving `Generate()` (e.g. with `.GenerateOffline()` and `TestGenerateOfflineReusesExistingKey()`) actually correct?**
  _`Generate()` has 59 INFERRED edges - model-reasoned connections that need verification._