.DEFAULT_GOAL := help

# Keep host validation modest; this does not change comma device budgets.
HOST_CHECK_PROCS ?= 2

# Project-local SDKs installed by setup-build-env; no shell profile edits.
export PATH := $(CURDIR)/.artifacts/toolchains/vite-plus/bin:$(CURDIR)/.artifacts/toolchains/go/bin:$(PATH)
# Keep the Vite+-managed frontend environment local after setup-build-env.
ifneq ($(wildcard .artifacts/toolchains/vite-plus/bin/vp),)
export VP_HOME := $(CURDIR)/.artifacts/toolchains/vite-plus
endif
export VP_NO_UPDATE_CHECK := 1

##@ Setup
.PHONY: setup-build-env build-env-check install
setup-build-env: ## Set up Vite+ using the installed Go SDK
	bash scripts/setup-build-env.sh

build-env-check: ## Check the host build toolchains
	bash scripts/setup-build-env.sh --check

install: ## Install locked frontend and Go dependencies
	vp install --frozen-lockfile
	cd backend && go mod download

##@ Development
.PHONY: dev fixture serve build build-arm64
dev: ## Start the Vite frontend dev server
	vp -C frontend dev --host 0.0.0.0

fixture: ## Prepare synthetic local recordings
	go -C backend run ./cmd/dash-tool fixture

serve: ## Serve the local app at 127.0.0.1:8088
	.artifacts/dash-backend --root .artifacts/device/recordings --cache .artifacts/device/cache --params .artifacts/device/params --app frontend/dist --listen 127.0.0.1:8088

build: ## Build the PWA and host backend
	vp -C frontend run build
	mkdir -p .artifacts
	cd backend && CGO_ENABLED=0 go build -trimpath -o ../.artifacts/dash-backend .

build-arm64: ## Build the PWA and Linux ARM64 backend
	vp -C frontend run build
	mkdir -p .artifacts
	cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o ../.artifacts/dash-backend-linux-arm64 .

##@ Quality
.PHONY: check test lint format
check: ## Run format, lint, types, tests and builds
	vp check
	cd backend && test -z "$$(gofmt -l .)"
	cd backend && GOMAXPROCS=$(HOST_CHECK_PROCS) go vet -p 1 ./...
	$(MAKE) test
	$(MAKE) build

test: ## Run unit, installer and Go race tests
	vp test run
	cd backend && GOMAXPROCS=$(HOST_CHECK_PROCS) go test -race -p 1 -parallel 1 ./...

lint: ## Run Oxlint; fail on warnings
	vp lint --deny-warnings

format: ## Format source and docs with Oxfmt and gofmt
	vp fmt
	cd backend && gofmt -w .

##@ Browser checks
.PHONY: smoke frontend-polish-smoke design-smoke appearance-smoke \
  shelf-smoke playback-smoke viewer-smoke statistics-smoke \
  mobile-scroll-smoke mobile-flow-smoke map-smoke index-ui-smoke \
  updates-smoke live-capture-smoke live-layout-smoke motion-smoke i18n-smoke
smoke: ## Check core app integration
	vp test run --config frontend/browser.config.ts frontend/tests/browser/smoke.test.js

frontend-polish-smoke: ## Check contrast, focus and localized layouts
	vp test run --config frontend/browser.config.ts frontend/tests/browser/frontend-polish-smoke.test.js

design-smoke: ## Check responsive interface behavior
	vp test run --config frontend/browser.config.ts frontend/tests/browser/design-smoke.test.js

appearance-smoke: ## Check themes and performance mode
	vp test run --config frontend/browser.config.ts frontend/tests/browser/appearance-smoke.test.js

shelf-smoke: ## Check Shelf components and keyboard flows
	vp test run --config frontend/browser.config.ts frontend/tests/browser/shelf-smoke.test.js

playback-smoke: ## Check preparation, GPS gaps and notifications
	vp test run --config frontend/browser.config.ts frontend/tests/browser/playback-smoke.test.js

viewer-smoke: ## Check playback, seeking and map follow
	vp test run --config frontend/browser.config.ts frontend/tests/browser/viewer-smoke.test.js

