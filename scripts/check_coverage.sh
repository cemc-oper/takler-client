#!/usr/bin/env bash
#
# Statement coverage gate of the Go client (requirement 16.18).
#
# Measures per-package statement coverage of the non generated packages and
# fails when one of them is below the threshold. It is self contained and
# locally runnable: "make cover-check" and the CI workflow run the same command,
# so a gate failure can be reproduced without pushing.
#
# Only the hand written packages are gated:
#   - takler_protocol is generated code (protoc-gen-go output) and is excluded.
#   - the root package is main() handing over to cmd.Execute, which cannot be
#     covered by a test binary that must not exit the process; it carries no
#     logic of its own, so it is not gated either.
#
# The threshold can be raised for a single run, e.g.
#   COVERAGE_THRESHOLD=80 make cover-check
set -euo pipefail

THRESHOLD="${COVERAGE_THRESHOLD:-70}"
PACKAGES=("./common" "./cmd")

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

profile_dir="$(mktemp -d)"
trap 'rm -rf "${profile_dir}"' EXIT

failed=0

printf '%-12s %10s %10s\n' "package" "coverage" "threshold"
for package in "${PACKAGES[@]}"; do
    name="$(basename "${package}")"
    profile="${profile_dir}/${name}.out"

    if ! go test -covermode=set -coverprofile="${profile}" "${package}" >"${profile_dir}/${name}.log" 2>&1; then
        cat "${profile_dir}/${name}.log" >&2
        echo "coverage gate: tests of ${package} failed" >&2
        exit 1
    fi

    percent="$(go tool cover -func="${profile}" | awk '/^total:/ {print $3}' | tr -d '%')"
    if [[ -z "${percent}" ]]; then
        echo "coverage gate: cannot read the coverage of ${package}" >&2
        exit 1
    fi

    printf '%-12s %9s%% %9s%%\n' "${name}" "${percent}" "${THRESHOLD}"

    if awk -v got="${percent}" -v want="${THRESHOLD}" 'BEGIN { exit (got >= want) ? 0 : 1 }'; then
        continue
    fi

    echo "coverage gate: ${package} is at ${percent}%, below the ${THRESHOLD}% threshold" >&2
    failed=1
done

if [[ "${failed}" -ne 0 ]]; then
    exit 1
fi

echo "coverage gate: every gated package is at or above ${THRESHOLD}%"
