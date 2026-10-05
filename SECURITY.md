# Security

Please report security issues privately through GitHub's security advisory
workflow when enabled, rather than including credentials or internal handoffs
in a public issue.

OSCM delegates signing and authentication to OCI CLI and Git/SSH. It does not
read credential files or implement a token store. Context metadata and PR
content can still be sensitive: keep `.oci-scm.json`, handoffs and run evidence
private where appropriate. Checksums establish integrity, not sender identity.

Handoff checks are executable input. Review their argv and referenced scripts
before enabling `--run-checks`. Plans do not execute checks or mutate cloud
resources. Explicit `--apply` grants execution of the selected operation.
