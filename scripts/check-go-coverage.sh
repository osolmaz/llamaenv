#!/usr/bin/env bash
# Fails when total test coverage is below the threshold in slophammer.yml.
set -euo pipefail
cd "$(dirname "$0")/.."

minimum_coverage="85"
go test -coverpkg=./... -coverprofile=coverage.out ./...
total="$(go tool cover -func=coverage.out | awk '/^total:/ {print substr($3, 1, length($3)-1)}')"
echo "total coverage: ${total}% (minimum ${minimum_coverage}%)"
awk -v total="$total" -v minimum="$minimum_coverage" 'BEGIN { exit !(total + 0 >= minimum + 0) }'
