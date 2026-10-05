# Contributing

Use Go 1.25.6 or newer. Run `make check` and `make build`; install `actionlint`
and GoReleaser for release/workflow validation. Changes to command behavior
should include a runnable check for their failure modes and update the README
and compatibility matrix.

Use fictional OCI identifiers, hosts, usernames and comments in public tests.
Keep live evidence outside Git. Do not require cloud accounts in CI or embed
workplace code in examples. Prefer OCI CLI's existing credential handling over
a second authentication store.

Use Conventional Commit subjects. Release tags are explicit. An initial PR
does not authorize a tag, publication to a package registry, or a tap change.
