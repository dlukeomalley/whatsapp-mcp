# Engineering this fork

Use the eng-rig skill for first-party changes and independent review at checkpoints.
Installed guidelines/review tools are owned by the revision in
`.pre-commit-config.yaml`; reinstall with `fogline-install` when updating it.
Activate the tracked commit hook after cloning with
`git config core.hooksPath .githooks`.

The first-party fork changes are in the Go bridge. `make check` runs gofmt checks,
go vet and its tests through the declared Go module. The normal commit gate also
runs hygiene/secret checks and required-kit freshness. Tests use temporary stores;
do not start the live bridge or touch authentication/message stores for a check.

The Python MCP and original documentation are upstream-derived. The fleet uses
its own Python MCP; this fork's active consumer is the Go bridge checkout at
`~/git/external/whatsapp-mcp`. Preserve local edits there. First-party changes to
upstream-derived code still require review and an appropriate native gate; they
are not exempt because this is a fork. No Python static gate or full source audit
is claimed by this adoption. Shared language-specific guidance currently covers
Python, Rust and TypeScript; apply its general criteria and the native Go checks
here, with Go-specific review coverage stated explicitly.
