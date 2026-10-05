# Optional extensions

The core CLI never requires or loads extensions. Management commands do not
resolve OCI profiles, contexts or repositories. Extension sources may be
`OWNER/REPO` on GitHub, an HTTPS Git repository URL, or an explicit local directory (`.`, `./path`, `../path`, or an absolute path).
`OWNER/REPO` always selects the remote repository, even when that relative
directory exists locally.
Private remote repositories use your existing Git authentication.

## Command extensions

A command extension contains an executable named `oscm-NAME` in its root.
For `OWNER/oscm-example`, the default name is `example`. Use `--name` to override
it, and `--subdir` to select a package inside a repository.

```sh
oscm extension install ./oscm-example --apply
oscm extension exec example -- --flag value
```

`exec` invokes the executable directly, without a shell or argument interpolation.
Arguments and streams are passed through. It is an explicit request to execute
external code; unlike install/upgrade/remove it does not require `--apply`.
Extensions receive the normal process environment and run in `--directory`.
OCI target flags are not silently copied to an extension's own commands.

## Neovim extensions

A Neovim package contains a `lua/` directory and `oscm-extension.json`:

```json
{"schema":"oci-scm.extension.v1","kind":"neovim"}
```

The selected package directory is its Neovim runtimepath. `extension path NAME`
prints that path; `extension list --json all` also includes its `path` and `kind`.
`extension exec` rejects Neovim packages. Install the editor/UI separately and
enable the runtimepath in your own editor configuration.

The bundled source package at `integrations/review-mode.nvim` is the OCI adapter,
not review-mode's UI. It is excluded from CLI binary archives and installs only
when explicitly selected as an extension. The UI and adapter can therefore be
omitted independently of the CLI.

## Storage and updates

Extensions live in `$XDG_DATA_HOME/oci-scm/extensions`, or
`~/.local/share/oci-scm/extensions` when unset. `OSCM_EXTENSION_DIR` overrides the
storage location. Each installation has an `extension.json` record. Remote Git
checkouts live under its `repo/` directory; local installations record the source
path and do not copy or delete the source.

`--pin TAG_OR_COMMIT` detaches a remote checkout at that revision. Without a pin,
the default branch tracks its remote. Upgrade refuses local changes and divergent
history, never force-pushes, and does not run hooks supplied by an extension.
An invalid package fails installation and the incomplete installation is removed.

Names cannot contain path separators, and `--subdir` cannot escape the repository
through `..` or a directory symlink. HTTPS URLs cannot contain embedded credentials.
Selected Git sources are downloaded only after `--apply`.

Differences from gh: no binary-release discovery, search/browse/create scaffolding,
`upgrade --all`, or automatic `oscm NAME` dispatch yet. Use `extension exec NAME`.
Installation uses Git; it does not fetch arbitrary release executables. Normal
SCM commands and this extension manager remain usable without GitHub CLI or an
account on GitHub.
