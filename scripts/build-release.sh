#!/bin/sh
# Builds the release binaries (Linux amd64 and arm64) into dist/ with their
# SHA-256, to attach to each version. Needs Go 1.27 or later; without it:
#   podman run --rm -v "$PWD:/src:Z" -w /src docker.io/library/golang:1.27 scripts/build-release.sh
set -eu
cd "$(dirname "$0")/.."
version="${1:-dev}"
rm -rf dist && mkdir dist
for arch in amd64 arm64; do
	out="dist/qadrant-server-${version}-linux-${arch}"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -tags timetzdata -ldflags "-s -w" -o "$out" .
done
(cd dist && sha256sum qadrant-server-* > SHA256SUMS)
cat dist/SHA256SUMS