statistics-smoke: ## Check dashboard graphs and interactions
	vp test run --config frontend/browser.config.ts frontend/tests/browser/statistics-smoke.test.js

mobile-scroll-smoke: ## Check touch scrolling and fixed viewer bounds
	vp test run --config frontend/browser.config.ts frontend/tests/browser/mobile-scroll-smoke.test.js

mobile-flow-smoke: ## Check mobile navigation and session workflows
	vp test run --config frontend/browser.config.ts frontend/tests/browser/mobile-flow-smoke.test.js

map-smoke: ## Check maps and tile failure recovery
	vp test run --config frontend/browser.config.ts frontend/tests/browser/map-smoke.test.js

index-ui-smoke: ## Check indexing and recording readiness
	vp test run --config frontend/browser.config.ts frontend/tests/browser/index-ui-smoke.test.js

updates-smoke: ## Check update settings and recovery flows
	vp test run --config frontend/browser.config.ts frontend/tests/browser/updates-smoke.test.js

live-capture-smoke: ## Check live capture and saved browser clips
	vp test run --config frontend/browser.config.ts frontend/tests/browser/live-capture-smoke.test.js

live-layout-smoke: ## Check live layouts and touch controls
	vp test run --config frontend/browser.config.ts frontend/tests/browser/live-layout-smoke.test.js

motion-smoke: ## Check transitions and reduced motion
	vp test run --config frontend/browser.config.ts frontend/tests/browser/motion-smoke.test.js

i18n-smoke: ## Check all five languages and preferences
	vp test run --config frontend/browser.config.ts frontend/tests/browser/i18n-smoke.test.js

##@ Docker sandbox
.PHONY: sandbox-up sandbox-test sandbox-smoke sandbox-logs sandbox-down
sandbox-up: ## Build and start the synthetic device sandbox
	go -C backend run ./cmd/dash-tool sandbox-up

sandbox-test: ## Check sandbox APIs and resource limits
	DASH_INTEGRATION=sandbox GOMAXPROCS=$(HOST_CHECK_PROCS) go -C backend test -p 1 -parallel 1 -count=1 -timeout=20m ./internal/hosttools -run TestSandboxIntegration

sandbox-smoke: ## Run browser integration against the sandbox
	DASH_TEST_URL="http://127.0.0.1:$${DASH_SANDBOX_PORT:-8089}" vp test run --config frontend/browser.config.ts frontend/tests/browser/smoke.test.js

sandbox-logs: ## Follow the latest backend logs
	docker compose logs --tail=200 -f dash

sandbox-down: ## Stop the sandbox
	docker compose down

##@ Device installation
.PHONY: device-check device-install device-install-source device-uninstall
device-check: ## Inspect a device over SSH without installing
	bash scripts/install-backend.sh $(if $(filter 1,$(RUNTIME)),--runtime) --check $(if $(DEVICE_KEY),--identity "$(DEVICE_KEY)") "$(DEVICE)"

device-install: ## Install a release over SSH; defaults to main
	bash scripts/install-backend.sh $(if $(filter 1,$(RUNTIME)),--runtime) $(if $(DEVICE_KEY),--identity "$(DEVICE_KEY)") --channel "$(or $(CHANNEL),main)" $(if $(RELEASE),--release "$(RELEASE)") $(if $(REPOSITORY),--repo "$(REPOSITORY)") "$(DEVICE)"

device-install-source: ## Build locally and install over SSH
	bash scripts/install-backend.sh $(if $(filter 1,$(RUNTIME)),--runtime) --source $(if $(DEVICE_KEY),--identity "$(DEVICE_KEY)") "$(DEVICE)"

device-uninstall: ## Remove Dash and its systemd registration
	bash scripts/uninstall-backend.sh $(if $(DEVICE_KEY),--identity "$(DEVICE_KEY)") "$(DEVICE)"

##@ Packaging and delivery
.PHONY: release source-release source-install-smoke ota-smoke build-media \
  media-smoke generate-mcap-schemas regenerate-telemetry-fixtures
release: ## Package a channel build; set CHANNEL and BUILD
	vp -C frontend run build
	DASH_RELEASE_CHANNEL="$(CHANNEL)" DASH_RELEASE_SEQUENCE="$(BUILD)" go -C backend run ./cmd/dash-tool release

