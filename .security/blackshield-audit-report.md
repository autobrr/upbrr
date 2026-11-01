# BlackShield Security Audit Report — `autobrr/upbrr`

- **Scanned Commit:** `eb47619dabb2bdb9532a65600da67bb8898dc2c6`
- **Actionable Findings:** `2` (`0 Critical`, `2 High`, `63 Medium`, `0 Low`)
- **BlackShield Platform & Continuous Zero-Cost PR Protection:** [https://blackshield.chaplau.com](https://blackshield.chaplau.com)

## Detailed Findings & Remediation Guide

### 1. [HIGH] `CVE-2022-25883` — CVE-2022-25883 in semver@6.3.1

- **Category:** `vulnerability`
- **Location:** `documentation/pnpm-lock.yaml`
- **Affected Package:** `semver@6.3.1`
- **Details:** Regular Expression Denial of Service (ReDoS) in semver range parser.
- **Recommended Fix:** Upgrade `semver` to `7.5.2` or newer

### 2. [HIGH] `CVE-2022-25883` — CVE-2022-25883 in semver@6.3.1

- **Category:** `vulnerability`
- **Location:** `webui/pnpm-lock.yaml`
- **Affected Package:** `semver@6.3.1`
- **Details:** Regular Expression Denial of Service (ReDoS) in semver range parser.
- **Recommended Fix:** Upgrade `semver` to `7.5.2` or newer

