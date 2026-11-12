#!/usr/bin/env bash
# Run the full contract checks against the selected Python checkout.
set -euo pipefail

client_repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$client_repo"
TAKLER_REPO="$(cd "${TAKLER_REPO:-../takler}" && pwd -P)"
export TAKLER_REPO

# The child-identity and service-state fixtures build Go from this sibling.
if [[ ! "$client_repo" -ef "$TAKLER_REPO/../takler-client" ]]; then
    echo "check-paired: the Python fixtures expect this Go checkout at $TAKLER_REPO/../takler-client" >&2
    exit 1
fi

(
    cd "$TAKLER_REPO"
    uv sync --locked --all-groups --extra http
    TAKLER_CLIENT_REPO="$client_repo" bash scripts/check_proto.sh
)
make proto-check wire-check
bash scripts/check_drift_negative.sh
make http-contract load-contract replace-contract show-contract query-a-contract query-b-contract

cd "$TAKLER_REPO"
workflow_test=tests/unit/packaging/test_ci_workflow.py
if [[ ! -f "$workflow_test" ]]; then
    workflow_test=tests/packaging/test_ci_workflow.py
fi
uv run --no-sync pytest -q "$workflow_test" \
    tests/integration/test_child_identity_matrix.py \
    tests/integration/test_server_state_cli_matrix.py
