# Rainbow Agent Notes

## Scope and commands

- This is one Go module (`github.com/ipfs/rainbow`) requiring Go `1.25.7`. The root `main` package builds the production `rainbow` daemon; `main.go` owns CLI and runtime wiring.
- Build from the repository root with `go build`; it creates the ignored `./rainbow` binary. `version.json` is embedded by `version.go`, so it must be present in builds.
- Run the repository test suite with `go test ./...`.
- Run the end-to-end CLI test alone with `go test . -run '^TestEndToEndTrustlessGatewayDomains$'`. It runs `go install .`, starts the binary, and is skipped on Windows.
- There is no repository-local lint, typecheck, or task-runner command. CI's Go test and check jobs delegate to `ipdxco/unified-github-workflows`; do not claim an unverified local command matches them.

## Runtime and integration

- Rainbow defaults its persistent datadir to the directory where it runs and can create `libp2p.key`, blockstore, and datastore state there. Use `--datadir <temp-dir>` or `RAINBOW_DATADIR` for ad hoc runs from the checkout.
- Flags and `RAINBOW_*` settings are documented in `docs/environment-variables.md`; keep that reference aligned with configuration changes.
- Gateway conformance is separate from `go test`: it needs Bash, curl, Kubo `v0.33.0-rc1`, gateway-conformance fixtures, and `GATEWAY_CONFORMANCE_TEST=true`; CI exercises three backend modes against Rainbow on port `8090`.
- Docker builds use Go `1.26`, cross-compile with `CGO_ENABLED=0`, and run the final image as the non-root `ipfs` user. Preserve those constraints when changing the image.

## PR and release checks

- A PR changing `*.go`, `go.mod`, or `go.sum` must also update `CHANGELOG.md`, unless its title includes `[skip changelog]` or it has the `skip/changelog` label.
- Release PRs update both `CHANGELOG.md` and `version.json`; see the release steps in `README.md`.
