.PHONY: build go-dev webui-build webui-dev dev test

build: webui-build
	go build

go-dev: webui-build
	go build
	RAINBOW_DATADIR="$${RAINBOW_DATADIR:-$$(mktemp -d "$${TMPDIR:-/tmp}/rainbow-dev.XXXXXX")}" ./rainbow

webui-build:
	bun install --cwd webui --frozen-lockfile
	bun run --cwd webui build

webui-dev:
	bun run --cwd webui dev

# Run the Vite development server. Start `rainbow` separately for API/content requests.
dev: webui-dev

test:
	go test ./...
	bun install --cwd webui --frozen-lockfile
	bun run --cwd webui test
