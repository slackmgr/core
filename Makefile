init: modules

modules:
	go mod tidy

test:
	gosec ./...
	go fmt ./...
	go test ./... -timeout 5s --cover
	go vet ./...

test-integration:
	go test ./... -tags integration -timeout 5s --cover

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run ./...
