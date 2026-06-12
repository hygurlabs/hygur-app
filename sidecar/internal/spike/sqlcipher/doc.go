// Package sqlcipher holds an isolated, build-tag-gated feasibility spike for C1
// (encrypted DB at rest). The actual spike lives in spike_test.go behind the
// `sqlcipher_spike` build tag, so it never compiles into normal builds — that
// matters because the SQLCipher driver registers under the same "sqlite3" name
// as mattn/go-sqlite3, and importing both in one binary panics at init.
//
// Run it with:
//
//	CGO_ENABLED=1 go test -tags 'sqlcipher_spike sqlite_fts5' ./internal/spike/sqlcipher/ -v
//
// This file (untagged) keeps the package buildable by `go build ./...`.
package sqlcipher
