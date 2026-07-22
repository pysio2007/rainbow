# IPFS Inspector Expansion

## Goal

Implement the requested public-gateway inspector scope: root CID inspection,
bounded DAG inspection, IPNS inspection, retrieval diagnostics, CAR inspection
links/validation, and Explorer metadata/navigation improvements.

## Confirmed Boundaries

- Rainbow remains a read-only public gateway: no accounts, pinning, uploads,
  global content index, or arbitrary remote probe URLs.
- Provider inspection stays discovery-only and never dials peers.
- Expensive work is bounded by explicit size, depth, node, result, timeout, and
  concurrency limits.
- CAR inspection applies to a bounded CAR response produced by this gateway; it
  does not accept arbitrary remote URLs or unbounded user uploads.
- UI language must distinguish observed/local/verified states from global
  availability claims.

## Confirmed Research

- Current MPA entries and server mapping: `webui/vite.config.ts`, `handlers.go`.
- Current provider SSE limits and DHT-only discovery: `providers.go`,
  `providers_cache.go`, `setup_routing.go`.
- Existing resolver, namesys, gateway backends, and CAR support: `setup.go`,
  `handlers.go`.
- Current Explorer metadata and directory surface: `webui/src/pages/explorer.tsx`,
  `webui/src/lib/normalizer.ts`.
- Official source constraints: CID/multihash parsing does not verify block bytes;
  CAR hash verification does not prove DAG completeness; IPNS TTL is bounded by
  record expiry; provider records are observations, not availability guarantees.

## Plan And Review Gates

1. Contract and verification design
   - Define bounded API contracts, data models, fixtures, and test seams.
   - Oracle review 1: validate public-gateway safety and protocol semantics.
2. Root Inspector foundation
   - Implement one-root metadata/direct-links/passive-observation API and its
     Inspector MPA plus Explorer local-data improvements.
   - Oracle review 2: assess resource controls, correctness, route wiring, and
     cross-entry UI integration.
3. Extended protocol contract
   - Freeze bounded DAG, validated IPNS, fixed-scope CAR, and retrieval-page
     contracts against the accepted root foundation.
   - Oracle review 3: validate protocol claims, resource controls, and
     diagnostic privacy before code is written.
4. Extended bounded inspection
   - Implement accepted DAG, IPNS, CAR, and retrieval-presentation behavior in
     the backend and MPA surfaces.
   - Oracle review 4: assess implementation correctness and integration risk.
5. End-to-end validation
   - Run project suites, build embedded assets, and browser-test desktop/mobile.

## Verification Claims

- Every inspector endpoint rejects malformed/oversized input and respects its
  bounded traversal/response contract.
- CID inspection labels parsing separately from fetched-byte verification.
- DHT/IPNS/retrieval views state uncertainty and unavailable capability clearly.
- MPA direct URLs and cross-entry navigation work from the embedded Go server.

## Reconciled Discovery

- `Node` already owns `bsrv`, `resolver`, `vs`, `ns`, and DHT-only
  `contentDiscovery` (`setup.go`). Root inspection can use the block service;
  it must recompute the CID multihash over retrieved bytes before claiming
  verification.
- Existing bounded directory handler patterns in `handlers.go` and fixtures in
  `handler_test.go` / `main_test.go` are the preferred model for timeouts,
  semaphores, output caps, and real UnixFS fixtures.
- Root DAG-PB/UnixFS decoding can expose only direct links and root fields. DAG
  `Tsize`, CID parseability, a CAR header root, and provider records must not
  be presented as global completeness or availability proofs.
- IPNS raw records require `ipns.UnmarshalRecord` followed by
  `ipns.ValidateWithName`; DNSLink and resolved namesys results are separate
  concepts. The existing `vs`/`ns` fields support a bounded summary API.
- CAR verification must use a bounded CARv1 response from the locally
  configured gateway backend, verify every read block hash, verify requested
  root declaration and presence, and state that DAG completeness is unknown.
- Boxo retrieval state is request-scoped and may contain peer samples. A
  public diagnostic response may expose only phase/count/root/terminal fields,
  never raw peer addresses or a user-selected probe URL.
- UI design uses independent Inspector MPA entries with compact, explicit
  observation/unknown/truncated states. Explorer improvements remain local to
  already-loaded directory entries.

