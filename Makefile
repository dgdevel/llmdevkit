VERSION := $(shell cat VERSION)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
BINS := llmdevkit-mcp llmdevkit-config llmdevkit-setup llmdevkit-acp llmdevkit-server llmdevkit-indexer

.DEFAULT_GOAL := compile

compile:
	go build $(LDFLAGS) -o llmdevkit-mcp ./cmd/llmdevkit-mcp/
	go build $(LDFLAGS) -o llmdevkit-config ./cmd/llmdevkit-config/
	go build $(LDFLAGS) -o llmdevkit-setup ./cmd/llmdevkit-setup/
	go build $(LDFLAGS) -o llmdevkit-acp ./cmd/llmdevkit-acp/
	go build $(LDFLAGS) -o llmdevkit-server ./cmd/llmdevkit-server/
	CGO_ENABLED=1 go build $(LDFLAGS) -o llmdevkit-indexer ./cmd/llmdevkit-indexer/

dist: compile
	@mkdir -p dist
	@for bin in $(BINS); do [ -f "$$bin" ] && mv "$$bin" dist/; done
	tar -czf dist/llmdevkit-$(VERSION).tar.gz -C dist $(BINS)
	@echo "Packaged dist/llmdevkit-$(VERSION).tar.gz"

tag:
	@test "$(VERSION)" != "" || (echo "VERSION is empty" && exit 1)
	git tag -a "v$(VERSION)" -m "Release v$(VERSION)"
	git push origin "v$(VERSION)"
	@echo "Tagged and pushed v$(VERSION)"

clean:
	rm -f $(BINS)
	rm -rf dist/

test:
	bun run eslint --no-config-lookup cmd/llmdevkit-server/
	go vet ./...
	go test ./... || go test -v ./...

.PHONY: compile dist tag clean test
