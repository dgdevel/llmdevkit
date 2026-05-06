.DEFAULT_GOAL := compile

compile:
	go build -o llmdevkit-mcp .
	go build -o llmdevkit-config ./cmd/llmdevkit-config/
	go build -o llmdevkit-setup ./cmd/llmdevkit-setup/
	CGO_ENABLED=1 go build -o llmdevkit-indexer ./cmd/llmdevkit-indexer/

clean:
	rm -f llmdevkit-mcp llmdevkit-config llmdevkit-setup llmdevkit-indexer

test:
	go vet ./...
	go test ./... || go test -v ./...

run: compile
	./llmdevkit-mcp --stdio

.PHONY: compile clean test run
