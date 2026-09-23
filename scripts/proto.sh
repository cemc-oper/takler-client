#!/usr/bin/env bash
#
# Proto sync and drift gate for the Go client.
#
# src/takler/server/protocol/takler.proto in the takler repo is the single
# source of truth of the wire protocol. This script keeps the copy in
# takler_protocol/ and the generated Go stubs in step with it:
#
#   bash scripts/proto.sh sync    copy takler.proto from the takler repo and
#                                 regenerate the Go stubs
#   bash scripts/proto.sh check   fail when takler_protocol/takler.proto differs
#                                 from the source of truth, or when the checked in
#                                 stubs differ from a fresh regeneration
#
# "make proto-sync" / "make proto-check" are thin wrappers; CI runs the check
# mode so a proto change merged on only one side turns that side red.
#
# The takler repo location defaults to ../takler (the workspace layout) and
# can be overridden:
#   TAKLER_REPO=/path/to/takler bash scripts/proto.sh check
#
# protoc comes from the takler repo's locked uv environment (grpcio-tools in
# its dev dependency group), so the compiler version follows takler's uv.lock
# and needs no separate installation. The two Go plugins are not in that
# environment; they are pinned below and installed with "go install" on
# demand, so local runs and CI produce byte identical stubs.
set -euo pipefail

PROTOC_GEN_GO_VERSION="v1.35.2"
PROTOC_GEN_GO_GRPC_VERSION="v1.5.1"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

takler_repo="${TAKLER_REPO:-${repo_root}/../takler}"
source_proto="${takler_repo}/src/takler/server/protocol/takler.proto"
proto_dir="takler_protocol"

if [[ ! -f "${source_proto}" ]]; then
    echo "proto: cannot find the source of truth at ${source_proto}" >&2
    echo "proto: set TAKLER_REPO to a checkout of the takler repo" >&2
    exit 1
fi

gobin="$(go env GOBIN)"
if [[ -z "${gobin}" ]]; then
    gobin="$(go env GOPATH)/bin"
fi

# ensure_plugin <binary> <module@version> <expected --version output>
ensure_plugin() {
    local binary="$1"
    local module="$2"
    local want="$3"

    if [[ -x "${gobin}/${binary}" ]] && [[ "$("${gobin}/${binary}" --version 2>/dev/null)" == "${want}" ]]; then
        return
    fi
    echo "proto: installing ${module}" >&2
    go install "${module}"
    if [[ "$("${gobin}/${binary}" --version 2>/dev/null)" != "${want}" ]]; then
        echo "proto: ${binary} is not at the pinned version after install" >&2
        echo "proto: got '$("${gobin}/${binary}" --version 2>/dev/null)', want '${want}'" >&2
        exit 1
    fi
}

ensure_plugin protoc-gen-go "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}" \
    "protoc-gen-go ${PROTOC_GEN_GO_VERSION}"
ensure_plugin protoc-gen-go-grpc "google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION}" \
    "protoc-gen-go-grpc ${PROTOC_GEN_GO_GRPC_VERSION#v}"

# generate <output dir>: run protoc (from takler's locked uv environment)
# with the pinned plugins. paths=source_relative keeps the output under
# <output dir>/takler_protocol/.
generate() {
    local out_dir="$1"
    mkdir -p "${out_dir}"
    uv run --project "${takler_repo}" --locked python -m grpc_tools.protoc \
        -I. \
        --plugin=protoc-gen-go="${gobin}/protoc-gen-go" \
        --plugin=protoc-gen-go-grpc="${gobin}/protoc-gen-go-grpc" \
        --go_out="${out_dir}" --go_opt=paths=source_relative \
        --go-grpc_out="${out_dir}" --go-grpc_opt=paths=source_relative \
        "${proto_dir}/takler.proto"
}

sync() {
    cp "${source_proto}" "${proto_dir}/takler.proto"
    generate "${repo_root}"
    echo "proto: ${proto_dir}/ is in sync with ${source_proto}"
}

check() {
    local failed=0

    if ! cmp -s "${source_proto}" "${proto_dir}/takler.proto"; then
        echo "proto: ${proto_dir}/takler.proto differs from ${source_proto}" >&2
        echo "proto: run \"bash scripts/proto.sh sync\" in this repo to catch up" >&2
        failed=1
    fi

    # Not declared local: the EXIT trap below runs after check() returns,
    # when a local would already be out of scope under "set -u".
    gen_dir="$(mktemp -d)"
    trap 'rm -rf "${gen_dir}"' EXIT
    generate "${gen_dir}"
    local stub
    for stub in takler.pb.go takler_grpc.pb.go; do
        if ! diff -u "${proto_dir}/${stub}" "${gen_dir}/${proto_dir}/${stub}"; then
            echo "proto: ${proto_dir}/${stub} is stale; regenerate with \"bash scripts/proto.sh sync\"" >&2
            failed=1
        fi
    done

    if [[ "${failed}" -ne 0 ]]; then
        exit 1
    fi
    echo "proto: ${proto_dir}/ matches ${source_proto} and the stubs are fresh"
}

case "${1:-}" in
    sync) sync ;;
    check) check ;;
    *)
        echo "usage: $0 {sync|check}" >&2
        exit 2
        ;;
esac
