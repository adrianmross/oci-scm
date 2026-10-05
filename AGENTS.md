# Agent instructions

- Keep this public repository generic. Never commit internal source bundles, PR
  comments, identifiers, hostnames, session material, or live evidence.
- Use an isolated WorkTrunk for changes. Preserve existing local changes.
- Use `make check` and `make build`. Tests use fake OCI responses and temporary
  Git repositories; they must never require live credentials or mutate OCI.
- Match gh-style command names and flags where OCI has an equivalent. Document
  unsupported behavior rather than silently approximating it.
- Read-back evidence is required for claimed remote success. A submitted
  asynchronous operation is not a completed operation.
- CLI flags override environment, which overrides a deliberately named context,
  then repo defaults and the current optional oci-context context.
- Never read private key/token files. Delegate authentication to OCI CLI.
- Mutations produce plans by default; `--apply` is the execution gate. Never add
  force-push, implicit comment resolution, automatic merge, or deployment to a
  handoff operation.
- Handoff checks are argv arrays, never shell strings. Executing them requires
  `--run-checks`. Keep ordinary checks distinct from OCI simulation evidence.
- Releases are explicit `v*` tags. Do not tag or update a Homebrew tap merely
  because implementation work passed tests.
