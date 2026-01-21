init: modules

modules:
	go mod tidy

test:
	gosec ./...
	go fmt ./...
	go test -race -timeout 5s --cover ./...
	go vet ./...

test-integration:
	go test ./... -tags integration -timeout 5s --cover

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

bump-common-lib:
	go get github.com/peteraglen/slack-manager-common@latest
	go mod tidy
