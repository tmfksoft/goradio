.PHONY: build release proto proto-push test vet fmt tidy

build:
	go build -o bin/radio ./cmd/radio

# Cross-builds linux/amd64 + windows/amd64 and packages each with the
# example configs + a starter Lua script into dist/release/.
# Usage: make release [VERSION=v0.1.0] (defaults to VERSION=dev)
VERSION ?= dev
release:
	./scripts/package-release.sh $(VERSION)

# Regenerate gen/go/audioserver/v1 from the proto/ module. Requires buf and
# the protoc-gen-go / protoc-gen-go-grpc / protoc-gen-connect-go plugins on
# PATH. buf.yaml lives at proto/buf.yaml (the module root); buf.gen.yaml
# (repo root) supplies the codegen plugins/output and is resolved relative
# to the cwd, not the input.
proto:
	buf lint proto
	buf generate proto

# Publish the proto/ module to its configured BSR (proto.prod.wtf/tmfksoft/goradio).
# Requires `buf registry login proto.prod.wtf` first.
proto-push:
	buf push proto

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

tidy:
	go mod tidy
