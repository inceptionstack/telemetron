# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities to `security@inceptionstack.io`.

- Acknowledgement target: within 5 business days
- Bug bounty: none
- Disclosure model: coordinated disclosure preferred

Do not open public GitHub issues for unpatched vulnerabilities involving token exposure, telemetry content leakage, or other sensitive security defects.

## Scope

The main threat model for `loki-otl` centers on:

- bearer token theft
- accidental leakage of prompt, response, or tool content
- exporter misconfiguration that weakens transport security

Reports that materially affect those areas are especially valuable.

## Expectations

- Provide reproduction steps, affected versions, and impact if known.
- If possible, include whether the issue requires local access, privileged access, or network access.
- We may ask for a private retest before public disclosure.
