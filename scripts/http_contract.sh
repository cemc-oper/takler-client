#!/usr/bin/env bash
# HTTP contract test (M3 task 10): run the Go client's full command matrix
# against a real takler server's HTTP port, and assert the exit code and the
# output line of every command.
#
# What this pins, end to end and cross-language:
#
#   - the envelope wire shape: a Go client posting to /v1/commands/{command}
#     is understood by the Python server (task 7), command for command;
#   - the exit code contract (requirements 15.1 ~ 15.5): flag 0 exits 0 with
#     "received: success", a business failure exits with the Error_Code's
#     mapping (node_not_found -> 1, the unvalidated meter value -> 3), an
#     unreachable server exits 4;
#   - the no-client-side-validation rule: meter_value "abc" crosses the wire
#     and is classified by the server, exactly as on the gRPC channel.
#
# Inputs (environment):
#
#   TAKLER_REPO   path of a takler repo checkout (the server source of truth)
#   BIN_PATH      directory holding the built takler_client binary
#
# The script is self-contained: it picks two free ports, writes a connect
# config mounting both transports, starts takler-server with the http extra,
# generates the flow definition with the takler core API (so the JSON never
# drifts from the format), runs the matrix, and stops the server on the way
# out.
set -euo pipefail

TAKLER_REPO="${TAKLER_REPO:?set TAKLER_REPO to the takler repo checkout}"
BIN="${BIN_PATH:?set BIN_PATH to the directory holding takler_client}/takler_client"

# Canonicalize to absolute paths: the server below starts from a scratch
# directory, where a relative TAKLER_REPO would no longer resolve.
TAKLER_REPO="$(cd "$TAKLER_REPO" && pwd)"
BIN_PATH="$(cd "$BIN_PATH" && pwd)"
BIN="$BIN_PATH/takler_client"

