#!/usr/bin/env bash
# Cross-compiles sing-box-tray for Windows from a Linux/macOS/WSL host.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

OUTPUT="build/sing_box_tray_runner.exe"

mkdir -p build
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
	-ldflags="-H windowsgui -s -w" \
	-o "$OUTPUT" \
	.

echo "Built: $OUTPUT"
