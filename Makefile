.DEFAULT_GOAL := help

# Project-local SDKs installed by setup-build-env; no shell profile edits.
export PATH := $(CURDIR)/.artifacts/toolchains/node/bin:$(CURDIR)/.artifacts/toolchains/go/bin:$(PATH)

.PHONY: install dev build build-arm64 test check format serve fixture smoke design-smoke appearance-smoke shelf-smoke viewer-smoke statistics-smoke mobile-scroll-smoke map-smoke index-ui-smoke updates-smoke mobile-flow-smoke live-capture-smoke motion-smoke i18n-smoke
install:
	npm ci
	cd dash-backend && go mod download
dev:
	npm run dev
build:
	npm run build
	mkdir -p .artifacts
	cd dash-backend && CGO_ENABLED=0 go build -trimpath -o ../.artifacts/dash-backend .
build-arm64:
	npm run build
	mkdir -p .artifacts
	cd dash-backend && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o ../.artifacts/dash-backend-linux-arm64 .
format:
	npm run format
	cd dash-backend && gofmt -w *.go internal/telemetry/*.go internal/mcaptypes/*/*.go
test:
	python3 scripts/setup-toolchains.test.py
	python3 scripts/fetch-release.test.py
	bash scripts/install-backend.test.sh
	npm test
	cd dash-backend && go test -race ./...
check:
	npm run format:check
	cd dash-backend && test -z "$$(gofmt -l .)"
	cd dash-backend && go vet ./...
	$(MAKE) test
	$(MAKE) build
.PHONY: generate-mcap-schemas regenerate-telemetry-fixtures
generate-mcap-schemas:
	bash scripts/generate-mcap-schemas.sh
regenerate-telemetry-fixtures:
	$(or $(PYTHON),python3) scripts/fixtures/generate.py
fixture:
	node scripts/prepare-fixture.mjs
serve:
	.artifacts/dash-backend --root .artifacts/device/recordings --cache .artifacts/device/cache --params .artifacts/device/params --app dist --listen 127.0.0.1:8088
smoke:
	node scripts/smoke.mjs
design-smoke:
	node scripts/design-smoke.mjs
appearance-smoke:
	node scripts/appearance-smoke.mjs
shelf-smoke:
	node scripts/shelf-smoke.mjs
viewer-smoke:
	node scripts/viewer-smoke.mjs
statistics-smoke:
	node scripts/statistics-smoke.mjs
mobile-scroll-smoke:
	node scripts/mobile-scroll-smoke.mjs
map-smoke:
	node scripts/map-smoke.mjs
index-ui-smoke:
	node scripts/index-ui-smoke.mjs
updates-smoke:
	node scripts/updates-smoke.mjs
mobile-flow-smoke:
	node scripts/mobile-flow-smoke.mjs
live-capture-smoke:
	node scripts/live-capture-smoke.mjs
motion-smoke:
	node scripts/motion-smoke.mjs
i18n-smoke:
	node scripts/i18n-smoke.mjs

.PHONY: sandbox-up sandbox-down sandbox-test sandbox-smoke sandbox-logs device-check device-install release ota-smoke
sandbox-up:
	node scripts/sandbox-up.mjs
sandbox-down:
	docker compose down
sandbox-test:
	node scripts/sandbox-test.mjs
sandbox-smoke:
	DASH_TEST_URL="http://127.0.0.1:$${DASH_SANDBOX_PORT:-8089}" node scripts/smoke.mjs
sandbox-logs:
	docker compose logs --tail=200 -f dash
device-check:
	bash scripts/install-backend.sh --check "$(DEVICE)"
.PHONY: setup-build-env build-env-check device-install-source source-release source-install-smoke
setup-build-env:
	bash scripts/setup-build-env.sh
build-env-check:
	bash scripts/setup-build-env.sh --check
device-install-source:
	bash scripts/install-backend.sh --source "$(DEVICE)"
source-install-smoke:
	node scripts/install-source-smoke.mjs
source-release:
	$(MAKE) check
	node scripts/release.mjs --source

device-install:
	bash scripts/install-backend.sh --channel "$(or $(CHANNEL),main)" $(if $(RELEASE),--release "$(RELEASE)") $(if $(REPOSITORY),--repo "$(REPOSITORY)") "$(DEVICE)"

ota-smoke:
	npm run build
	node scripts/ota-smoke.mjs

release:
	npm run build
	DASH_RELEASE_CHANNEL="$(CHANNEL)" DASH_RELEASE_SEQUENCE="$(BUILD)" node scripts/release.mjs

.PHONY: help
help:
	@printf '%s\n' 'Dash workflows:' '  make install / dev / build / check' '  make generate-mcap-schemas / regenerate-telemetry-fixtures [PYTHON=path]' '  make fixture / serve / smoke / design-smoke / appearance-smoke / shelf-smoke / viewer-smoke / statistics-smoke / mobile-scroll-smoke / map-smoke / index-ui-smoke / updates-smoke / mobile-flow-smoke / live-capture-smoke / motion-smoke / i18n-smoke' '  make sandbox-up / sandbox-test / sandbox-smoke / sandbox-logs / sandbox-down' '  make setup-build-env / build-env-check' '  make device-install-source DEVICE=comma@DEVICE_IP / source-release / source-install-smoke' '  make device-check DEVICE=comma@DEVICE_IP' '  make device-install DEVICE=comma@DEVICE_IP [CHANNEL=main|nightly|dev] [RELEASE=TAG]' '  make release CHANNEL=dev BUILD=1 / ota-smoke'
