# Security Policy

## Reporting a vulnerability

Please do not open public issues for suspected vulnerabilities. Use GitHub Private Vulnerability Reporting instead:

1. Open the repository's **Security** tab.
2. Select **Report a vulnerability**.
3. Include the details listed below and submit the private report.

If GitHub does not show the private reporting form, contact the repository owner privately and do not publish exploit details until the issue is resolved.

## What to include

Please include as much of the following as you can:

- Affected `ajq` version, commit, or release tag.
- Operating system and installation method.
- Reproduction steps or a minimal proof of concept.
- Expected and observed impact, including whether secrets, local files, model prompts, or generated output are exposed or modified.
- Any mitigations or patches you have already identified.

## Response expectations

This is a small project, but maintainers will make a best effort to acknowledge private vulnerability reports within 7 days. After acknowledgment, we will coordinate on validation, fix planning, release timing, and public disclosure notes through the private advisory thread.

## Supported versions

Security fixes target the latest released line of `ajq`. Please verify against the latest release when possible; older pre-release builds and unreleased commits may not receive separate patches.

## Scope notes

- The managed local daemon is designed to bind loopback-only (`127.0.0.1`, `localhost`, or `[::1]`) and rejects non-loopback hosts.
- Provider API keys are environment-only by design. Configuration files reject credential-looking keys; see `internal/config/config.go` and the credential-key rejection path.
- Reports about unsafe defaults, credential handling, local daemon exposure, release provenance, CI supply-chain risk, or dependency vulnerabilities are in scope.
- General support questions, public feature requests, and non-sensitive bugs should use the normal issue tracker instead.
