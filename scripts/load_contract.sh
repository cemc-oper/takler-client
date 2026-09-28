#!/usr/bin/env bash
# Paired Python/Go CLI load contract over real gRPC and HTTP listeners.
set -euo pipefail
TAKLER_REPO="$(cd "${TAKLER_REPO:-../takler}" && pwd)"
TAKLER_LOAD_GO_CLIENT="$(pwd)/bin/takler_client"
export TAKLER_LOAD_GO_CLIENT
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"
cd "$TAKLER_REPO"
uv run pytest tests/integration/test_load_cli_contract.py -q
