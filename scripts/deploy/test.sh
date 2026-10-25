#!/usr/bin/env bash
# Rollback checks must not depend on the application suite or workflow-linter proxy.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
shellcheck scripts/deploy/*.sh scripts/deploy/test/bin/* scripts/deploy/test/*.bats
bats scripts/deploy/test
