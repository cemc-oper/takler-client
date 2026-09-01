# takler-client

A command line client tool for [takler](https://github.com/cemc-oper/takler).

# Development

```bash
make            # build bin/takler_client
make test       # go test ./...
make cover      # write coverage.out and print the total
make cover-check # statement coverage gate: common and cmd at 70% or above
make check      # go vet, gofmt check, tests and the coverage gate
```

`takler_protocol` is generated code and is excluded from the coverage gate. The
threshold can be raised for a single run with `COVERAGE_THRESHOLD=80 make cover-check`.

# LICENSE

Copyright 2022-2024, developers at cemc-oper.

`takler` is licensed under [Apache License, Version 2.0](./LICENSE).

<span style="color:#01665e">t</span><span style="color:#5ab4ac">a</span><span style="color:#c7eae5">k</span><span style="color:#f6e8c3">l</span><span style="color:#d8b365">e</span><span style="color:#8c510a">r</span>