#!/usr/bin/env bash
# Rebuild the fixtures and both binaries, then run the benchmark.
# Extra arguments are passed to printbench (for example -only tiny -runs 2).
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p bin bench/out
go run ./bench/gen
go build -o bin/htmlprint ./cmd/htmlprint
go build -o bin/printbench ./bench/cmd/printbench
exec ./bin/printbench \
  -htmlprint "$PWD/bin/htmlprint" \
  -wkhtml "${WKHTMLTOPDF:-wkhtmltopdf}" \
  -fixtures "$PWD/bench/fixtures" \
  -out "$PWD/bench/out" \
  -json "$PWD/bench/results.json" \
  "$@"