## Tentative Public Contracts For Oracle Gate 1

- `GET /_rainbow/api/v1/metadata?cid=`: one CID, root block only, 2s timeout,
  1 MiB decoded block/JSON bound; returns codec, verified byte status, bounded
  UnixFS metadata, and direct link count.
- `GET /_rainbow/api/v1/dag?cid=&depth=`: depth 0..2, at most 128 nodes, 256
  links per node, 2 MiB decoded bytes, 3s timeout; result includes `truncated`.
- `GET /_rainbow/api/v1/ipns/<name>/summary` and `/resolve`: name <=256 bytes,
  3s timeout, validation before returned record fields, resolve depth one.
- `GET /_rainbow/api/v1/car/verify?cid=`: gateway-generated CARv1 block scope
  only, bounded total bytes, root/section/hash validation, no arbitrary URL,
  upload, selector, or all-DAG scope.
- `GET /_rainbow/api/v1/retrieval?cid=`: root-only, bounded observation
  summary. Its implementation must not create an internal HTTP loop; exact
  backend integration remains an Oracle gate decision.

## Oracle Gate 1 Decision

- Accepted for the foundation: one `GET /_rainbow/api/v1/metadata?cid=`
  endpoint and one Inspector MPA. It accepts exactly one canonical CID and
  fetches at most one root block.
- Metadata response includes parsed CID details, root byte verification,
  bounded UnixFS root metadata, at most 64 direct links, canonical links, and
  passive retrieval observation from that same operation.
- Foundation limits: two global jobs, per-client burst 2/sustained 6 per
  minute, 2s outer timeout, 512 KiB post-fetch parse cap, 64 KiB JSON cap,
  64 links, 256-byte names, and `no-store` response caching.
- Direct links replace a public depth parameter for the foundation. No
  recursive traversal, selector, upload, arbitrary URL, internal HTTP loop,
  peer address, raw backend error, or provider probing is allowed.
- Root byte verification means only retrieved root bytes match the CID. UnixFS
  declared size and DAG-PB `Tsize` remain declarations, not DAG completeness.
- Remote-CAR mode must return parsed CID/link data with
  `root.status=unsupported_mode`; it must not compensate by requesting the
  local gateway over HTTP.
- IPNS resolve, validated record summary, fixed-scope CAR verification, and
  bounded DAG expansion are deliberately Phase 3 contracts after foundation
  validation. Retrieval has no standalone probe endpoint.

## Phase 2 Delivery Under Review

- Backend delivery: `metadata.go`, `metadata_test.go`, `handlers.go`, and
  `CHANGELOG.md`. It adds a root metadata API and embedded `/inspect/*` route
  mapping without changing existing Provider behavior.
- UI delivery: `webui/inspect/index.html`, Inspector entry/routes/page, Inspector
  normalizers/tests, Header/SearchBox actions, Explorer local entry filtering and
  sorting, Vite input, and generated `webui/dist`.
- Specialist-reported focused validation: Go tests, WebUI tests, and WebUI build
  passed. Coordinator validation and Oracle Gate 2 remain required.
- Reconciliation risk to review: backend emits `root.unixfs` as structured
  metadata and retrieval `outcome`/`phase`/count fields, while the initial UI
  normalizer may be expecting different shapes. The final wire contract must be
  unified before this phase is accepted.

## Phase 2 Validation

- Coordinator ran `make test`: Go suite passed and WebUI reported 11 passing
  test files / 82 tests.
- Coordinator ran `make build`: Vite emitted `dist/inspect/index.html` and the
  embedded Go binary built successfully.
- `git diff --check` passed.
- Gate 2 must still examine the actual Go JSON structs and
  `webui/src/lib/inspector.ts` together; compilation does not establish wire
  compatibility or public diagnostic semantics.

## Oracle Gate 2 Result: Rejected Pending One Remediation Batch

- P0: Go response structs and WebUI normalization disagree on structured
  UnixFS, retrieval fields, and unnamed direct links. Tests use a UI-only
  fixture instead of the actual wire shape.
- P0: `webui/.gitignore` ignores `dist`, while `.github/workflows/webui.yml`
  explicitly requires tracked `webui/dist`. The generated-entry policy must be
  reconciled.
