#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
(cd web && npm ci && npm run build)
go test ./...
mkdir -p dist
go build -o dist/service-lab .
