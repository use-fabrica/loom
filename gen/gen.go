// Package gen holds the generated proto and Connect code for the Loom v0
// contract (proto/loom/v1/loom.proto, ADR-0005). Regenerate after editing
// the proto with `go generate ./gen/...`; the output is committed so the
// toolchain (protoc, protoc-gen-go, protoc-gen-connect-go) is a build-time
// dependency only, never a runtime one.
package gen

//go:generate protoc --proto_path=../proto --go_out=. --go_opt=paths=source_relative --connect-go_out=. --connect-go_opt=paths=source_relative loom/v1/loom.proto
