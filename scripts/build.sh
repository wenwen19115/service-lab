#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
(cd web && npm ci && npm run build)
go test ./...
mkdir -p dist
build_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go build -ldflags "-X service-lab/internal/lab.BuildTime=$build_time" -o dist/service-lab .
