# gh-style functionality

The goal is the same everyday SCM workflow as `gh`, adapted to OCI's resources.
Command spelling is familiar; API behavior is defined by
[OCI DevOps](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/devops.html).
This table is the current implementation boundary, not a claim of full parity.

| gh workflow | oscm implementation |
| --- | --- |
| auth login/status/refresh | OCI CLI browser sessions and scoped access validation |
| repo list/view/clone/create/edit/delete/fork/sync | Implemented; sync defaults to merging upstream, with explicit discard opt-in |
| repo set-default | Per-worktree `.oci-scm.json` |
| pr list/view/status | OCID, PR URL, branch, or current upstream; filtered lists |
| pr create/edit | Title/body/base/reviewer principal IDs; structured payloads |
| pr diff | OCI file-diff JSON, not a unified Git patch |
| pr checkout | WorkTrunk; preserves current checkout; forks require an explicit verified `--remote` |
| pr comment | Idempotent comments or threaded replies with `--parent` |
| pr comments | Read every thread; `comments edit/delete PR COMMENT_ID` manage existing comments |
| pr checks | OCI build snapshots; no watch/fail-status exit code yet |
| pr review | Approve or withdraw approval; no GitHub review-event model |
| pr close/reopen/merge | Plan by default; explicit execution and merge strategy |
| run list/view/watch/cancel/rerun | OCI DevOps build runs, with explicit execution for cancel/rerun |
| workflow list/view/run | OCI DevOps build pipelines; execution is separately explicit |
| browse | Opens a supplied HTTPS console/SCM URL; no guessed console URL routes |
| api | OCI-signed regional DevOps JSON API requests |
| completion/version | Bash, Zsh, Fish, PowerShell completion; both executable names |
| handoff | Additional cross-machine bundle/checksum/commit/reply/evidence workflow |
| context/doctor | Optional octx and oidm integration with effective-target inspection |

Remaining OCI equivalents: checks watching and build log
streaming/download through the relevant logging service. These require explicit
fork remotes or logging permissions/configuration. `api` is available for
deliberate API operations meanwhile.

GitHub-specific issues, labels, discussions, gists, Codespaces, GitHub Releases,
Actions secrets and GitHub Projects have no direct OCI SCM mapping here. They
remain owned by the relevant issue tracker, artifact registry or build service.
There is no silent fallback to GitHub and no GitHub token required.

Flag differences: mutations require `--apply`; `--json all` prints the full OCI
response/records, and selected fields accept native OCI names or PR aliases
`title`, `body`, `state`, `headRefName`, `baseRefName`, `createdAt`, `updatedAt`.
There is no jq/template flag or interactive editor wizard yet. Default list
output is compact; detail responses use JSON.
