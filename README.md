# takler-client

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
| `init --task-id ID` | report job start; `ID` identifies the job instance (a scheduler job id or the PID) |
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
| `load [--flow-type json] FLOW_FILE` | load a flow definition |
| `begin [FLOW_NAME]` | start the calendar (all flows when no name is given) |

## Query commands

| Command | Purpose |
| --- | --- |
| `show` | print the node tree (`--show-trigger`, `--show-parameter`, `--show-all`, ...) |
| `ping` | health check; needs no credentials |
| `coroutine` | list the coroutines on the server's event loop |

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
| `TAKLER_PASS` | one-time job password; injected into job scripts, sent by child commands |
| `TAKLER_SECRET_FILE` | operator shared-secret file (control and query commands) |
| `TAKLER_TLS_CA_FILE` | CA certificate to trust; unset means plaintext |
| `TAKLER_TLS_SERVER_NAME` | certificate host name override |
| `TAKLER_TIMEOUT` | retry window in seconds; defaults: 86400 for child commands, 60 otherwise |
| `NO_TAKLER` | when set (any value), child commands succeed without contacting the server — for debugging scripts stand-alone |

# Exit codes

`0` success, `1` the server refused the request (or a local TLS/secret file
problem), `2` command line usage error, `3` the server failed the command or
answered something unparseable, `4` the retry window was exhausted or another
transport failure. A failure prints exactly one line on stderr, so
`set -e` in a job script does the right thing.

# Job script integration

A task's job script reports its lifecycle with the child commands; the server
injects `TAKLER_HOST`, `TAKLER_PORT`, `TAKLER_NAME` and `TAKLER_PASS` when it
generates the script. The canonical shape (see the takler tutorial's
`head.takler` / `tail.takler`):

```bash
#!/bin/bash
set -e

export TAKLER_HOST={{ TAKLER_HOST }}      # rendered by the server
export TAKLER_PORT={{ TAKLER_PORT }}
export TAKLER_NAME={{ TAKLER_NAME }}
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

`takler_protocol` is generated code and is excluded from the coverage gate. The
threshold can be raised for a single run with `COVERAGE_THRESHOLD=80 make cover-check`.

# LICENSE

Copyright 2022-2024, developers at cemc-oper.

`takler` is licensed under [Apache License, Version 2.0](./LICENSE).

<span style="color:#01665e">t</span><span style="color:#5ab4ac">a</span><span style="color:#c7eae5">k</span><span style="color:#f6e8c3">l</span><span style="color:#d8b365">e</span><span style="color:#8c510a">r</span>
