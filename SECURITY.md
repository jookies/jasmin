# Security policy

Synevyr Messaging Platform is proprietary software operated by Chernomor. It is
not currently distributed publicly, so there are no released versions with
independent support windows.

## Reporting a vulnerability

Report suspected vulnerabilities privately to the repository owner. Do not open a
public issue, and do not include working exploit detail in the first message.

> **TODO before any public release:** replace this with a real security contact
> address and a disclosure timeline. Until then this file exists only to prevent
> a report being sent to the wrong party.

This file previously carried the upstream Jasmin project's policy and directed
reports to support@jasminsms.com, which is an unrelated third party. That was an
artefact of this repository's fork history, and would have leaked vulnerability
detail about this product to people with no relationship to it.

## What is most worth attacking

Stated plainly, so a reviewer knows where to look first:

- **The SMPP PDU decoder** (`internal/transport/smppwire`) is the only code
  reachable by an unauthenticated network peer. It is fuzzed
  (`FuzzDecodeNeverPanics`).
- **The admin REST API** creates users, changes balances and starts connectors.
  It should be on its own loopback listener via `admin.api_listen_address`; see
  docs/operations/security.md.
- **Interceptor scripts** are arbitrary Python running as the gateway user, so
  `admin.allow_interceptor_editing` is effectively remote code execution when
  enabled. It defaults to false.
- **Bind credentials** are limited to 8 characters by SMPP 3.4, so the SMPP
  listener must be reachable only by known peers rather than protected by
  password strength alone.
