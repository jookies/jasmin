# Approved compatibility deviations

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.

No deviations are approved.

## Required record format

Every future deviation must include:

- ID and affected compatibility matrix rows;
- legacy behavior and evidence;
- new behavior and reason;
- security/operational impact;
- migration and rollback instructions;
- owner approval date;
- differential fixture/test proving the boundary.

Security hardening is delivered through an explicit `secure` profile until the owner approves changing compatibility defaults.
