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
