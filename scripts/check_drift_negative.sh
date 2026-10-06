#!/usr/bin/env bash
# Prove the paired proto and wire gates reject a deliberately mismatched peer.
set -euo pipefail

client_repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
takler_repo="$(cd "${TAKLER_REPO:-${client_repo}/../takler}" && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

mkdir -p "$work_dir/client/scripts" "$work_dir/client/takler_protocol" "$work_dir/takler/src/takler/protocol" "$work_dir/takler/tests/protocol/fixtures"
cp "$client_repo/scripts/proto.sh" "$work_dir/client/scripts/proto.sh"
cp "$client_repo/takler_protocol/takler.pb.go" "$client_repo/takler_protocol/takler_grpc.pb.go" "$work_dir/client/takler_protocol/"
cp "$client_repo/takler_protocol/takler.proto" "$work_dir/client/takler_protocol/takler.proto"
printf '\n// deliberate peer drift\n' >> "$work_dir/client/takler_protocol/takler.proto"
if TAKLER_REPO="$takler_repo" bash "$work_dir/client/scripts/proto.sh" check > "$work_dir/go-proto.log" 2>&1; then
    echo "Go proto drift gate accepted a mismatched peer" >&2
    exit 1
fi
if ! grep -q 'takler_protocol/takler.proto differs' "$work_dir/go-proto.log"; then
    cat "$work_dir/go-proto.log" >&2
    echo "Go proto gate failed for a reason other than peer drift" >&2
    exit 1
fi
if TAKLER_CLIENT_REPO="$work_dir/client" bash "$takler_repo/scripts/check_proto.sh" > "$work_dir/proto.log" 2>&1; then
    echo "proto drift gate accepted a mismatched peer" >&2
    exit 1
fi
if ! grep -q 'differs from takler-client' "$work_dir/proto.log"; then
    cat "$work_dir/proto.log" >&2
    echo "proto gate failed for a reason other than peer drift" >&2
    exit 1
fi

cp "$takler_repo/src/takler/protocol/wire_schema.json" "$work_dir/takler/src/takler/protocol/wire_schema.json"
cp "$takler_repo/tests/protocol/fixtures/http_vectors.json" "$work_dir/takler/tests/protocol/fixtures/http_vectors.json"
printf '\n' >> "$work_dir/takler/src/takler/protocol/wire_schema.json"
if TAKLER_REPO="$work_dir/takler" make -C "$client_repo" wire-check > "$work_dir/wire.log" 2>&1; then
    echo "wire drift gate accepted a mismatched peer" >&2
    exit 1
fi
if ! grep -q 'wire_schema.json' "$work_dir/wire.log"; then
    cat "$work_dir/wire.log" >&2
    echo "wire gate failed for a reason other than peer drift" >&2
    exit 1
fi
echo "Python proto, Go proto and wire drift negative checks: OK"
