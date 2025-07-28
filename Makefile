dev-deps:
	go install github.com/axw/gocov/gocov@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
mocks:
	go generate ./...
test-coverage:
	go test `go list ./...|grep -v mocks` -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	open coverage.html
	gocov test `go list ./...|grep -v mocks`|gocov report
test:
	go test ./...
	go vet ./...
	govulncheck ./...
	gosec -quiet ./...
	golangci-lint run ./...	
bench:
	go test -bench=. ./... -benchmem
act-ci-test:
	DOCKER_HOST=`docker context inspect --format '{{.Endpoints.docker.Host}}'` act