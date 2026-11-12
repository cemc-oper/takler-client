.PHONY: all vet fmt-check test cover cover-check proto-sync proto-check http-contract load-contract replace-contract query-a-contract query-b-contract wire-check check check-fast check-paired

export BIN_PATH := $(shell pwd)/bin

# COVERAGE_PROFILE is the merged profile of the gated packages, written by the
# cover target for a human to browse with "go tool cover -html".
export COVERAGE_PROFILE := $(shell pwd)/coverage.out

VERSION ?= dev
COMMIT ?= $(shell git rev-parse HEAD)

all:
	mkdir -p ${BIN_PATH}
	go build -trimpath -ldflags "-X github.com/cemc-oper/takler-client/cmd.Version=$(VERSION) -X github.com/cemc-oper/takler-client/cmd.Commit=$(COMMIT)" \
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

# cover-check writes the merged profile and enforces the per-package coverage
# gate for the non-generated packages (requirement 16.18).
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
# (make all) first. The full paired check calls this target.
http-contract: all
	TAKLER_REPO="$${TAKLER_REPO:-../takler}" bash ./scripts/http_contract.sh

# Reproduce the Go checks that run on pushes and pull requests.
check-fast: vet fmt-check test cover-check

# Include protocol drift checks against the Python checkout.
check: check-fast proto-check wire-check

check-paired:
	bash ./scripts/check_paired.sh

# Both CLIs and both transports against the paired Python checkout.
load-contract: all
	TAKLER_REPO="$${TAKLER_REPO:-../takler}" bash ./scripts/load_contract.sh

replace-contract: all
	TAKLER_REPO="$${TAKLER_REPO:-../takler}" bash ./scripts/replace_contract.sh

wire-check:
	cmp common/wire_schema.json "$${TAKLER_REPO:-../takler}/src/takler/protocol/wire_schema.json"
	cmp common/testdata/http_vectors.json "$${TAKLER_REPO:-../takler}/tests/protocol/fixtures/http_vectors.json"
	cmp common/query_v1_schema.json "$${TAKLER_REPO:-../takler}/src/takler/protocol/query_v1_schema.json"
	cmp common/testdata/query_v1_vectors.json "$${TAKLER_REPO:-../takler}/tests/protocol/fixtures/query_v1_vectors.json"

.PHONY: show-contract
show-contract: all
	bash scripts/show_contract.sh

query-a-contract: all
	bash scripts/query_a_contract.sh

query-b-contract: all
	bash scripts/query_b_contract.sh
