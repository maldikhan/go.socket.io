dev-deps:
	go install github.com/ory/go-acc@latest
	go install govulncheck/cmd/govulncheck@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
mocks:
	go generate ./...
test-coverage:
	go-acc ./... --ignore "mock,fake" --output coverage.out
	go tool cover -html=coverage.out -o coverage.html
	open coverage.html
test:
	go test ./...
	go vet ./...
	govulncheck ./...
	gosec -quiet ./...
	golangci-lint run ./...	
bench:
	go test -bench=. ./... -benchmem
act-ci-test:
	DOCKER_HOST=`docker context inspect --format '{{.Endpoints.docker.Host}}'` act push -v -n