# OCI provider for review-mode.nvim

This optional external Neovim plugin is installed separately from oci-scm. The CLI
works without Neovim or this adapter. It requires review-mode's SCM
provider interface, Neovim 0.11+, the `oscm` executable, and configured OCI CLI
authentication. It has no Lua dependencies beyond Neovim.

Install the adapter explicitly:

```sh
oscm extension install adrianmross/oci-scm --name review-mode \
  --subdir integrations/review-mode.nvim --apply
oscm extension path review-mode
```

Install review-mode.nvim separately. Add the returned directory to Neovim's
runtimepath before starting ReviewMode; installing the extension never enables it:

```lua
local path = vim.fn.system({ "oscm", "extension", "path", "review-mode" })
if vim.v.shell_error == 0 then
  vim.opt.runtimepath:append(vim.trim(path))
end

require("review_mode").setup({
  scm = {
    provider = "github",
    projects = {
      ["/absolute/path/to/oci/project"] = {
        provider = "oci",
        args = { "--context", "my-context", "--auth", "security_token", "-R", "repository-ocid" },
        -- pr = "PR-ocid-or-source-branch",
        -- url = "https://.../pull-requests/PR-ocid", -- browser/copy URL
        base_remote = "origin",
      },
    },
  },
})
```

For lazy.nvim, use the extension installed by oscm:

```lua
{
  "adrianmross/review-mode.nvim",
  init = function()
    local path = vim.fn.system({ "oscm", "extension", "path", "review-mode" })
    if vim.v.shell_error == 0 then
      vim.opt.runtimepath:append(vim.trim(path))
    end
  end,
  opts = { scm = { provider = "oci" } }, -- use project settings above for mixed SCMs
}
```

The previous lazy.nvim repository dependency with an explicit subdirectory
runtimepath remains supported as an alternative to oscm extension management.
No migration is required for existing installations.

Install the CLI separately using oci-scm's install instructions. Plugin loading
does not install an executable. You can set `scm.command` to an absolute binary
path. octx integration is optional; use `--no-context --profile ... --region ...`
instead, or existing `.oci-scm.json` repo defaults. Secrets belong to OCI CLI,
never to provider configuration. Named context selection does not switch globals.

## Behavior

Start `:ReviewMode` in the checkout. The adapter resolves the PR and reads its
source branch's current commit through the OCI refs API, including fork PRs
and branch names containing slashes. Fetch the chosen base remote beforehand.

Existing OCI source-side inline comments and replies appear in the same gutter,
preview, navigation and file picker as GitHub comments. Reply-only objects
inherit their root's path/line. OUTDATED stays outdated, not resolved.
Destination-side and general PR conversation comments have no valid new-file
line anchor and are omitted from gutter threads; inspect them using
`oscm pr comments --json all`.

`:ReviewModeReply` targets the thread root.
`:ReviewModeComment` posts a single-line source comment with the reviewed commit
and explicit `--apply`, then oscm verifies readback and refreshes the UI.
Local HEAD must match the PR head and the reviewed file must have no local edits.
Range comments are rejected, not silently
reanchored. Thread resolve/unresolve and remote viewed sync are unsupported;
viewed state remains local. Browser actions require the configured console URL.
No review approval, PR merge, or deployment is inferred or triggered.

API field semantics:
[Oracle comment model](https://docs.oracle.com/en-us/iaas/tools/python/latest/api/devops/models/oci.devops.models.PullRequestComment.html)
and [create-comment details](https://docs.oracle.com/en-us/iaas/tools/python/latest/api/devops/models/oci.devops.models.CreatePullRequestCommentDetails.html).

## Check

```sh
OSCM_PLUGIN_ROOT="$PWD/integrations/review-mode.nvim" nvim --headless -u NONE -i NONE -l integrations/review-mode.nvim/scripts/check.lua
```

The check uses fake API responses and never contacts OCI.
