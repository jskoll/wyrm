#!/usr/bin/env bash
# Fail the build if total statement coverage drops below the floor.
#
# The coverage step used to print a number and throw it away, which meant a
# change that deleted tests looked exactly like one that didn't. The floor is
# deliberately a little under the current total: it exists to catch a real
# regression, not to force every patch to raise the number.
set -euo pipefail

profile="${1:-coverage.out}"
floor="${COVERAGE_FLOOR:-75}"

total=$(go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')
if [ -z "$total" ]; then
  echo "check-coverage: could not read a total from $profile" >&2
  exit 1
fi

# Integer compare, so this needs no bc/python on the runner.
if [ "${total%.*}" -lt "$floor" ]; then
  echo "check-coverage: total coverage ${total}% is below the ${floor}% floor" >&2
  exit 1
fi
echo "check-coverage: total coverage ${total}% (floor ${floor}%)"
