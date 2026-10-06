# oci-scm · oscm

OCI DevOps source control from the terminal, with the command vocabulary of
[GitHub CLI](https://cli.github.com/manual/gh). `oscm` and `oci-scm` are the same
program. Standalone Go CLI, MIT licensed; no GitHub account or daemon required.

Manage repositories and pull requests, keep reviewer replies attached to their
threads, and carry review fixes between machines with verified Git bundles.

## Install

Requires Git and [OCI CLI](https://docs.oracle.com/en-us/iaas/Content/API/SDKDocs/cliinstall.htm).
[WorkTrunk](https://worktrunk.dev/) is needed only for `pr checkout`.
`octx`/`oci-context` and `oidm`/`oci-idm` are optional.

```sh
git clone https://github.com/adrianmross/oci-scm.git
cd oci-scm
make install
oscm version
```

Go 1.25.6 or newer is needed to build. Tagged releases produce both executable
names for macOS, Linux and Windows, amd64 and arm64, with archive checksums.
Stable releases are available on [GitHub](https://github.com/adrianmross/oci-scm/releases).
Install the CLI with `brew install adrianmross/tap/oci-scm`.
Neovim and review-mode are not CLI dependencies; editor integration is opt-in.

## Choose your context

Use explicit settings without any companion CLI:

```sh
oscm context --no-context --profile DEV --region us-phoenix-1
oscm repo list --no-context --profile DEV --region us-phoenix-1 \
  --project-id ocid1.devopsproject.oc1.example
```

Or select a named context without changing the globally selected context:

```sh
oscm context --context dev
oscm repo list --context dev --project-id ocid1.devopsproject.oc1.example
oscm repo set-default ocid1.devopsrepository.oc1.example --context dev --apply
oscm doctor
oscm pr list
```

`repo set-default` writes `.oci-scm.json` in the selected worktree. It contains
references and identifiers, never credentials. Make that local config private
when the identifiers should not be shared. Explicit context selection changes
profile/region defaults, not the selected repository; use `-R` for another repo.

Precedence: individual flags → environment → explicitly named context → repo
config → current optional context. The environment supports `OCI_CLI_PROFILE`,
`OCI_CLI_REGION`/`OCI_REGION`, `OCI_CLI_AUTH`, `OCI_CLI_CONFIG_FILE`,
`OCI_COMPARTMENT_OCID`, `OSCM_CONTEXT`, `OSCM_REPOSITORY`, and `OSCM_PROJECT`.

Context executable discovery: `octx`, `oci-context`, then `ocix`.
`--context-bin` and `--context-config` override discovery/configuration.
`--no-context` disables integration; an explicitly requested missing context
fails rather than falling back to another tenancy.

```sh
oscm context --identity --context dev
```

This additionally reads `oidm`/`oci-idm` defaults, passing the effective OCI
profile/region to it. Identity service/issuer metadata is advisory. IDCS bearer
tokens are not OCI DevOps SCM credentials. OSCM never requests or prints them.
Old `ocix` and `ocidm` commands remain supported by discovery.

## Pull requests

```sh
oscm pr list --state open
oscm pr view feature-branch
oscm pr view ocid1.devopspullrequest.oc1.example --json id,title,state
oscm pr diff feature-branch
oscm pr comments feature-branch --json all
oscm pr checks feature-branch --json all
oscm pr checkout feature-branch --apply
```

PR selectors accept an OCID, HTTPS PR URL ending in an OCID, or an open source
branch. Omitting a selector uses the current branch's upstream, then its local
name. OCI OCIDs replace GitHub's PR numbers. A branch resolving to multiple open
PRs fails; choose an OCID. Fork checkout requires explicit remote setup and
`oscm pr checkout PR_OCID --remote fork --apply`; its URL is verified against
the source repository before fetching. Fork worktree branches include the
remote name to avoid colliding with same-repository worktrees.

```sh
oscm pr create --title "Fix key cleanup" --head feature-branch --base main \
  --body-file description.md
oscm pr create --title "Fix key cleanup" --head feature-branch --base main \
  --body-file description.md --apply
oscm pr edit feature-branch --body-file description.md --apply
oscm pr comment feature-branch --parent COMMENT_ID --body-file reply.md --apply
```

Mutations show a plan unless `--apply` is supplied. Creating a PR checks for an
existing open PR on the source/base. Replies check the parent, skip an existing
active/non-deleted reply with the same parent/body, and verify readback. Source
updates may make line comments outdated; OSCM does not resolve threads or infer
reviewer approval. Exact-body deduplication is sequential, not an atomic service
guarantee: concurrent writers can still race. Inspect unknown outcomes before
retrying; OSCM never blindly retries a mutation.

```sh
oscm pr review feature-branch --approve            # plan
oscm pr review feature-branch --unapprove --apply
oscm pr close feature-branch                      # plan
oscm pr reopen feature-branch --apply
oscm pr merge feature-branch --squash              # plan only
```

Merge requires an explicit strategy and `--apply`. Source branches are retained
unless `--delete-branch` is explicitly supplied. Async operations report submission
and a readback, not assumed completion. `pr checks` reads OCI snapshots; it does
not run checks or claim missing CI is passing.

## Optional extensions

`oscm` works without any extensions. Install command extensions or editor plugins
separately with gh-style commands; no OCI authentication is needed to manage them.

```sh
oscm extension install OWNER/oscm-example --pin v1.0.0 --apply
oscm extension list --json all
oscm extension exec example -- --help
oscm extension upgrade example --apply
oscm extension remove example --apply
```

Install, upgrade and remove show a plan without `--apply`. Explicit `exec` runs
the installed command with your arguments, stdin/stdout/stderr and exit status.
Installation never runs an extension or its install scripts. Local directories
are linked by reference for development; removal keeps the source files.
Pinned and local extensions are not upgraded; unpinned remote extensions upgrade
with a clean-checkout, fast-forward Git pull. See [extension format](docs/extensions.md).

The [OCI adapter for review-mode.nvim](integrations/review-mode.nvim) is an
optional Neovim extension. Install it only if you want that editor integration:

```sh
oscm extension install adrianmross/oci-scm \
  --name review-mode --subdir integrations/review-mode.nvim --apply
oscm extension path review-mode
```

Add the returned path to Neovim's runtimepath and install review-mode.nvim
separately. Installing this extension does not install Neovim, install the review
UI, enable it, or change your editor configuration. Provider selection and OCI
context arguments remain configurable per project. CLI release archives exclude
the adapter; Git installs select it explicitly using `--subdir`.

## Cross-machine handoffs

On the source machine, with committed changes and a clean worktree:

```sh
oscm handoff create ../handoffs/review \
  --since EXISTING_REVIEWED_COMMIT --source feature-branch --base main \
  --pr ocid1.devopspullrequest.oc1.example \
  --body-file description.md --replies-file replies.json \
  --checks-file checks.json --dependencies-file dependencies.json \
  --push-host laptop-hostname
```

Copy the directory using your own SSH/SCP configuration. On the receiving
machine, in an isolated WorkTrunk containing the reviewed starting commit:

```sh
oscm handoff verify ../handoffs/review
oscm handoff plan ../handoffs/review
oscm handoff apply ../handoffs/review              # plan
oscm handoff apply ../handoffs/review --apply --run-checks --push --update-pr
```

Checks are visible argv arrays and run only with `--run-checks`. Review them
before execution: checks can execute programs and publication steps are not
automatically safe. Use your documented local simulation with publication,
notifications and deployment disabled. See [handoff format](docs/handoff.md).

The plan compares commits and patch IDs in a disposable object database. It
does not update your working repository's refs. Apply refuses a dirty worktree,
verifies the selected OCI target and Git remote, preserves commit IDs when a
fast-forward is possible, and never force-pushes. Divergence requires explicit
reconciliation. Equivalent cherry-picked fixes are detected, but publication
requires refreshed reviewer commit references. Push and PR updates require
at least one recorded check, executed successfully during this run.

Each execution saves commands, versions, checks, dependency statuses, PR
readbacks and partial failures in `runs/<UTC timestamp>/`. Ordinary checks and
OCI simulation have separate evidence labels. Labels describe the command's
intent; they are not independent attestation of simulation behavior. Pending
artifact publication remains pending even if a local library passes tests.

## More commands

```sh
oscm repo view
oscm repo edit --description "Updated description" # plan
oscm repo fork ocid1.devopsrepository.oc1.example --name fork \
  --project-id ocid1.devopsproject.oc1.example       # plan
oscm repo sync --source main --branch main          # plan, merges upstream
oscm pr comments edit feature-branch COMMENT_ID --body-file reply.md # plan
oscm repo clone ocid1.devopsrepository.oc1.example --apply
oscm repo create example --project-id ocid1.devopsproject.oc1.example # plan
oscm repo create fork --fork-of ocid1.devopsrepository.oc1.example \
  --project-id ocid1.devopsproject.oc1.example                        # plan
oscm run list
oscm run view ocid1.devopsbuildrun.oc1.example
oscm run watch ocid1.devopsbuildrun.oc1.example --exit-status
oscm run cancel ocid1.devopsbuildrun.oc1.example      # plan
oscm run rerun ocid1.devopsbuildrun.oc1.example       # plan
oscm workflow list
oscm workflow run ocid1.devopsbuildpipeline.oc1.example # plan
oscm browse https://scm.example.com/my-pull-request
oscm api /20210630/repositories/ocid1.devopsrepository.oc1.example
oscm auth status
oscm auth login --profile DEV --region us-phoenix-1 --apply
oscm auth refresh --profile DEV --apply
oscm completion zsh
```

`api` signs requests only for the resolved region's standard OCI DevOps endpoint.
It supports JSON bodies via `--input`, and requires `--apply` for non-GET methods.
Use typed commands for private SCM deployments with different endpoints.
Authentication, SSH agents and security-token renewal remain owned by OCI CLI
and your existing SSH configuration.

This is the initial implementation of gh-style SCM functionality, not complete
flag-for-flag gh compatibility. [The command matrix](docs/compatibility.md)
records implemented behavior, remaining OCI equivalents and GitHub-only gaps.

## Development

```sh
make check
make build
```

Tests use fake OCI responses and temporary real Git repositories. They do not
need credentials or mutate a cloud resource. See [contributing](CONTRIBUTING.md).

### Draft pull requests

```sh
oscm pr create --draft --title "Work in progress" --head feature --base main
oscm pr create --draft --title "Work in progress" --head feature --base main --apply
oscm pr view feature --json title,reviewStatus,isDraft
oscm pr ready feature                 # preview the transition
oscm pr ready feature --apply         # mark ready for review
oscm pr ready feature --undo --apply  # convert back to draft
```

OCI SCM returns native `reviewStatus` values `DRAFT` and `READY` that older OCI CLI SDK models omit. Draft creation, status reads, and readiness updates use signed `oci raw-request` calls against the regional DevOps API. Updates require an ETag and verify the requested status by reading it back; a submitted or ignored update is never reported as verified. Already matching states produce no write. Draft creation never silently converts an existing ready PR.

Full PR view/status JSON retains the CLI's existing kebab-case fields and adds `review-status` and the gh-style `isDraft` boolean. `--json reviewStatus` selects the native status. Unknown status values are preserved without inventing a draft boolean. Draft status alone does not guarantee notification or build suppression.
### Optional issue providers

The base CLI includes only an issue-provider loader and a JSON contract. Tracker adapters are separate extensions; installing one does not authenticate, fetch issues, or activate it for a project.

```sh
oscm extension install adrianmross/issue-providers --name jira --subdir packages/jira --apply
oscm extension install adrianmross/issue-providers --name github --subdir packages/github --apply
oscm extension install adrianmross/issue-providers --name linear --subdir packages/linear --apply
```

Select the provider in the owning repository's `.oci-scm.json`:

```json
{
  "schema": "oci-scm.repo.v1",
  "issues": { "provider": "jira", "options": { "target": "jira-oci" } }
}
```

Keep existing SCM settings when adding `issues`. GitHub uses `options.repo` (`OWNER/REPO`); Linear optionally uses `options.team`. The providers package documents dependencies and environment-based authentication. Do not put credentials in repository settings.

```sh
oscm issue view EX-123
oscm issue view EX-123 --offline
oscm issue view EX-123 --refresh
oscm issue search 'project = EX AND status = Open'
oscm issue view EX-123 --json key,title,status
```

Issue commands never resolve OCI authentication. `--provider` overrides the selected extension and clears options from a different provider. Responses expose `cache.source`, `cache.fetchedAt` (Unix seconds), and `cache.stale`. Search semantics belong to each adapter: Jira JQL, GitHub search, and Linear title text. Read-only providers cannot implicitly transition or create issues.

The extension manifest uses `schema: oci-scm.extension.v1`, `kind: issue-provider`, and an executable relative path in `executable`. Its executable receives `--request JSON` with schema `issue-provider.request.v1`, operation `get` or `search`, `key` or `query`, provider `options`, and `offline`/`refresh` flags. It returns one JSON object with schema `issue-provider.response.v1`, an `issue` or `items`, and optional cache metadata. Canonical issue fields are `id`, `key`, `title`, `body`, `url`, `status`, `assignee`, `labels`, and `updatedAt`. Plugin code executes only after an explicit issue command.

The same executables work with the optional [issues.nvim](https://github.com/adrianmross/issues.nvim) plugin or any consumer of this contract. review-mode delegates issue context to that plugin; neither host owns tracker authentication. An adapter installed through oscm can be discovered by issues.nvim without a duplicate installation.
