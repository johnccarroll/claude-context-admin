.PHONY: build web test dev dev-real check

build: web
	go build -o cca ./cmd/cca

web:
	cd web && bun install --frozen-lockfile && bun run build

test:
	go test -race ./...
	cd web && bunx tsc --noEmit

check: test
	git ls-files -z '*.go' | xargs -0 gofmt -l | (! grep .)
	go vet ./...

# Live dev server: UI edits reload the page, Go edits restart cca (see scripts/dev.sh).
dev:
	scripts/dev.sh demo

# The same against your real config, read-only. `scripts/dev.sh write` makes it editable.
dev-real:
	scripts/dev.sh real
