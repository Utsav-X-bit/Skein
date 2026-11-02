# UX / Plan Review — Skein Desktop GUI (rev. 1)

Date: 2026-11-03
Reviewer: strict UX/accessibility/plan reviewer agent
Verdict: **REJECT** (rev. 1)

## Blocking defects

- **P0-1 First run cannot create/join:** no init/create endpoint or helper
  verb; `/join` needs a running daemon/config; mock helper gate hides it.
- **P0-2 Remove/revoke is not revocation:** removed peers rejoin; no rotation.
- **P0-3 Connect toggle is service start/stop (elevated), contradicting
  unprivileged normal operation; no connection state.**
- **P0-4 Network name has no backend/protocol field.**

## High-priority findings

- Linux service-user vs root-config inconsistency; firewall needs root.
- `SO_PEERCRED` cannot check supplementary groups; group grant needs re-login
  so onboarding dead-ends.
- `PATCH /config` has no allowlist (could move API/token/secret).
- Windows firewall non-transactional and PowerShell interpolation.
- TCP fatal bind is a local DoS.
- Windows release can pass without install/uninstall verification.
- Tray fallback can make the app inaccessible.
- Plan file ownership overlaps; add-peer-from-invite missing; contrast fails
  AA; “Reconnecting”/health undefined; per-edge RTT fabricated; invite
  snapshot semantics; public key API missing; firewall status/rules contract
  missing; rotation missing; fake controls (DNS, log level, channel, language,
  ACL, share, config folder); telemetry opt-in/retention missing; probe result
  undefined; `ConfigView` missing STUN/public; secret handling contradiction;
  Windows multi-user policy undefined.
- Plan gates are mock-heavy and do not prove clean-machine create/join,
  migration, revocation, firewall rollback, SSE fan-out, or Windows runtime.

## Required changes

Resolve P0-1..P0-4 and P1-1..P1-17; rework gates to test clean-machine Linux
and Windows installs, real helper/service transitions, revocation, and
accessibility budgets. Full details in the review transcript.
