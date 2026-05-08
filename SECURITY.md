# Security Policy

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

To report a vulnerability privately, use [GitHub's private vulnerability reporting](https://github.com/lxnay/matrixos/security/advisories/new) or email the maintainer directly via the contact details on his [GitHub profile](https://github.com/lxnay).

Please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof of concept
- Any relevant logs, screenshots, or code references

We will acknowledge your report as soon as possible and aim to provide a fix or mitigation plan within 30 days, depending on severity.

Please do not disclose the issue publicly until a fix has been released.

## Scope

This policy covers:

- The `vector` CLI tool (this repository)
- The matrixOS build toolchain (`dev/build.sh` and associated scripts)
- Default configuration shipped with matrixOS images

It does **not** cover third-party software included in matrixOS images (Steam, Flatpak, Snap, NVIDIA drivers, etc.). Report vulnerabilities in those upstream to their respective projects.

## Default Credentials

matrixOS images ship with publicly documented default credentials (`matrix`/`matrix`) and a default LUKS passphrase. These are **not** considered vulnerabilities — they are intentional defaults that users are directed to change via `sudo vector setupOS` on first boot. Reports about the defaults themselves will be closed as informational.

## Supported Versions

Only the latest release of matrixOS is actively maintained. If you are running an older image, please update before reporting.

## Preferred Languages

We accept reports in English and Italian.
