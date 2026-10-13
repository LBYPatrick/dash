.DEFAULT_GOAL := help

.PHONY: install dev build build-arm64 test check format serve fixture smoke design-smoke appearance-smoke shelf-smoke viewer-smoke
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
	cd dash-backend && gofmt -w *.go internal/telemetry/*.go
test:
	bash scripts/install-backend.test.sh
	npm test
	cd dash-backend && go test -race ./...
check:
	npm run format:check
	cd dash-backend && go vet ./...
	$(MAKE) test
	$(MAKE) build
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

.PHONY: sandbox-up sandbox-down sandbox-test sandbox-smoke sandbox-logs device-check device-install
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
device-install:
	bash scripts/install-backend.sh "$(DEVICE)"

.PHONY: help
help:
	@printf '%s\n' 'Dash workflows:' '  make install / dev / build / check' '  make fixture / serve / smoke / design-smoke / appearance-smoke / shelf-smoke / viewer-smoke' '  make sandbox-up / sandbox-test / sandbox-smoke / sandbox-logs / sandbox-down' '  make device-check DEVICE=comma@DEVICE_IP' '  make device-install DEVICE=comma@DEVICE_IP'
