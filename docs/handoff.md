# Handoff contract v1

A handoff carries a linear review-fix commit range; merge commits require
explicit reconciliation before export. It contains `manifest.json`, `changes.bundle`, `SHA256SUMS`, and an
optional `pr-description.md`. All referenced files must remain inside its
directory, including through symlinks. Manifest JSON uses strict fields; unknown
schemas/fields fail. SHA-256 covers the manifest, bundle and description.
Checksums detect corruption; they do not authenticate the sender. Use a trusted
transport and verify the sender's commits through your existing signing policy.

`manifest.json` records:

- `schema`: `oci-scm.handoff.v1`.
- `target`: repository OCID, profile/region/auth references, remote and optional
  context/project/compartment. Machine-specific credential paths are omitted.
- `pullRequest`: existing PR OCID; no replacement PR is created during apply.
- `sourceBranch`, `baseBranch`, `expectedStart`, `tip`, ordered `commits`.
- `bundle`, `sha256`, optional `description` filename.
- `replies`, `checks`, `dependencies`, and optional `pushHost` restriction.

Reviewer input (`--replies-file`):

```json
[
  {
    "parent": "EXISTING_COMMENT_ID",
    "body": "Fixed ownership cleanup. Validation evidence follows in the report.",
    "fixCommit": "0123456789abcdef0123456789abcdef01234567"
  }
]
```

`fixCommit` is optional, but when present it must identify an incoming commit.
Replies remain attached to their original thread. The tool does not invent test
results or approval. Review text before sending it, and keep internal material
out of this public source repository.

Check input (`--checks-file`):

```json
[
  {"name":"unit tests","argv":["npm","test"],"evidence":"ordinary"}
]
```

`evidence` is `ordinary` or `oci-simulation`. Supply the actual supported,
documented simulation argv after inspecting its publication behavior. OSCM has
no built-in assumption about ocibuild syntax or CI configuration format. A
label alone establishes no OCI simulation evidence. There is no implicit shell
interpretation: pipelines, redirects and substitutions are not supported.
Checks inherit the receiving process environment and run in its chosen worktree.
Use a trusted wrapper script when mirror, runtime and native-library setup is
required. Never put tokens, private keys or secret-bearing argv in a handoff.

Dependency input (`--dependencies-file`):

```json
[
  {"name":"native library","status":"pending-publication","detail":"Local checksum verified; remote artifact publication awaits permissions."}
]
```

Dependencies are reported independently of tests. They do not prevent starting
PR review, and passing tests never changes their publication status.

Apply preserves completed steps on failure and writes partial evidence. A
failed cherry-pick leaves Git's conflict state for inspection, never an
automatic reset. A successful push followed by a failed API call is reported as
partial; a rerun detects already-present commits and replies. Before retrying an
unknown comment submission outcome, inspect the remote thread.

For divergent local history, inspect the plan and reconcile manually in a
WorkTrunk. Publish a refreshed handoff with commit/reviewer references matching
the reconciled history. Publication steps must run on the selected receiving
host; OSCM never asks an SSH source host to push on its behalf.
