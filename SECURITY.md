# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately via
[GitHub security advisories](https://github.com/jskoll/wyrm/security/advisories/new)
rather than public issues.

## Scope note

wyrm config files **execute shell commands by design** (lifecycle hooks and
pane commands). "A config file can run commands" is the documented trust
model — the same as a Makefile — not a vulnerability. Reports about wyrm
executing commands from a config the user chose to run are out of scope;
anything that makes wyrm execute code the *config author* didn't write is
very much in scope.

## Reading a config before running it

`wyrm up -n` (and `wyrm restart -n`, `wyrm kill -n`) print the tmux commands
and the lifecycle hooks a config would run, and execute neither. That is the
supported way to inspect an unfamiliar config.

When cloning untrusted remote repositories, pass `wyrm clone -no-start <repo>`
(or `wyrm clone -n <repo>`) to clone the repository without automatically executing
lifecycle hooks or launching a session, allowing you to review `.wyrm.toml` first.

Before 0.6.0, `-n` printed the hooks' tmux commands but ran `on_project_start`
for real, which defeated the point. If you are on an older build, read the
config itself rather than relying on `-n`.
