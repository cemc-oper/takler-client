.PHONY: all test cover cover-check check

export BIN_PATH := $(shell pwd)/bin

# COVERAGE_PROFILE is the merged profile of the gated packages, written by the
# cover target for a human to browse with "go tool cover -html".
export COVERAGE_PROFILE := $(shell pwd)/coverage.out

all:
	go build \
		-o ${BIN_PATH}/takler_client \
		main.go

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

# check is what CI runs, and what to run locally before pushing.
check:
	go vet ./...
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go test ./...
	./scripts/check_coverage.sh
