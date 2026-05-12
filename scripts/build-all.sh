#!/usr/bin/env bash
# Build argusd for every architecture listed in .github/workflows/release.yml.
# Mirrors the CI matrix so local tarballs byte-match Release artifacts.
#
# Usage:  ./scripts/build-all.sh [version]
# Output: dist/argusd_<version>_<target>.tar.gz + SHA256SUMS
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"

# target spec: name|GOOS|GOARCH|GOARM|GOMIPS   (empty trailing fields OK)
TARGETS=(
  "linux-amd64|linux|amd64||"
  "linux-386|linux|386||"
  "linux-arm64|linux|arm64||"
  "linux-armv5|linux|arm|5|"
  "linux-armv7|linux|arm|7|"
  "linux-mips-softfloat|linux|mips||softfloat"
  "linux-mipsle-softfloat|linux|mipsle||softfloat"
  "linux-mips64-softfloat|linux|mips64||softfloat"
  "linux-mips64le-softfloat|linux|mips64le||softfloat"
  "linux-riscv64|linux|riscv64||"
)

rm -rf dist && mkdir -p dist
echo ">>> building argusd ${VERSION} for ${#TARGETS[@]} targets"

for spec in "${TARGETS[@]}"; do
  IFS='|' read -r name goos goarch goarm gomips <<<"$spec"
  echo
  echo "=== ${name}"
  stage="dist/argusd_${VERSION}_${name}"
  mkdir -p "${stage}"
  env CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      ${goarm:+GOARM=${goarm}} ${gomips:+GOMIPS=${gomips}} \
      go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o "${stage}/argusd" \
        ./cmd/argusd
  cp README.md LICENSE ONLINE.md OFFLINE.md "${stage}/"
  tar -C dist -czf "dist/argusd_${VERSION}_${name}.tar.gz" "argusd_${VERSION}_${name}"
  rm -rf "${stage}"
  ls -lh "dist/argusd_${VERSION}_${name}.tar.gz"
done

echo
echo ">>> sha256"
( cd dist && shasum -a 256 *.tar.gz > SHA256SUMS && cat SHA256SUMS )
