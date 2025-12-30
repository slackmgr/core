.PHONY: init modules test lint tools

init: modules

modules:
	go mod tidy

test:
#	gosec ./...
	go fmt ./...
	go test ./... -timeout 5s --cover
	go vet ./...

lint:
	golangci-lint run --fix ./...

tools:
	@echo "Installing gosec..."
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	@echo "Installing golangci-lint via Homebrew (recommended for macOS)..."
	brew install golangci-lint || brew upgrade golangci-lint