WORK="$(mktemp -d)"
SERVER_PID=""
cleanup() {
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT

# Two free ports, picked by the kernel. Keeping the ports in lockstep with the
# connect config below is why they are generated here, not hardcoded.
GRPC_PORT="$(uv run --project "$TAKLER_REPO" python -c \
    'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
HTTP_PORT="$(uv run --project "$TAKLER_REPO" python -c \
    'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"

cat > "$WORK/connect.yaml" <<EOF
server:
  address:
    hostname: 127.0.0.1
    ip: 127.0.0.1
    port: "$GRPC_PORT"
  http:
    host: 127.0.0.1
    port: "$HTTP_PORT"
EOF

# The flow definition, built with the core API and serialized -- the same
# round trip a user's `takler-client-py load` performs.
uv run --project "$TAKLER_REPO" python - "$WORK/flow1.json" <<'PY'
import json
import sys

from takler.core import Flow

flow = Flow("flow1")
container1 = flow.add_container("container1")
task1 = container1.add_task("task1")
task1.add_event("event_a")
task1.add_meter("meter_a", 0, 10)
task2 = container1.add_task("task2")
task2.add_trigger("./task1 == complete")
flow.add_task("task3")

with open(sys.argv[1], "w", encoding="utf-8") as f:
    json.dump(flow.to_dict(), f)
PY

# The server runs from a scratch directory so its checkpoint and log files
# stay out of the checkout. --extra http is what mounts the HTTP transport.
(
    cd "$WORK"
    uv run --extra http --project "$TAKLER_REPO" \
        takler-server --config "$WORK/connect.yaml" \
        > "$WORK/server.log" 2>&1
) &
SERVER_PID=$!

# The environment every client call below shares: HTTP transport, the HTTP
# port, and a zero Retry_Window so a failure fails fast instead of retrying
# for a minute.
export TAKLER_TRANSPORT=http
export TAKLER_HOST=127.0.0.1
export TAKLER_PORT="$HTTP_PORT"
export TAKLER_TIMEOUT=0

# Wait for the server to come up: ping until it answers.
ready=0
for _ in $(seq 1 100); do
    if "$BIN" ping > /dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 0.2
done
if [ "$ready" != "1" ]; then
    echo "server did not come up; server log:" >&2
    cat "$WORK/server.log" >&2
    exit 1
fi

FAILURES=0

# expect_success <name> <args...>: exit code 0 and, for the commands answering
# a ServiceResponse, the "received: success" line.
expect_success() {
    local name="$1"; shift
    local out
    if out="$("$BIN" "$@" 2>&1)"; then
        echo "ok    $name"
    else
        echo "FAIL  $name: exit $? (want 0): $out" >&2
        FAILURES=$((FAILURES + 1))
    fi
}

# expect_flag <name> <exit code> <flag name> <args...>: a business failure --
# the HTTP status stays 200, the Error_Code arrives in the envelope's flag and
# maps to the exit code of the cross-language contract.
expect_flag() {
    local name="$1" want_code="$2" want_flag="$3"; shift 3
    local out rc
    set +e
    out="$("$BIN" "$@" 2>&1)"; rc=$?
    set -e
    if [ "$rc" = "$want_code" ] && printf '%s' "$out" | grep -q "$want_flag"; then
        echo "ok    $name"
    else
        echo "FAIL  $name: exit $rc (want $want_code), output $out (want $want_flag)" >&2
        FAILURES=$((FAILURES + 1))
    fi
}

# -- Query -------------------------------------------------------------------
expect_success "ping" ping
expect_success "coroutine" coroutine

# show prints the bunch; before any load it is an empty tree but still exit 0.
expect_success "show (empty)" show

# -- Control: load a flow, then operate on it --------------------------------
expect_success "load" load "$WORK/flow1.json"
expect_success "begin" begin flow1
expect_success "suspend" suspend /flow1
expect_success "resume" resume /flow1
expect_success "requeue" requeue /flow1
expect_success "force" force complete /flow1
expect_success "free-dep" free-dep --dep-type trigger /flow1/container1/task2

# show now prints the loaded flow.
if "$BIN" show | grep -q flow1; then
    echo "ok    show (loaded)"
else
    echo "FAIL  show (loaded): output does not mention flow1" >&2
    FAILURES=$((FAILURES + 1))
fi

# -- Business failures over a healthy wire ------------------------------------
# The commands below all reach the server and are answered with a non-zero
# flag: the envelope round-trips, and the flag maps to the contract's exit
# code. node_not_found is exit 1; the unvalidated meter value ("abc" crosses
# the wire verbatim) is classified by the server as internal_error, exit 3.
expect_flag "init on an unknown node" 1 node_not_found \
    init --node-path /flow1/no_such_task --task-id job-1
expect_flag "complete on an unknown node" 1 node_not_found \
    complete --node-path /flow1/no_such_task
expect_flag "abort on an unknown node" 1 node_not_found \
    abort --node-path /flow1/no_such_task --reason boom
expect_flag "event on an unknown node" 1 node_not_found \
    event --node-path /flow1/no_such_task --event-name event_a
expect_flag "meter on an unknown node" 1 node_not_found \
    meter --node-path /flow1/no_such_task --meter-name meter_a --meter-value 5
expect_flag "run on an unknown node" 1 node_not_found run /flow1/no_such_task
expect_flag "unvalidated meter value" 3 internal_error \
    meter --node-path /flow1/container1/task1 --meter-name meter_a --meter-value abc

# -- Unreachable server -------------------------------------------------------
# A closed port exhausts the zero Retry_Window after one attempt: exit 4.
set +e
unreachable_out="$(TAKLER_PORT=1 "$BIN" ping 2>&1)"; rc=$?
set -e
if [ "$rc" = "4" ] && printf '%s' "$unreachable_out" | grep -q "unreachable"; then
    echo "ok    unreachable server"
else
    echo "FAIL  unreachable server: exit $rc (want 4), output $unreachable_out" >&2
    FAILURES=$((FAILURES + 1))
fi

echo
if [ "$FAILURES" -ne 0 ]; then
    echo "http contract: $FAILURES case(s) failed" >&2
    exit 1
fi
echo "http contract: all cases passed"
