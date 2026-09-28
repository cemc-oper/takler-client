#!/usr/bin/env bash
# Paired Python/Go CLI show contract over real gRPC and HTTP listeners.
set -euo pipefail
TAKLER_REPO="$(cd "${TAKLER_REPO:-../takler}" && pwd)"
TAKLER_SHOW_GO_CLIENT="$(pwd)/bin/takler_client"
export TAKLER_SHOW_GO_CLIENT
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"
cd "$TAKLER_REPO"
uv run pytest tests/integration/test_show_cli_contract.py -q
