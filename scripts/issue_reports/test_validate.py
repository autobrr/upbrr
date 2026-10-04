"""Offline contract tests: no test contacts GitHub or changes a real issue."""

import copy
from pathlib import Path
import unittest

from validate import BUG_MARKER, FEATURE_MARKER, REQUIRED_FIELDS, REQUIRED_SECTIONS, is_bug, process, validate

ROOT = Path(__file__).resolve().parents[2]
BUG_TEMPLATE = (ROOT / ".github/ISSUE_TEMPLATE/bug_report.md").read_text()
FEATURE_TEMPLATE = (ROOT / ".github/ISSUE_TEMPLATE/feature_request.md").read_text()
VALID = f"""{BUG_MARKER}
## Before reporting
- [x] I searched issues.
- [x] I read the docs.
- [x] I checked repository docs for this build.
## What happened?
The description preview loses the first screenshot after saving.
## Version and environment
- upbrr version and build: v0.1.0 / abc1234
- Installation method: Docker, ghcr.io/autobrr/upbrr:pr123
- Operating system and architecture: Linux amd64
## Steps to reproduce
1. Open an existing prepared release and save the description.
## Expected behavior
All selected screenshots remain.
## Actual behavior
The first screenshot disappears.
"""
ENV = {
    "GITHUB_EVENT_NAME": "issues",
    "GITHUB_REPOSITORY": "autobrr/upbrr",
    "GITHUB_REF": "refs/heads/main",
    "GITHUB_RUN_ATTEMPT": "1",
}


def event(body=BUG_TEMPLATE):
    return {
        "action": "opened",
        "repository": {"full_name": "autobrr/upbrr", "default_branch": "main"},
        "sender": {"type": "User"},
        "issue": {
            "number": 123,
            "title": "[Bug]: Preview loses a screenshot",
            "body": body,
            "state": "open",
            "updated_at": "2026-01-01T00:00:00Z",
            "user": {"type": "User"},
        },
    }