source-release: ## Check and package a local source build
	$(MAKE) check
	go -C backend run ./cmd/dash-tool release --source

source-install-smoke: ## Check source installation with mocked SSH
	DASH_INTEGRATION=source-install GOMAXPROCS=$(HOST_CHECK_PROCS) go -C backend test -p 1 -parallel 1 -count=1 -timeout=20m ./internal/hosttools -run TestSourceInstallIntegration

ota-smoke: ## Check activation and rollback in Linux/Docker
	vp -C frontend run build
	DASH_INTEGRATION=ota GOMAXPROCS=$(HOST_CHECK_PROCS) go -C backend test -p 1 -parallel 1 -count=1 -timeout=20m ./internal/hosttools -run TestOTAIntegration

build-media: ## Build bundled media tools; set MEDIA_ARCH
	go -C backend run ./cmd/dash-tool build-media $(MEDIA_ARCH)

media-smoke: ## Exercise bundled tools in Linux containers
	DASH_INTEGRATION=media GOMAXPROCS=$(HOST_CHECK_PROCS) go -C backend test -p 1 -parallel 1 -count=1 -timeout=20m ./internal/hosttools -run TestMediaIntegration

generate-mcap-schemas: ## Regenerate typed MCAP schemas
	bash scripts/generate-mcap-schemas.sh

regenerate-telemetry-fixtures: ## Regenerate synthetic firmware telemetry
	$(or $(PYTHON),python3) scripts/fixtures/generate.py

.PHONY: help
help:
	@help_color=; \
	if test -t 1 && test -z "$$NO_COLOR" && test "$$TERM" != dumb; then help_color=1; fi; \
	awk -v color="$$help_color" '\
		BEGIN { \
			bold = color ? "\033[1m" : ""; \
			accent = color ? "\033[36m" : ""; \
			reset = color ? "\033[0m" : ""; \
			printf "\n%sDash%s\n  Usage: make <command> [NAME=value ...]\n", bold, reset; \
		} \
		/^##@ / { printf "\n%s%s%s\n", bold, substr($$0, 5), reset; next } \
		/^[a-z][a-z0-9-]*:.*## / { \
			split($$0, entry, ":.*## "); \
			printf "  %s%-30s%s %s\n", accent, entry[1], reset, entry[2]; \
		} \
		END { \
			printf "\n%sOptions%s\n", bold, reset; \
			printf "  %-30s %s\n", "DEVICE=comma@HOST", "Required for device commands"; \
			printf "  %-30s %s\n", "DEVICE_KEY=/path/to/key", "Optional SSH identity"; \
			printf "  %-30s %s\n", "CHANNEL=main|nightly|dev", "Device installs default to main"; \
			printf "  %-30s %s\n", "RELEASE=TAG", "Install a specific release"; \
			printf "  %-30s %s\n", "REPOSITORY=owner/repo", "Optional release repository"; \
			printf "  %-30s %s\n", "RUNTIME=1", "Temporary install; no boot persistence"; \
			printf "  %-30s %s\n", "BUILD=NUMBER", "Sequence number for release packaging"; \
			printf "  %-30s %s\n", "MEDIA_ARCH=arm64|amd64", "Architecture for bundled media tools"; \
			printf "  %-30s %s\n", "PYTHON=/path/to/python", "Interpreter for telemetry fixtures"; \
			printf "  %-30s %s\n", "CHROME_PATH=/path/to/chrome", "Browser executable for browser checks"; \
			printf "  %-30s %s\n", "DASH_TEST_URL=http://HOST:PORT", "Backend address for browser checks"; \
			printf "\n%sExamples%s\n", bold, reset; \
			print "  make setup-build-env install"; \
			print "  make build fixture serve"; \
			print "  make device-install DEVICE=comma@HOST CHANNEL=dev"; \
			print "  make release CHANNEL=dev BUILD=1"; \
			print "\n  Browser checks need a running synthetic backend and Chrome."; \
			print "  Guides: README.md and docs/development.md\n"; \
		}' $(MAKEFILE_LIST)
