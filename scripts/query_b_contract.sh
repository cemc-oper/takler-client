#!/usr/bin/env bash
# R2-12B: paired delta, reset, and restart matrix plus fixed 100k sync.
set -euo pipefail
TAKLER_REPO="$(cd "${TAKLER_REPO:-../takler}" && pwd)"
export TAKLER_QUERY_GO_CLIENT="$(pwd)/bin/takler_client"
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"
export TAKLER_REQUIRE_PAIRED_QUERY=1
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY GRPC_PROXY http_proxy https_proxy all_proxy grpc_proxy
cd "$TAKLER_REPO"
uv run pytest -q tests/integration/test_query_b_matrix.py tests/server/test_query_since.py
uv run python scripts/query_b_matrix_100k.py