class ReportTests(unittest.TestCase):
    def test_valid_minimal_report(self):
        self.assertEqual(validate(VALID), [])

    def test_template_has_required_contract(self):
        for heading in REQUIRED_SECTIONS:
            self.assertIn("## " + heading, BUG_TEMPLATE)
        for field in REQUIRED_FIELDS:
            self.assertIn("- " + field + ":", BUG_TEMPLATE)
        self.assertIn(BUG_MARKER, BUG_TEMPLATE)
        self.assertIn("documentation Markdown files", BUG_TEMPLATE)
        self.assertIn(FEATURE_MARKER, FEATURE_TEMPLATE)
        self.assertIn("title: \"[Bug]: \"", BUG_TEMPLATE)
        self.assertIn("title: \"[Feature]: \"", FEATURE_TEMPLATE)

    def test_empty_template_is_rejected(self):
        errors = validate(BUG_TEMPLATE)
        self.assertEqual(len(errors), 7)
        self.assertTrue(any("numbered" in error for error in errors))
        for field in REQUIRED_FIELDS:
            self.assertTrue(any(field in error for error in errors))

    def test_missing_sections_and_placeholders(self):
        for heading in REQUIRED_SECTIONS:
            with self.subTest(heading=heading):
                self.assertTrue(validate(VALID.replace("## " + heading, "## Removed")))
        for placeholder in ("", "N/A", "TBD", "...", "---", "<!-- instruction -->"):
            with self.subTest(placeholder=placeholder):
                self.assertTrue(validate(VALID.replace("All selected screenshots remain.", placeholder)))

    def test_optional_sections_extra_details_and_checkboxes_are_not_gates(self):
        body = VALID.replace("- [x]", "- [ ]") + "\n## Logs or screenshots (optional)\n\n## Additional context\nMore details."
        body = body.replace("All selected screenshots remain.", "### Example\nAll screenshots remain.\n\nMore detail.")
        self.assertEqual(validate(body), [])
        self.assertEqual(validate(VALID.replace("## Actual behavior", "### ACTUAL BEHAVIOR ###")), [])

    def test_comments_and_fences_cannot_supply_required_headings(self):
        self.assertTrue(validate("<!-- " + VALID.replace(BUG_MARKER, "") + " -->"))
        self.assertTrue(validate("```markdown\n" + VALID + "\n```"))
        self.assertTrue(validate("~~~markdown\n" + VALID + "\n~~~"))
        self.assertEqual(validate(VALID.replace("All selected screenshots remain.", "```text\nKeep all screenshots\n```")), [])

    def test_explicit_classification_and_feature_exemption(self):
        self.assertTrue(is_bug({"title": " [BUG]: failure", "body": "replaced"}))
        self.assertTrue(is_bug({"title": "Failure", "body": BUG_MARKER}))
        self.assertFalse(is_bug({"title": "A suspected bug", "body": "Help"}))
        self.assertFalse(is_bug({"title": "[Bug]: change", "body": FEATURE_TEMPLATE}))
        self.assertFalse(is_bug({"title": "[Feature]: change", "body": BUG_MARKER}))

    def run_event(self, payload, env=None, current=None, fail=None):
        calls = []

        def request(method, path, data=None):
            calls.append((method, path, data))
            if fail == method + " " + path:
                raise RuntimeError("Simulated API failure")
            return copy.deepcopy(current if current is not None else payload["issue"])

        process(payload, env or ENV, request)
        return calls

    def test_new_malformed_bug_is_tagged_explained_and_closed(self):
        calls = self.run_event(event())
        self.assertEqual([(m, p) for m, p, _ in calls], [
            ("GET", "/issues/123"), ("POST", "/issues/123/labels"),
            ("POST", "/issues/123/comments"), ("PATCH", "/issues/123"),
        ])
        self.assertEqual(calls[1][2], {"labels": ["invalid"]})
        self.assertIn("Please edit", calls[2][2]["body"])
        self.assertEqual(calls[3][2], {"state": "closed", "state_reason": "not_planned"})
        self.assertNotIn("body", calls[3][2])

    def test_no_api_calls_for_valid_feature_unclassified_or_old_events(self):
        cases = [event(VALID), event(FEATURE_TEMPLATE)]
        unclassified = event("No template")
        unclassified["issue"]["title"] = "Help please"
        cases.append(unclassified)
        for action in ("edited", "reopened", "labeled", "closed"):
            old = event()
            old["action"] = action
            cases.append(old)
        bot = event()
        bot["sender"]["type"] = "Bot"
        cases.append(bot)
        pr = event()
        pr["issue"]["pull_request"] = {}
        cases.append(pr)
        for payload in cases:
            self.assertEqual(self.run_event(payload), [])
        for key, value in (("GITHUB_RUN_ATTEMPT", "2"), ("GITHUB_REF", "refs/heads/feature"), ("GITHUB_EVENT_NAME", "pull_request"), ("GITHUB_REPOSITORY", "other/repo")):
            self.assertEqual(self.run_event(event(), {**ENV, key: value}), [])

    def test_owner_exemption_uses_original_author_numeric_id(self):
        owner = event()
        owner["issue"]["user"] = {"id": 13182387, "type": "User", "login": "renamed"}
        self.assertEqual(self.run_event(owner), [])
        spoofed = event()
        spoofed["issue"]["user"] = {"id": 999, "type": "User", "login": "Audionut", "name": "Audionut"}
        self.assertEqual(self.run_event(spoofed)[-1][0], "PATCH")
        owner_sender = event()
        owner_sender["sender"] = {"id": 13182387, "type": "User", "login": "Audionut"}
        self.assertEqual(self.run_event(owner_sender)[-1][0], "PATCH")

    def test_queued_report_changes_skip_all_writes(self):
        for key, value in (("body", VALID), ("state", "closed"), ("title", "[Feature]: request"), ("updated_at", "later")):
            payload = event()
            current = {**payload["issue"], key: value}
            self.assertEqual(self.run_event(payload, current=current), [("GET", "/issues/123", None)])

    def test_untrusted_text_never_enters_requests_or_logs(self):
        payload = event(BUG_MARKER + "\n$(touch /tmp/owned)\n${{ secrets.GITHUB_TOKEN }}\n::error::payload\n@everyone")
        calls = self.run_event(payload)
        for method, path, data in calls:
            self.assertTrue(path.startswith("/issues/123"))
            self.assertNotIn("touch", str(data))
            self.assertNotIn("secrets", str(data))
            self.assertNotIn("@everyone", str(data))
        for fail in ("POST /issues/123/labels", "POST /issues/123/comments"):
            with self.assertRaises(RuntimeError):
                self.run_event(event(), fail=fail)

    def test_workflow_is_opened_only_and_has_separate_readonly_pr_tests(self):
        workflow = (ROOT / ".github/workflows/issue-reports.yml").read_text()
        self.assertIn("types: [opened]", workflow)
        self.assertNotIn("pull_request_target", workflow)
        self.assertNotIn("workflow_dispatch", workflow)
        self.assertIn("persist-credentials: false", workflow)
        self.assertIn("ref: ${{ github.sha }}", workflow)
        self.assertNotIn("${{ github.event.issue.body", workflow)
        self.assertNotIn("${{ github.event.issue.title", workflow)


if __name__ == "__main__":
    unittest.main()
