# Security Policy

## Supported versions

environments-bots is pre-1.0: only the latest release receives security
fixes.

## Reporting a vulnerability

Report it privately through GitHub's "Report a vulnerability" on this
repository's Security tab, not in a public issue. Include the affected tool
and version, what an attacker can do, and how to reproduce it.

## Scope

The tools act on GitHub with the token the calling workflow passes in. A
finding that lets untrusted input (a pull request, a test name, test output)
make a tool act beyond the issues and project items it manages is in scope.
