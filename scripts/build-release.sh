#!/usr/bin/env bash
# Builds the release archives of one version into a folder, with SHA256SUMS.
# Usage: scripts/build-release.sh v0.1.0 dist
set -euo pipefail
cd "$(dirname "$0")/.."

version="${1:?usage: build-release.sh <version> <out dir>}"
out="${2:?usage: build-release.sh <version> <out dir>}"

# Windows and Linux are the supported platforms. macOS builds serve the
# macOS Llama app exception in docs/DESIGN_PRINCIPLES.md.
targets=(
  windows/amd64 windows/arm64
  linux/amd64 linux/arm64
  darwin/arm64 darwin/amd64
)

rm -rf "$out"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="llamaenv-${version}-${goos}-${goarch}"
  exe="llamaenv"
  [[ "$goos" == windows ]] && exe="llamaenv.exe"
  mkdir -p "$work/$name"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
    -ldflags "-X github.com/osolmaz/llamaenv/internal/cli.Version=${version}" \
    -o "$work/$name/$exe" .
  cp README.md LICENSE "$work/$name/"
  if [[ "$goos" == windows ]]; then
    (cd "$work" && zip -qr "$out/$name.zip" "$name")
  else
    tar --no-xattrs -C "$work" -czf "$out/$name.tar.gz" "$name"
  fi
done

(cd "$out" && shasum -a 256 -- *.zip *.tar.gz > SHA256SUMS)
cat "$out/SHA256SUMS"
