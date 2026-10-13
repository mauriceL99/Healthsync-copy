GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG     := github.com/mauriceL99/Healthsync-copy
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64

PREFIX  ?= $(HOME)/.local

.PHONY: build install test lint vectors skills dist relay-image e2e clean

build: ## Build healthbridge and healthbridge-relay into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

install: build ## Copy both binaries to $(PREFIX)/bin (e.g. make install PREFIX=$$(brew --prefix))
	install -d $(PREFIX)/bin
	install -m 0755 bin/healthbridge bin/healthbridge-relay $(PREFIX)/bin/

test: ## Run all Go tests with the race detector
	$(GO) test -race ./...

lint: ## gofmt + go vet
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
	$(GO) vet ./...

vectors: ## Regenerate protocol test vectors and copy them to the iOS tests
	$(GO) test ./internal/protocol -run TestVectors -update
	cp testdata/protocol_vectors.json ios/HealthBridgeTests/Resources/protocol_vectors.json

skills: ## Copy the generic skill into the agent integrations
	cp integrations/skill/healthbridge/SKILL.md integrations/claude-code/healthbridge/skills/healthbridge/SKILL.md
	cp integrations/skill/healthbridge/SKILL.md integrations/codex/skills/healthbridge/SKILL.md
	@echo "OpenClaw SKILL.md carries extra metadata; update integrations/openclaw/healthbridge/SKILL.md manually."

dist: ## Cross-compile release archives into ./dist
	rm -rf dist && mkdir -p dist
	for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  out=dist/healthbridge-$(VERSION)-$$os-$$arch; mkdir -p $$out; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out/healthbridge$$ext ./cmd/healthbridge || exit 1; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out/healthbridge-relay$$ext ./cmd/healthbridge-relay || exit 1; \
	  tar -C dist -czf $$out.tar.gz $$(basename $$out) && rm -rf $$out; \
	done
	cd dist && sha256sum *.tar.gz > SHA256SUMS

relay-image: ## Build the relay container image
	docker build -f deploy/Dockerfile -t healthbridge-relay:$(VERSION) .

e2e: build ## Local end-to-end demo: relay + simulated iPhone + fetch + summary
	./scripts/e2e-demo.sh

clean:
	rm -rf bin dist
