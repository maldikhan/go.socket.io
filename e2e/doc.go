// Package e2e contains end-to-end integration tests that run the Go client
// against a real Socket.IO server.
//
// The tests are guarded by the "e2e" build tag, so they are excluded from the
// default `go test ./...` run (and therefore from the unit-test coverage gate).
// Run them with a reachable server via:
//
//	make integration-test
//
// or, against an already-running server, with:
//
//	E2E_SERVER_URL=http://localhost:3000 go test -tags e2e ./e2e/...
package e2e
