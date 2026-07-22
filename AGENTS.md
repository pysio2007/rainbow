# Rainbow Agent Notes

## Structure

- The root is one Go `main` module (`github.com/ipfs/rainbow`, Go `1.25.7`); `main.go` owns CLI/startup and `handlers.go` wires gateway routes, UI APIs, and embedded assets.
- `webui/` is a separate Bun `1.3.3` React/Vite multi-page app. Its output is embedded from `webui/dist`; new or renamed UI routes need matching Vite inputs (`webui/vite.config.ts`) and Go route mapping (`handlers.go`).
- Keep `version.json` available: `version.go` embeds it into every Go build.

## Build And Test

- Use `make build` for a complete binary: it installs WebUI dependencies with the frozen lockfile, runs the TypeScript/Vite build, then runs `go build`. A bare `go build` requires an existing `webui/dist` because of `go:embed`.
- `make test` runs `go test ./...` before installing WebUI dependencies and running Vitest. For focused checks use `go test . -run '^TestName$'` or `bun run --cwd webui test -- path/to/file.test.ts`.
- `go test . -run '^TestEndToEndTrustlessGatewayDomains$'` installs and starts the binary as a CLI smoke test; it is skipped on Windows.
- `bun run --cwd webui build` is the WebUI typecheck as well as its production build; there is no repository-local lint or separate typecheck command.
- Run `make webui-build` before validating newly added embedded page entries: the Inspector and Retrieval Go route tests skip when their generated files are absent from `webui/dist`.

## Local Runtime And CI

- `make dev` starts only Vite. `make go-dev` rebuilds the WebUI and daemon, then uses a temporary datadir unless `RAINBOW_DATADIR` is set. Direct daemon runs default persistent state to the current directory; use `RAINBOW_DATADIR` or `--datadir` to avoid creating keys and datastore state in the checkout.
- Gateway conformance is separate from unit tests: its CI workflow needs Bash, curl, Kubo `v0.33.0-rc1`, downloaded fixtures, and `GATEWAY_CONFORMANCE_TEST=true`, then exercises three backend modes.
- `webui/dist` is generated output and must not be committed to Git. The current WebUI CI workflow still requires it to be tracked and reproducible, so that check needs alignment with this policy.
- Docker intentionally builds with Go `1.26`, cross-compiles with `CGO_ENABLED=0`, and runs as the non-root `ipfs` user.
- PRs that modify Go files, `go.mod`, or `go.sum` must update `CHANGELOG.md` unless the title contains `[skip changelog]` or the PR has the `skip/changelog` label.