- P1: remote-CAR mode can invoke a non-nil block service rather than returning
  `unsupported_mode` without fetching.
- P1: retrieval output is fabricated around `GetBlock`; no request-scoped
  Boxo state is attached. Foundation must remove it rather than claim passive
  diagnostics.
- P2: root block cap is post-fetch only and must remain described that way.
- P2: Explorer controls do not reset on a new path, and its current action
  targets a resolved CID rather than the original path root.

## Gate 2 Remediation Scope

1. Establish metadata schema v1 with exactly `version`, `parsedCid`, `root`,
   and `canonicalLinks`. `root.directLinks` is the single direct-link shape;
   `root.unixfs` remains structured; optional link names remain optional.
2. Remove retrieval output from foundation Go/UI rather than exposing a fake
   observation. A later phase owns real diagnostics.
3. Pass remote-CAR mode explicitly to metadata construction and assert it does
   not call a block getter.
4. Repair Explorer local-state reset and make its inspector action use the
   original `/ipfs/<root>` CID.
5. Remove the `webui/dist` ignore rule and regenerate all MPA artifacts so the
   final worktree contains assets for CI to track.
6. Add exact schema fixtures to both Go and UI tests, embedded route checks,
   then validate and request a focused Gate 2 follow-up review.

## Gate 2 Remediation Progress

- Backend now emits schema v1 only, removes foundation retrieval claims, and
  treats remote-CAR mode as unsupported before any block getter call.
- WebUI now consumes structured UnixFS and root direct links, resets Explorer
  local controls on path changes, and derives Inspector root actions from the
  original IPFS path.
- A final schema mismatch was found during coordinator source review: Go emits
  UnixFS `mtime` as Unix seconds. WebUI now accepts only finite numeric seconds
  and renders explicit UTC text; its exact fixture was corrected accordingly.
- `webui/.gitignore` no longer ignores `dist`; the regenerated assets are now
  intentionally untracked pending the normal project commit that records the
  source and generated output together.

## Gate 2 Remediation Validation

- Coordinator reran `make test`: Go suite passed; WebUI reported 12 passing
  test files / 86 tests.
- Coordinator reran `make build`: Inspector HTML and its hashed assets were
  generated and the embedded daemon compiled successfully.
- A DHT-disabled isolated daemon returned an actual metadata v1 JSON response
  for a valid CID with a bounded `timeout` root state. Browser navigation to
  `/inspect/<cid>` rendered Root metadata and canonical actions with no page
  errors or horizontal overflow.
- The live response matched schema v1 and did not include retrieval fields.
- Remaining integration check: emulate the CI tracked-dist assertion in a
  temporary Git index without altering the user's real index.

## Gate 2 Dist Policy Validation

- A temporary Git index staged the updated `webui/.gitignore` and regenerated
  `webui/dist` only. In that index, the workflow's tracked-dist assertions
  passed: assets were tracked, worktree and index matched, and no dist assets
  remained untracked. The real user index was not changed.

## Gate 2 Follow-Up

- Follow-up review found no remaining protocol or UI P1 issue.
- The sole P0 is delivery mechanics: the real index must track regenerated
  `webui/dist` together with the ignore-rule change and foundation source
  changes. The next action stages only those intended feature files; it does
  not create a commit.

## Gate 2 Accepted

- The intended Inspector source, ignore-rule change, and generated assets are
  now staged without a commit. The real-index tracked-dist CI assertions pass.
- Root Inspector source diffs pass whitespace validation. A third-party Vite
  generated bundle contains template-literal trailing spaces; the repository's
  CI checks generated-output consistency rather than whitespace linting, so it
  is recorded as a build artifact limitation rather than hand-editing hashed
  output and invalidating compressed siblings.

## Extended Phase Contract Gate

- The user requested bounded DAG inspection, IPNS inspection, retrieval
  diagnostics, and CAR verification. These introduce materially different
  protocol and resource risks, so a dedicated contract gate precedes their
  implementation and a final Oracle review follows the phase.
- Candidate safe shape: bounded DAG depth 1..2; validated peer-ID IPNS resolve
  and record summary; fixed block-scope locally generated CAR verification;
  a UI retrieval page that combines independently observed metadata and
  existing provider SSE without an internal HTTP loop or fake telemetry.

