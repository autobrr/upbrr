"""Validate explicitly identified new bug reports without executing issue content."""

import json
import os
import re
import urllib.request

REPOSITORY = "autobrr/upbrr"
BUG_MARKER = "<!-- upbrr-bug-report:v1 -->"
FEATURE_MARKER = "<!-- upbrr-feature-request:v1 -->"
REQUIRED_SECTIONS = (
    "What happened?",
    "Version and environment",
    "Steps to reproduce",
    "Expected behavior",
    "Actual behavior",
)
REQUIRED_FIELDS = (
    "upbrr version and build",
    "Installation method",
    "Operating system and architecture",
)
PLACEHOLDERS = {"", "n/a", "na", "none", "unknown", "todo", "tbd", "...", "…"}


def is_bug(issue):
    """Feature identification wins; ordinary unclassified issues are left alone."""
    title, body = issue.get("title", ""), issue.get("body") or ""
    if FEATURE_MARKER in body or re.match(r"\s*\[feature\]", title, re.I):
        return False
    return BUG_MARKER in body or bool(re.match(r"\s*\[bug\]", title, re.I))


def sections(body):
    """Read headings outside comments/fences; preserve fenced answers and subheadings."""
    body = re.sub(r"<!--.*?(?:-->|$)", "", body, flags=re.S)
    result, current, fence = {}, None, None
    for line in body.splitlines():
        fence_match = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if fence_match:
            marker = fence_match[1]
            if fence is None:
                fence = marker
            elif marker[0] == fence[0] and len(marker) >= len(fence):
                fence = None
            continue
        heading = re.match(r"^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$", line) if fence is None else None
        if heading:
            name = heading[2].casefold()
            if name in {s.casefold() for s in REQUIRED_SECTIONS}:
                current = name
                result.setdefault(current, [])
                continue
            if len(heading[1]) <= 2:
                current = None
        if current is not None:
            result[current].append(line)
    return {name: "\n".join(lines).strip() for name, lines in result.items()}


def meaningful(value):
    """Reject empty Markdown scaffolding and a small explicit placeholder vocabulary."""
    value = re.sub(r"(?m)^\s*(?:[-*+]\s+|\d+[.)]\s*)", "", value)
    value = value.strip(" \t\r\n`*_:#->")
    return value.casefold() not in PLACEHOLDERS and any(c.isalnum() for c in value)


def validate(body):
    """Return fixed, safe explanations of missing structure; never judge the bug's merit."""
    parsed = sections(body)
    errors = []
    for name in REQUIRED_SECTIONS:
        if name.casefold() not in parsed:
            errors.append(f"Missing required heading: {name}.")
    for name in ("What happened?", "Expected behavior", "Actual behavior"):
        if name.casefold() in parsed and not meaningful(parsed[name.casefold()]):
            errors.append(f"Fill in {name}.")
    environment = parsed.get("version and environment", "")
    for name in REQUIRED_FIELDS:
        match = re.search(rf"(?im)^[ \t]*[-*+][ \t]+{re.escape(name)}[ \t]*:[ \t]*([^\n]*)", environment)
        if not match or not meaningful(match[1]):
            errors.append(f"Fill in {name} on its bullet line.")
    steps = parsed.get("steps to reproduce", "")
    if not any(meaningful(step) for step in re.findall(r"(?m)^[ \t]*\d+[.)][ \t]+(.+)$", steps)):
        errors.append("Include at least one filled numbered reproduction step.")
    return errors


def api(method, path, data=None):
    """Send JSON only to the fixed repository API; issue text never chooses a URL."""
    request = urllib.request.Request(
        "https://api.github.com/repos/" + REPOSITORY + path,
        data=json.dumps(data).encode() if data is not None else None,
        headers={
            "Authorization": "Bearer " + os.environ["GITHUB_TOKEN"],
            "Accept": "application/vnd.github+json",
            "Content-Type": "application/json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        method=method,
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def process(event, env, request=api):
    """Close only unchanged, newly opened human bug reports on the first event attempt."""
    issue = event.get("issue", {})
    if issue.get("user", {}).get("id") == 13182387:
        return
    if (
        env.get("GITHUB_EVENT_NAME") != "issues"
        or env.get("GITHUB_REPOSITORY") != REPOSITORY
        or env.get("GITHUB_RUN_ATTEMPT") != "1"
        or event.get("repository", {}).get("full_name") != REPOSITORY
        or env.get("GITHUB_REF") != "refs/heads/" + event.get("repository", {}).get("default_branch", "")
        or event.get("action") != "opened"
        or event.get("sender", {}).get("type") != "User"
        or issue.get("user", {}).get("type") != "User"
        or "pull_request" in issue
        or issue.get("state") != "open"
        or not is_bug(issue)
    ):
        return
    number = issue.get("number")
    if type(number) is not int or number <= 0:
        raise ValueError("Invalid issue number")
    errors = validate(issue.get("body") or "")
    if not errors:
        return
    path = f"/issues/{number}"
    current = request("GET", path)
    # A queued workflow must not close a corrected report or override later triage.
    if any(current.get(key) != issue.get(key) for key in ("title", "body", "updated_at", "state")):
        return
    if current.get("state") != "open" or "pull_request" in current:
        return
    comment = (
        "This bug report is missing required template information:\n\n"
        + "\n".join("- " + error for error in errors)
        + "\n\nPlease edit the report using the [bug report template]"
        "(https://github.com/autobrr/upbrr/blob/main/.github/ISSUE_TEMPLATE/bug_report.md), "
        "then ask a maintainer to reopen it. This is a completeness check, not a judgment "
        "about whether the bug is valid. Corrections and additional evidence are welcome."
    )
    # Fail before closure if tagging or the explanation cannot be published.
    request("POST", path + "/labels", {"labels": ["invalid"]})
    request("POST", path + "/comments", {"body": comment})
    request("PATCH", path, {"state": "closed", "state_reason": "not_planned"})


if __name__ == "__main__":
    with open(os.environ["GITHUB_EVENT_PATH"], encoding="utf-8") as event_file:
        process(json.load(event_file), os.environ)
