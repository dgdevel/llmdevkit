VERSION := $(shell cat VERSION)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := compile

compile:
	scripts/check-non-ascii.sh
	go fmt ./...
	CGO_ENABLED=1 go build $(LDFLAGS) -o llmdevkit ./cmd/llmdevkit/

dist: compile
	@mkdir -p dist
	@cp llmdevkit dist/
	tar -czf dist/llmdevkit-$(VERSION).tar.gz -C dist llmdevkit
	@echo "Packaged dist/llmdevkit-$(VERSION).tar.gz"

tag:
	@test "$(VERSION)" != "" || (echo "VERSION is empty" && exit 1)
	git tag -a "v$(VERSION)" -m "Release v$(VERSION)"
	git push origin "v$(VERSION)"
	@echo "Tagged and pushed v$(VERSION)"

release: dist
	gh release create "v$(VERSION)" dist/llmdevkit-$(VERSION).tar.gz \
		--title "v$(VERSION)" \
		--notes "Release v$(VERSION)"
	@echo "Published GitHub release v$(VERSION)"

clean:
	rm -f llmdevkit
	rm -rf dist/

test:
	bun run eslint --no-config-lookup cmd/llmdevkit/
	go vet ./...
	go test ./... || (go test -v ./... 2>&1 | grep --line-buffered -E "FAIL|--- FAIL"; exit 1)

.PHONY: compile dist tag release clean test

