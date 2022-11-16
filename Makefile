.PHONY: coverage
coverage:
	go tool cover -func=coverage.out
.PHONY: generate
generate:
	$(ENV_VARS) go generate ./...
.PHONY: test
test:
	go test -v -coverprofile coverage.out -race ./...
.PHONY: integration
integration:
	go test -v -tags integration ./integration/...
.PHONY: tidy
tidy:
	go mod tidy