## Extended Contract Discovery

- DAG traversal must use an explicit depth-aware queue and CID visited set. The
  installed Boxo walker has no depth contract and must not be run then filtered.
  Root depth 0, children depth 1, and grandchildren depth 2 are distinct
  operations; unknown descendants remain unrequested.
- Existing `Node` fields (`bsrv`, `vs`, `ns`) are enough for block traversal and
  IPNS. Traversal must remain unsupported in remote-CAR mode; IPNS can remain
  available through a configured value store in DHT-off/remote modes.
- IPNS summary requires peer-ID names only, raw record retrieval, unmarshal,
  `ValidateWithName`, and bounded selection before exposing value, sequence,
  EOL, and TTL. DNSLink and raw signatures are outside this API.
- CAR verification requires a narrow `GetCAR` backend dependency, fixed
  `DagScopeBlock`, CARv1, one declared/requested root, bounded stream/section,
  verified block CID hashes, and clean EOF. It cannot claim DAG completeness.
- No globally queryable Boxo retrieval state exists. The retrieval MPA must
  compose the metadata root outcome with the existing provider SSE observation,
  label them as separate requests, and make no phase/dial/success claim.
- Candidate paths: `/api/v1/dag?cid=&depth=`, `/api/v1/ipns?name=`, and
  `/api/v1/car/check?cid=`. Each has a separate cost class and concurrency
  budget; metadata/provider limits must not be silently reused as a shared
  unlimited pool.

## Oracle Gate 3 Decision

- DAG endpoint: `GET /_rainbow/api/v1/dag?cid=<canonical>&depth=1|2`, BFS,
  16 nodes including root, 4 emitted/expanded links per node, 256 KiB
  post-fetch block parse cap, 1 MiB aggregate parsed bytes, 3s timeout, one
  shared heavy job, and separate 2/min burst-1 client policy. It reports only
  bounded in-block links and never DAG completeness.
- IPNS endpoint: `GET /_rainbow/api/v1/ipns?name=<peer-id>`, one
  `ValueStore.GetValue` then unmarshal and `ValidateWithName`; no `SearchValue`,
  recursive resolve, DNSLink, raw signature/key, or target fetch. Response
  reports a selected configured-store record, not global freshness.
- CAR endpoint: `GET /_rainbow/api/v1/car/check?cid=<canonical>`, fixed CARv1
  block scope, one declared/requested root, one verified root block, clean EOF,
  fixed header/section/stream limits, and 1/min burst-1 heavy admission.
  Remote-CAR mode is rejected before `GetCAR`; local and remote-block modes are
  allowed.
- Retrieval MPA: `/retrieval/` and `/retrieval/:cid`; no new retrieval API.
  It composes a metadata root observation and explicit existing Provider SSE
  summary, labels them as separate requests, and never claims dialing,
  transfer source, phase, persistence, or global availability.
- New APIs are GET JSON/no-store only; invalid input 400, wrong method 405,
  admission failure 429, and valid requests always receive a versioned 200
  body with normalized outcome status.
- Phase 4 must keep DAG/IPNS/CAR admission pools distinct from metadata and
  providers. Gate 4 reviews implementation, integration, and privacy.

## Phase 4 Delivery Under Review

- Backend delivery: `dag.go`, `ipns.go`, `car.go`, focused tests, handler
  routes, retrieval MPA mapping, direct `go-car` dependency, and changelog.
- UI delivery: Inspector DAG/IPNS/CAR views, Retrieval MPA, strict endpoint
  normalizers/fixtures, MPA entries, and regenerated assets.
- Coordinator ran `make test`: Go suite passed; WebUI reported 15 passing test
  files / 92 tests. `make build` emitted Inspector and Retrieval entries and
  rebuilt the embedded daemon.
- Gate 4 must confirm caps and semantic claims against actual code; passing
  fixtures alone do not establish CAR single-section, DAG-CBOR decode, IPNS,
  or retrieval privacy boundaries.

## Oracle Gate 4 Result: Rejected Pending One Remediation Batch

- P0: the real `webui/.gitignore` again ignores `dist`, and no generated assets
  are tracked. This violates embedded build and CI delivery requirements.
- P0: Go emits boolean DAG `limitsHit` and numeric CAR `carVersion`, while the
  frontend expects a string array and string. Existing UI fixtures are not
  actual server shapes.
