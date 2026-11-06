# Loom — manager's cockpit for beads.
#
# make dev      API on :7333 + vite on :5173 (proxying /api), both hot-reloading
# make build    vite build -> embed -> single ./loom binary
# make release  the same, version-stamped and stripped
# make test     go test ./... + the web suite
# make check    everything CI would run
# make seed     seed the local dolt db with realistic test data

GO      ?= go
NPM     ?= npm
BINARY  ?= loom
WEB_DIR ?= web
# webembed cannot go:embed across package boundaries, so the vite output is
# copied here before the Go build.
EMBED_DIR ?= internal/webembed/dist

# VERSION defaults to the nearest git tag, falling back to a short SHA. Override
# it for a real release: make release VERSION=v0.2.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BUILDINFO := github.com/kurofune/beads-loom/internal/buildinfo
LDFLAGS   := -X '$(BUILDINFO).Version=$(VERSION)' \
             -X '$(BUILDINFO).Commit=$(COMMIT)' \
             -X '$(BUILDINFO).Date=$(DATE)'
# -s -w drop the symbol table and DWARF: a few MB smaller, and nothing here is
# debugged from a shipped binary.
RELEASE_LDFLAGS := $(LDFLAGS) -s -w

.PHONY: dev dev-api dev-web build build-web build-go release test test-go test-web \
        check check-public vet fmt seed clean version help

help:
	@echo "targets:"
	@echo "  dev      API + vite dev server, hot reload"
	@echo "  build    single binary with the frontend embedded"
	@echo "  release  build, version-stamped and stripped"
	@echo "  test     go test ./... + the web suite"
	@echo "  check    typecheck, lint gates, vet and every test"
	@echo "  seed     load the test backlog into this repo's dolt db"
	@echo "  clean    remove build outputs"

## dev — run the API and the vite dev server together; Ctrl-C stops both.
dev:
	@echo "loom: API on :7333, vite on :5173 (Ctrl-C to stop both)"
	@trap 'kill 0' EXIT INT TERM; \
	$(MAKE) dev-api & \
	$(MAKE) dev-web & \
	wait

dev-api:
	$(GO) run . --listen 127.0.0.1:7333

dev-web:
	cd $(WEB_DIR) && $(NPM) run dev

## build — produce a single self-contained binary with the frontend inside.
build: build-web build-go

# build-web builds the frontend and stages it where go:embed can reach it.
#
# The `cd` is confined to a subshell on purpose: make runs each recipe line in
# one shell, so a bare `cd web && ...` leaks into every later command on the
# line and the copy silently resolves to web/web/dist. `set -e` makes a failed
# build or copy fail the target instead of leaving a binary with no frontend
# inside it — which is exactly what happened before both were fixed.
build-web:
	@set -e; \
	if [ ! -f $(WEB_DIR)/package.json ]; then \
		echo "no $(WEB_DIR)/package.json; building the API only"; \
		mkdir -p $(EMBED_DIR); touch $(EMBED_DIR)/.gitkeep; \
		exit 0; \
	fi; \
	( cd $(WEB_DIR) && $(NPM) run build ); \
	if [ ! -f $(WEB_DIR)/dist/index.html ]; then \
		echo "error: $(WEB_DIR)/dist/index.html missing after the vite build" >&2; \
		exit 1; \
	fi; \
	rm -rf $(EMBED_DIR); \
	mkdir -p $(EMBED_DIR); \
	cp -R $(WEB_DIR)/dist/. $(EMBED_DIR)/; \
	touch $(EMBED_DIR)/.gitkeep; \
	echo "embedded $$(find $(EMBED_DIR) -type f | wc -l | tr -d ' ') files from $(WEB_DIR)/dist"

build-go:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) .

## release — a stamped, stripped binary. Prints its own version when done.
release: build-web
	$(GO) build -trimpath -ldflags "$(RELEASE_LDFLAGS)" -o $(BINARY) .
	@echo
	@./$(BINARY) --version
	@ls -lh $(BINARY) | awk '{print "  size      " $$5}'

version:
	@echo $(VERSION)

test: test-go test-web

test-go:
	$(GO) test ./...

test-web:
	@if [ -f $(WEB_DIR)/package.json ]; then cd $(WEB_DIR) && $(NPM) test; fi

## check — everything CI would run, backend and frontend.
check: check-public vet test-go
	@if [ -f $(WEB_DIR)/package.json ]; then cd $(WEB_DIR) && $(NPM) run check; fi
	@echo "all checks passed"

## check-public — no personal paths or names in a tracked file.
check-public:
	./scripts/check-public_test.sh
	./scripts/check-public.sh

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

## seed — load the realistic test backlog into this repo's dolt db.
seed:
	./scripts/seed-testdata.sh

clean:
	rm -f $(BINARY)
	rm -rf $(WEB_DIR)/dist
	find $(EMBED_DIR) -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true
