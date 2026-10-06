# takler-client

![Maturity-Sandbox](https://img.shields.io/badge/Maturity-Sandbox-F9D71C)
![ci](https://github.com/cemc-oper/takler-client/actions/workflows/ci.yml/badge.svg)

A command line client tool for [takler](https://github.com/cemc-oper/takler).

`takler_client` talks to a running `takler-server`: it reports job lifecycle
events from inside job scripts (the *child* commands), runs operator actions
against the node tree (the *control* commands) and queries the server (the
*query* commands). It speaks both transports takler serves — gRPC (the
default) and HTTP — with identical exit codes and stderr wording on either.

# Install

Build from source (Go >= 1.23):

```bash
make            # builds bin/takler_client
```

Copy `bin/takler_client` anywhere on the `PATH` of the accounts that run job
scripts or operate the server. The binary has no runtime dependencies.

# Commands

Every command accepts `--host` / `--port`; child commands additionally take
`--node-path` (defaults to `$TAKLER_NAME`, which the server injects into job
scripts).

## Child commands (inside job scripts)

| Command | Purpose |
| --- | --- |
| `init --task-id ID` | report job start; `ID` is the job ID (`TAKLER_RID`, such as a scheduler job id or PID), separate from the attempt UUID |
| `complete` | report successful completion |
| `abort [--reason TEXT]` | report failure |
| `event --event-name NAME` | set an event |
| `meter --meter-name NAME --meter-value VALUE` | update a meter |

## Control commands (operations)

| Command | Purpose |
| --- | --- |
| `requeue PATH...` | requeue nodes |
| `suspend PATH...` / `resume PATH...` | suspend / resume nodes |
| `run [--force] PATH...` | submit tasks manually |
| `force STATE PATH...` | force a state (`unknown`..`aborted`) or set/clear an event (`PATH:EVENT` with `set`/`clear`); `--recursive` defaults to true |
| `free-dep [--dep-type all\|time\|trigger] PATH...` | release dependencies |
| `load [--flow-type json] FLOW_FILE` | load a new single Flow DefinitionDocument v1; rejects existing names; requires explicit begin |
| `begin [FLOW_NAME]` | start the calendar (all flows when no name is given) |
| `server-halt` / `server-resume` | halt new service execution / resume after manual checks; distinct from node `suspend` / `resume` |

## Query commands

| Command | Purpose |
| --- | --- |
| `show` | print the node tree and root service status, including current task attempts and file references (`--show-trigger`, `--show-parameter`, `--show-all`, ...) |
| `ping` | health check; needs no credentials |
| `coroutine` | list the coroutines on the server's event loop |
| `server-status` | show service status, halt causes, and checkpoint recovery summary |

`server-halt` blocks both automatic work and manual `run --force`. Restored
services start halted; inspect `server-status` and `show`, reconcile external
jobs, then use `server-resume`. A successful control response describes an
in-memory change; the next checkpoint may not have been written yet.
The service keeps deployment settings separately from its checkpointed
running/halted state; restoring a checkpoint does not replace the current
server address.

Run `takler_client <command> --help` for the full option list of a command.

# Connecting to the server

Address resolution, highest precedence first: `--host` / `--port` > the
`connect.yaml` file named by `TAKLER_CONNECT_FILE` > `TAKLER_HOST` /
`TAKLER_PORT` > the built-in default `localhost:33083`.

The transport is selected by the `server.transport` field of `connect.yaml`
(`grpc` or `http`), falling back to the `TAKLER_TRANSPORT` environment
variable, then to gRPC. With `http` selected and a `server.http` section
present, the client dials `server.http.port` instead of `address.port`; an
explicit `--port` still wins. The retry window, backoff and exit codes are
identical on both transports, so job scripts do not need to know which one
carries them.

The three TLS/credential settings exist as persistent flags, environment
variables and `connect.yaml` `security` fields, resolved in that order:
`--tls-ca` / `TAKLER_TLS_CA_FILE` / `security.ca_file`,
`--tls-server-name` / `TAKLER_TLS_SERVER_NAME` / `security.server_name` and
`--secret-file` / `TAKLER_SECRET_FILE` / `security.operator_secret_file`.
Unlike on the Python client, `TAKLER_TLS_SERVER_NAME` also works over HTTP.

# Environment variables

| Variable | Meaning |
| --- | --- |
| `TAKLER_HOST` / `TAKLER_PORT` | server address (below the connect config in precedence) |
| `TAKLER_CONNECT_FILE` | path of the shared `connect.yaml` |
| `TAKLER_TRANSPORT` | `grpc` (default) or `http` |
| `TAKLER_NAME` | default node path of child commands; injected into job scripts |
| `TAKLER_ATTEMPT_ID` | current execution attempt UUID; required by every child command unless `--attempt-id` is given |
| `TAKLER_RID` | job ID reported by `init --task-id`; may be reused independently of the attempt UUID |
| `TAKLER_PASS` | one-time job password; injected into job scripts, sent by child commands |
| `TAKLER_SECRET_FILE` | operator shared-secret file (control and query commands) |
| `TAKLER_TLS_CA_FILE` | CA certificate to trust; unset means plaintext |
| `TAKLER_TLS_SERVER_NAME` | certificate host name override |
| `TAKLER_TIMEOUT` | read-only retry window in seconds (default 60); mutating commands, including child reports, send once |
| `NO_TAKLER` | when set (any value), child commands succeed without contacting the server — for debugging scripts stand-alone |

`TAKLER_ATTEMPT_ID` identifies one execution attempt, even when `try_no` resets
after `requeue`. `TAKLER_RID` names the external job reported by `init`;
`TAKLER_PASS` is the credential. The UUID alone does not authorize a report.

# Exit codes

`0` success, `1` the server refused the request (or a local TLS/secret file
problem), `2` command line usage error, `3` the server failed the command or
answered something unparseable, `4` the retry window was exhausted or another
transport failure. A failure prints exactly one line on stderr, so
`set -e` in a job script does the right thing.

# Job script integration

A task's job script reports its lifecycle with the child commands; the server
injects `TAKLER_HOST`, `TAKLER_PORT`, `TAKLER_NAME`, `TAKLER_ATTEMPT_ID` and `TAKLER_PASS` when it
generates the script. The canonical shape (see the takler tutorial's
`head.takler` / `tail.takler`):

```bash
#!/bin/bash
set -e

export TAKLER_HOST={{ TAKLER_HOST }}      # rendered by the server
export TAKLER_PORT={{ TAKLER_PORT }}
export TAKLER_NAME={{ TAKLER_NAME }}
export TAKLER_ATTEMPT_ID={{ TAKLER_ATTEMPT_ID }}
export TAKLER_PASS={{ TAKLER_PASS }}
export TAKLER_RID=${SLURM_JOB_ID:-$$}     # job instance id

takler_client init --task-id ${TAKLER_RID}

ERROR() {
    set +e
    wait
    takler_client abort
    trap 0
    exit 0
}
trap ERROR 0
trap '{ echo "Killed by a signal"; trap 0; ERROR; }' 1 2 3 4 5 6 7 8 10 12 13 15

# ... the real work; report progress along the way:
takler_client meter --meter-name step --meter-value 5
takler_client event --event-name half_done

wait
takler_client complete
trap 0
exit 0
```

To talk to the HTTP listener instead of gRPC, export `TAKLER_TRANSPORT=http`
and point `TAKLER_PORT` at the server's HTTP port (or use a `connect.yaml`
with `server.transport: http`); nothing else in the script changes.

# Development

```bash
make            # build bin/takler_client
make test       # go test ./...
make cover      # write coverage.out and print the total
make cover-check # statement coverage gate: common and cmd at 70% or above
make check      # go vet, gofmt check, tests and the coverage gate
make http-contract  # contract test of all commands over HTTP against a real takler server
```

## Release verification

`make VERSION=v1.2.3 COMMIT=<full-commit-sha>` embeds the source identity in
`bin/takler_client --version`. The release workflow checks out an existing tag,
requires the latest `ci.yml` run for that exact main-branch commit and its Go
and paired-contract jobs to pass, then builds Linux amd64 and arm64 binaries.
It records the tag, commit, CI run, and SHA-256 hashes in
`release-manifest.json`; `SHA256SUMS` can be checked with `sha256sum -c` from
the downloaded artifact directory.

To validate a release without uploading anything, run the **release** workflow
manually with an existing remote tag and leave `dry_run` enabled. The build job
uploads only a GitHub Actions artifact. A tag push, or a manual run with
`dry_run` disabled, creates the GitHub Release after rechecking the tag and
downloaded checksums. A failed or missing exact-commit CI run blocks the build.

`takler_protocol` is generated code and is excluded from the coverage gate. The
threshold can be raised for a single run with `COVERAGE_THRESHOLD=80 make cover-check`.

# LICENSE

Copyright 2022-2024, developers at cemc-oper.

`takler` is licensed under [Apache License, Version 2.0](./LICENSE).

<span style="color:#01665e">t</span><span style="color:#5ab4ac">a</span><span style="color:#c7eae5">k</span><span style="color:#f6e8c3">l</span><span style="color:#d8b365">e</span><span style="color:#8c510a">r</span>

### Batch control results

`requeue`, `suspend`, `resume`, `run`, `force`, `free-dep`, and `begin` return
ordered best-effort results. Invalid targets and execution failures do not stop
remaining targets; successful operations are not rolled back. Duplicates and
overlapping parent/child targets execute in the supplied order. Each result
prints its index, target, error classification and effect (`none`, `applied`,
`partial`, or `unknown`), followed by a summary on stdout. Any failed item
produces `batch_failed` (16), an error summary on stderr, and exit code 1.
Both HTTP and gRPC make only one attempt for these mutations, regardless of
`TAKLER_TIMEOUT`. A lost response can mean the operation already took effect.

The load input is a single Flow `takler.definition` document with `schema_version: 1`,
exported using Python `takler.serialization.export_definition(flow).model_dump_json()`.
Load rejects existing names (`flow_state`, flag 14) and invalid or legacy documents
(`invalid_request`, flag 15); both failures exit 1 and preserve the existing tree.
Run `begin FLOW_NAME` explicitly after a successful load.

Run `TAKLER_REPO=../takler make load-contract` to validate Python and Go load CLIs
over both gRPC and HTTP against the paired Python checkout (including rejection
and exit codes). The Python checkout needs its locked development/HTTP dependencies.

### Replace a flow

```sh
takler_client replace /forecast forecast.json
make replace-contract
```

`replace TARGET_PATH FLOW_FILE` requires operator credentials and a UTF-8
DefinitionDocument containing one Flow with the matching name. Server state and
resource gates cannot be bypassed. Success says `flow replaced in memory;
checkpoint pending`: persistence is asynchronous; a crash before the next
checkpoint may lose the replacement. The target must be an existing Flow;
active/submitted state on the Flow or any descendant, outstanding resource
usage, or a changed target identity prevents replacement. Suspension does not
bypass these checks. Success begins the new Flow and preserves only the old
Flow's own suspended flag; descendant runtime progress is reset.

Takler provides neither cross-restart request deduplication nor exactly-once submission. Repeating
replace initializes the Flow again. YAML, legacy mixed trees, checkpoints,
query projections, Bunch roots and runtime fields are not accepted as load or
replace definitions. Custom types must be registered by trusted server startup
code; clients cannot request dynamic module imports.

Both gRPC and HTTP send all
mutation commands (including child commands, load and replace) once. Only ping,
show and coroutine may retry transient failures. After an ambiguous mutation
failure, query server state before sending it again. TLS/configuration errors,
malformed responses and business failures are never retried.

HTTP requires complete typed envelopes and payloads, canonical base64 bytes and
signed int64 decimal strings for meters. Invalid HTTP requests exit 1; malformed
200 responses exit 3. `make check` also compares the shared wire schema and seed
vectors with the paired Python checkout. `make replace-contract` exercises both
CLIs and both transports against a real Python server.

### Safe show queries

`show` prints a connection banner followed by the server's JSON projection over
both HTTP and gRPC. Nodes carry
`node_kind`, safe `generated_parameters`, and `redacted_parameters`; root user
parameters are retained. Unknown execution type labels require no local plugin.
Parameters matching built-in credential names or the server's
`security.query_redacted_parameters` list have the value `<redacted>`. The name
list distinguishes redaction from a literal value. This view is for inspection,
not a definition or checkpoint document.

The R1-16 local synthetic 10k-task `show` response was about 7.85 MB and
exceeded the default gRPC client's 4 MiB receive limit. That sample has no
valid gRPC `show` latency; smaller-tree measurements do not establish a
supported production scale or SLO.

Run `UV_CACHE_DIR=/tmp/takler-uv-cache make show-contract` to exercise both CLIs
against the paired Python server over HTTP and gRPC, including unknown task types
and parameter redaction. `TAKLER_REPO` selects the Python checkout.

## Paired repository CI

CI checks the other repository's `main` by default. For an unmerged protocol
change, manually run the workflow on the current candidate ref with `peer_sha`
set to the other candidate's full commit SHA. Empty input uses `main`.
Both actual SHAs appear in the job log and GitHub Actions summary. Default
reruns may pick up newer peer code; use the recorded SHAs for exact replay.

Locally, prepare both checkouts and run from `takler-client/`:

```sh
export TAKLER_REPO=../takler
(
  cd "$TAKLER_REPO"
  uv sync --locked --all-groups --extra http
  TAKLER_CLIENT_REPO=../takler-client bash scripts/check_proto.sh
)
make proto-check wire-check
make http-contract load-contract replace-contract show-contract
```

These commands allow local edits and do not switch branches. They check proto
freshness, shared schema/vectors, HTTP commands, and Python/Go × gRPC/HTTP
load, replace and show behavior. No separate runner or JSON report is needed.

For protocol changes, regenerate Python bindings first, then run `make proto-sync`
in Go against that Python checkout and commit the generated files. Validate both
candidates locally or with the manual SHA override before merging. Prefer an
additive server change followed by its client change, then rerun default CI.
Regular PR checks still use peer main; a manual candidate run does not replace
required PR checks. Incompatible changes require coordinating merge order.
After squash/rebase, use actual merged SHAs for replay. Manual dispatch requires
the workflow to exist on the default branch; use local checks before initial rollout.

## Job files and HPC execution

New job files inherit filesystem default ACL/umask behavior; takler only adds
owner execute. Existing files are written directly, and a failed write can leave
partial content. Operators manage the trusted job directories and permissions.
HPC scheduler interaction is provided by orvix through the configured submission
command. The local submission process does not restrict jobs to local execution.
`async_task` is explicitly rejected at definition and execution boundaries;
the full asynchronous task model is outside R1.