- P1: DAG-CBOR uses the default decoder before applying local nesting/visit
  checks; DAG-PB also fully parses links before its displayed-link cap.
- P1: CAR returns `verified` for a root block plus extra valid sections; the
  fixed block-scope contract requires exactly one section/root block.
- P1: `/retrieval/` has no start-route React match, and its copy incorrectly
  promises no transfer/dial even though metadata may invoke block retrieval.
- IPNS validation, single `GetValue`, peer-ID canonicalization, expiry
  handling, and the separate admission pools were accepted.

## Gate 4 Remediation Scope

1. Restore the unignored, tracked generated-output policy and regenerate/stage
   all current assets at final integration.
2. Make the Go DAG schema authoritative with named limit reasons, and make the
   UI consume numeric CAR version plus exact Go JSON fixtures.
3. Apply real pre-decode DAG-CBOR options and a bounded DAG-PB link parsing
   strategy; prove limits with tests.
4. Require exactly one CAR section/root block before `verified`.
5. Add a Retrieval start route/CID entry and correct copy to describe possible
   root retrieval and separate provider discovery.

## Gate 4 Decode Research

- `dagcbor.DecodeOptions` supports `AllowLinks`, `AllocationBudget`,
  `MaxCollectionPrealloc`, and `MaxDepth`. With `basicnode.Prototype.Any`,
  these options use the generic bounded decoder in the pinned release.
- DAG-CBOR collection scanning must stop at its business link/visit cap and
  report truncation, not continue to count every decoded link or label the
  safety limit as malformed.
- Pinned `go-codec-dagpb` has no decode options. A bounded `protowire`
  preflight must cap root raw bytes, top-level links, individual link bytes,
  names, and CID fields before `dagpb.DecodeBytes` constructs its IPLD list.
- The remediation keeps Go DAG `limitsHit` as named reason strings, leaves CAR
  version numeric, and makes frontend fixtures match those exact wire types.

## Gate 4 Remediation Validation

- `webui/.gitignore` now leaves `dist` unignored; generated assets are present
  and await final intended-file staging with source changes.
- DAG now initializes `limitsHit` to an empty array, uses decode-time DAG-CBOR
  limits, preflights DAG-PB links before construction, reports named limit
  reasons, and makes any limit set top-level `truncated`.
- CAR rejects an extra valid block with `unexpected_block_count`; a validated
  IPNS target is labelled `reported`, not resolved or fetched.
- Retrieval has both start and CID routes. Its copy states that metadata may
  retrieve the root and provider lookup is separate, while excluding peer
  details and dialing claims.
- Coordinator reran `make test`: Go passed and WebUI reported 15 passing test
  files / 93 tests. `make build` regenerated all MPA entries and built Go.
- Live DHT-disabled daemon checks: DAG response contains `limitsHit: []`; CAR
  renders numeric `carVersion`; IPNS renders `not_found`; Retrieval renders its
  landing, explicit observation workflow, metadata timeout, and provider error
  without fabricated success. Root Inspector, DAG, CAR, IPNS, and Retrieval
  deep links rendered with no browser errors. 390px Retrieval and DAG views
  had no horizontal overflow.

## Final Acceptance Review: Last Remediation Batch

- P0: the actual `webui/.gitignore` again contains `dist`, so final integration
  must restore the two-line rule and stage generated output with the source.
- P1: Retrieval must not promise that the gateway will not dial; only that the
  browser receives no content stream. IPNS must call the record path reported,
  not resolved, and omit resolve wording.
- P1: IPNS EOL uses RFC3339Nano to preserve record precision.
- P1: metadata must provide a nonempty stable fallback for unknown multihash
  codes so a structurally valid server response remains accepted by the UI.

## Final Acceptance

- The final independent staged review accepted Phase 4 with no P0/P1 findings.
- The real index tracks generated `webui/dist` assets and passes the repository
  workflow's three tracked-dist assertions. Source whitespace checks pass;
  generated bundle whitespace remains a documented Vite dependency artifact.
- Final evidence: `make test` (Go plus 16 WebUI test files / 95 tests),
  `make build`, route/API/browser checks, mobile overflow checks, and final
  staged Oracle review.
