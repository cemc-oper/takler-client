#!/usr/bin/env bash
# R2-12A: real paired initial-query edge cases and fixed 100k matrix.
set -euo pipefail
TAKLER_REPO="$(cd "${TAKLER_REPO:-../takler}" && pwd)"
export TAKLER_QUERY_GO_CLIENT="$(pwd)/bin/takler_client"
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"
export TAKLER_REQUIRE_PAIRED_QUERY=1
cd "$TAKLER_REPO"
uv run pytest -q tests/integration/test_query_a_matrix.py
uv run python scripts/query_a_matrix_100k.py
