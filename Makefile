.PHONY: all vet fmt-check test cover cover-check proto-sync proto-check http-contract check

export BIN_PATH := $(shell pwd)/bin

# COVERAGE_PROFILE is the merged profile of the gated packages, written by the
# cover target for a human to browse with "go tool cover -html".
export COVERAGE_PROFILE := $(shell pwd)/coverage.out

all:
	go build \
		-o ${BIN_PATH}/takler_client \
		main.go

vet:
	go vet ./...

# fmt-check fails when some file is not gofmt clean. gofmt itself always exits 0,
# so the exit code has to come from its output being non empty; the file list is
# printed again to say which files need formatting.
fmt-check:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

test:
	go test ./...

# cover writes a merged coverage profile of the non generated packages.
cover:
	go test -covermode=set -coverprofile=${COVERAGE_PROFILE} ./common/... ./cmd/...
	go tool cover -func=${COVERAGE_PROFILE} | tail -n 1

# cover-check is the coverage gate (requirement 16.18): per package statement
# coverage of the non generated packages, failing below the threshold.
cover-check:
	./scripts/check_coverage.sh

# proto-sync copies takler.proto from the takler repo (the single source of
# truth, TAKLER_REPO environment variable, default ../takler) and regenerates
# the Go stubs. Run this after the proto changed on the takler side.
proto-sync:
	bash ./scripts/proto.sh sync

# proto-check is the drift gate: it fails when takler_protocol/takler.proto
# differs from the takler repo's copy, or when the checked in stubs differ
# from a fresh regeneration.
proto-check:
	bash ./scripts/proto.sh check

# http-contract runs the Go client's full command matrix over HTTP against a
# real takler server (M3 task 10), asserting every command's exit code and
# output line. TAKLER_REPO points at the takler checkout, default ../takler;
# the script builds the server environment with uv and needs the binary built
# (make all) first. It is not part of "make check" because it needs the
# sibling repo; CI runs it as its own job.
http-contract:
	TAKLER_REPO="$${TAKLER_REPO:-../takler}" bash ./scripts/http_contract.sh

# check is what CI runs, and what to run locally before pushing. The CI workflow
# invokes these same targets one per step, so that the failing step is visible in
# the run summary while "make check" stays the single local entry point.
check: vet fmt-check test cover-check proto-check
