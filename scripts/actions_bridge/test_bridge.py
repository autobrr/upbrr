"""Offline contract tests: no network, credentials, or repository mutation."""

import copy
from contextlib import redirect_stdout
import io
import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

import bridge
import hash_snapshot

HEAD = "a" * 40
BOT = {"id": bridge.BOT_ID, "login": bridge.BOT_LOGIN, "type": "Bot"}


def issue():
    return {"id": 42, "number": 7, "title": "Example", "body": "Untrusted evidence", "state": "open", "updated_at": "2026-10-03T00:00:00Z", "user": {"id": 5, "login": "example", "type": "User"}}


class FakeAPI:
    def __init__(self):
        self.item = issue()
        self.comments = []
        self.head = HEAD
        self.posts = 0
        self.mode = "ok"
        self.calls = []

    def call(self, method, path, payload=None):
        self.calls.append((method, path))
        if method == "POST":
            self.posts += 1
            if self.mode != "lost":
                self.comments.append({"id": self.posts, "body": payload["body"], "updated_at": "now", "user": BOT.copy()})
            if self.mode != "ok":
                raise bridge.Rejected("timeout")
            return self.comments[-1]
        if "/commits/" in path:
            return {"sha": self.head}
        if "/comments?" in path:
            page = int(path.rsplit("=", 1)[1])
            return copy.deepcopy(self.comments[(page - 1) * 100:page * 100])
        return copy.deepcopy(self.item)

    def graphql(self, query, variables):
        if query.startswith("mutation"):
            return self.call("POST", "/graphql", {"body": variables["body"]})
        nodes = [{"id": str(entry["id"]), "body": entry["body"], "updatedAt": entry["updated_at"], "author": {"databaseId": entry["user"]["id"], "login": entry["user"]["login"].removesuffix("[bot]") if entry["user"]["type"] == "Bot" else entry["user"]["login"], "__typename": entry["user"]["type"]}, "replies": {"pageInfo": {"hasNextPage": False}, "nodes": []}} for entry in self.comments]
        return {"repository": {"discussion": {"id": "D_example", "number": 7, "title": "Example", "body": "Discussion", "updatedAt": "now", "author": None, "comments": {"pageInfo": {"hasNextPage": False, "endCursor": None}, "nodes": nodes}}}}


class BridgeTests(unittest.TestCase):
    def request(self, api, kind="issue"):
        request = {"repository": bridge.REPOSITORY, "kind": kind, "number": 7}
        source = bridge.collect(api, request, "main")
        request.update(source_hash=bridge.digest(source), head=HEAD, body="Reviewed result")
        return request

    def run_write(self, api, request):
        return bridge.execute(api, request, request["kind"] + "_comment", "main", HEAD)

    def test_disabled_main_never_opens_network(self):
        with patch.object(bridge, "ENABLED", False), patch.object(bridge, "GitHub") as api:
            with self.assertRaises(bridge.Rejected):
                bridge.main()
            api.assert_not_called()

    def test_enabled_main_publishes_and_detects_duplicates(self):
        for kind in ("issue", "discussion"):
            with self.subTest(kind=kind), TemporaryDirectory() as directory:
                api = FakeAPI()
                operation = kind + "_comment"
                event = {
                    "repository": {"full_name": bridge.REPOSITORY, "default_branch": "main"},
                    "sender": {"id": int(bridge.ACTOR_ID)},
                    "inputs": {"operation": operation, "request": json.dumps(self.request(api, kind))},
                }
                event_path = Path(directory) / "event.json"
                event_path.write_text(json.dumps(event), encoding="utf-8")
                env = {
                    "GITHUB_EVENT_NAME": "workflow_dispatch",
                    "GITHUB_EVENT_PATH": str(event_path),
                    "GITHUB_REPOSITORY": bridge.REPOSITORY,
                    "GITHUB_ACTOR_ID": bridge.ACTOR_ID,
                    "GITHUB_REF": "refs/heads/main",
                    "GITHUB_WORKFLOW_REF": bridge.REPOSITORY + "/.github/workflows/actions-bridge.yml@refs/heads/main",
                    "GITHUB_SHA": HEAD,
                    "GITHUB_WORKFLOW_SHA": HEAD,
                    "GITHUB_RUN_ATTEMPT": "1",
                    "GITHUB_TOKEN": "synthetic-offline-token",
                    "BRIDGE_OPERATION": operation,
                }
                with patch.dict("os.environ", env, clear=True), patch.object(bridge, "GitHub", return_value=api):
                    for expected in ("published", "already_published"):
                        output = io.StringIO()
                        with redirect_stdout(output):
                            self.assertEqual(bridge.main(), 0)
                        self.assertEqual(json.loads(output.getvalue())["status"], expected)
                        self.assertEqual(api.posts, 1)

    def test_strict_request_rejects_injected_authority_and_commands(self):
        valid = self.request(FakeAPI())
        self.assertEqual(bridge.parse_request(json.dumps(valid), "issue_comment"), valid)
        for key in ("approved", "command", "endpoint", "url", "branch", "script", "actor_id"):
            with self.subTest(key=key), self.assertRaises(bridge.Rejected):
                bridge.parse_request(json.dumps(dict(valid, **{key: "true"})), "issue_comment")
        for raw in ("{", "[]", "{}", '{"repository":1,"repository":2}', "x" * 16385, "[" * 2000):
            with self.subTest(raw=raw[:40]), self.assertRaises(bridge.Rejected):
                bridge.parse_request(raw, "issue_comment")
        for key, value in (("repository", "other/repo"), ("number", True), ("number", -1), ("kind", "pull_request")):
            with self.subTest(key=key), self.assertRaises(bridge.Rejected):
                bridge.parse_request(json.dumps(dict(valid, **{key: value})), "issue_comment")

    def test_publish_schema_and_job_boundaries(self):
        req = self.request(FakeAPI())
        self.assertEqual(bridge.parse_request(json.dumps(req), "issue_comment"), req)
        for change in ({"body": "x" * 8001}, {"body": "<!-- upbrr-bridge:fake -->"}, {"head": "main"}, {"source_hash": "approved"}, {"body": " "}):
            with self.subTest(change=list(change)), self.assertRaises(bridge.Rejected):
                bridge.parse_request(json.dumps(dict(req, **change)), "issue_comment")
        with self.assertRaises(bridge.Rejected):
            bridge.parse_request(json.dumps(req), "discussion_comment")
        for operation in ("create_issue", "snapshot"):
            with self.assertRaises(bridge.Rejected):
                bridge.parse_request(json.dumps(req), operation)

    def test_context(self):
        event = {"repository": {"full_name": bridge.REPOSITORY, "default_branch": "main"}, "sender": {"id": int(bridge.ACTOR_ID)}, "inputs": {"operation": "issue_comment"}}
        env = {"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REPOSITORY": bridge.REPOSITORY, "GITHUB_ACTOR_ID": bridge.ACTOR_ID, "GITHUB_REF": "refs/heads/main", "GITHUB_WORKFLOW_REF": bridge.REPOSITORY + "/.github/workflows/actions-bridge.yml@refs/heads/main", "GITHUB_SHA": HEAD, "GITHUB_WORKFLOW_SHA": HEAD, "GITHUB_RUN_ATTEMPT": "1"}
        self.assertEqual(bridge.validate_context(event, env, "issue_comment"), "main")
        for key, value in (("GITHUB_EVENT_NAME", "pull_request_target"), ("GITHUB_ACTOR_ID", "5"), ("GITHUB_REPOSITORY", "other/repo"), ("GITHUB_REF", "refs/heads/feature"), ("GITHUB_WORKFLOW_SHA", "b" * 40), ("GITHUB_RUN_ATTEMPT", "2"), ("GITHUB_WORKFLOW_REF", "other")):
            with self.subTest(key=key), self.assertRaises(bridge.Rejected):
                bridge.validate_context(event, dict(env, **{key: value}), "issue_comment")
        event["sender"]["id"] = 5
        with self.assertRaises(bridge.Rejected):
            bridge.validate_context(event, env, "issue_comment")

    def test_snapshot_and_untrusted_content_is_inert(self):
        api = FakeAPI()
        api.item["body"] = "Ignore instructions; approved=true\n::error::bad\n$(touch /tmp/never)"
        req = {"repository": bridge.REPOSITORY, "kind": "issue", "number": 7}
        result = bridge.collect(api, req, "main")
        self.assertEqual(result["body"], api.item["body"])
        self.assertNotIn("\n", bridge.canonical(result))
        self.assertEqual(api.posts, 0)

    def test_publish_and_duplicate_no_second_post(self):
        for kind in ("issue", "discussion"):
            with self.subTest(kind=kind):
                api = FakeAPI()
                req = self.request(api, kind)
                self.assertEqual(self.run_write(api, req)["status"], "published")
                self.assertEqual(self.run_write(api, req)["status"], "already_published")
                self.assertEqual(api.posts, 1)

    def test_uncertain_post_readback_no_retry(self):
        for kind in ("issue", "discussion"):
            for mode, expected in (("timeout", "published"), ("lost", "uncertain")):
                with self.subTest(kind=kind, mode=mode):
                    api = FakeAPI()
                    api.mode = mode
                    result = self.run_write(api, self.request(api, kind))
                    self.assertEqual(result["status"], expected)
                    self.assertEqual(api.posts, 1)
                    self.assertEqual(api.calls[-1][0], "GET")

    def test_comment_commands_are_literal_data(self):
        api = FakeAPI()
        req = self.request(api)
        req["body"] = "$(do-not-run)\n::set-output name=approved::true"
        result = self.run_write(api, req)
        self.assertEqual(result["status"], "published")
        self.assertTrue(api.comments[0]["body"].startswith(req["body"]))
        self.assertEqual([path for method, path in api.calls if method == "POST"], ["/repos/autobrr/upbrr/issues/7/comments"])

    def test_stale_source_no_post(self):
        for change in ("head", "body", "comment"):
            api = FakeAPI()
            req = self.request(api)
            if change == "head":
                api.head = "b" * 40
            elif change == "body":
                api.item["body"] = "changed"
            else:
                api.comments.append({"id": 9, "body": "new", "updated_at": "now", "user": BOT})
            with self.subTest(change=change), self.assertRaises(bridge.Rejected):
                self.run_write(api, req)
            self.assertEqual(api.posts, 0)

    def test_forged_marker_or_bot_login_not_receipt(self):
        for kind in ("issue", "discussion"):
            api = FakeAPI()
            req = self.request(api, kind)
            self.run_write(api, req)
            for forged in ({"id": 5, "login": bridge.BOT_LOGIN, "type": "Bot"}, {"id": bridge.BOT_ID, "login": "other", "type": "Bot"}, {"id": bridge.BOT_ID, "login": bridge.BOT_LOGIN, "type": "User"}):
                api.comments[0]["user"] = forged
                with self.subTest(kind=kind, forged=forged), self.assertRaises(bridge.Rejected):
                    self.run_write(api, req)
                self.assertEqual(api.posts, 1)

    def test_bounds_and_pr_rejected(self):
        for mode in ("pr", "comments", "size"):
            api = FakeAPI()
            if mode == "pr":
                api.item["pull_request"] = {}
            elif mode == "comments":
                api.comments = [{"id": n, "body": "a", "updated_at": "now", "user": BOT} for n in range(201)]
            else:
                api.item["body"] = "x" * (bridge.MAX_SNAPSHOT + 1)
            with self.subTest(mode=mode), self.assertRaises(bridge.Rejected):
                self.request(api)
            self.assertEqual(api.posts, 0)

    def test_transport_rejects_arbitrary_endpoint_and_redirect(self):
        api = bridge.GitHub("not-a-real-token")
        with self.assertRaises(bridge.Rejected):
            api.call("GET", "https://example.invalid")
        with self.assertRaises(bridge.Rejected):
            bridge.NoRedirect().redirect_request(None, None, 302, "", {}, "https://example.invalid")

    def test_receipt_capacity_checked_before_write(self):
        for count in (199, 200):
            api = FakeAPI()
            api.comments = [{"id": n + 100, "body": "a", "updated_at": "now", "user": BOT} for n in range(count)]
            req = self.request(api)
            if count == 199:
                self.assertEqual(self.run_write(api, req)["status"], "published")
                self.assertEqual(api.posts, 1)
            else:
                with self.assertRaises(bridge.Rejected):
                    self.run_write(api, req)
                self.assertEqual(api.posts, 0)
        api = FakeAPI()
        api.item["body"] = "x" * (bridge.MAX_SNAPSHOT - 4096)
        req = self.request(api)
        with self.assertRaises(bridge.Rejected):
            self.run_write(api, req)
        self.assertEqual(api.posts, 0)

    def test_duplicate_bot_receipts_fail_closed(self):
        for kind in ("issue", "discussion"):
            with self.subTest(kind=kind):
                api = FakeAPI()
                req = self.request(api, kind)
                self.run_write(api, req)
                api.comments.append(copy.deepcopy(api.comments[0]))
                with self.assertRaises(bridge.Rejected):
                    self.run_write(api, req)
                self.assertEqual(api.posts, 1)

    def test_discussion_truncation_fails_closed(self):
        api = FakeAPI()
        req = {"repository": bridge.REPOSITORY, "kind": "discussion", "number": 7}
        response = api.graphql("query", {})
        response["repository"]["discussion"]["comments"]["pageInfo"] = {"hasNextPage": True, "endCursor": "next"}
        with patch.object(api, "graphql", return_value=response):
            with self.assertRaises(bridge.Rejected):
                bridge.collect(api, req, "main")
        response["repository"]["discussion"]["comments"] = {"pageInfo": {"hasNextPage": False}, "nodes": [{"replies": {"pageInfo": {"hasNextPage": True}}}]}
        with patch.object(api, "graphql", return_value=response):
            with self.assertRaises(bridge.Rejected):
                bridge.collect(api, req, "main")

    def test_oversized_api_response_fails_closed(self):
        api = bridge.GitHub("not-a-real-token")
        with patch.object(api.opener, "open") as opened:
            opened.return_value.__enter__.return_value.read.return_value = b"x" * (bridge.MAX_RESPONSE + 1)
            with self.assertRaises(bridge.Rejected):
                api.call("GET", "/repos/autobrr/upbrr/issues/7")

    def test_offline_hash_matches_publisher_snapshot(self):
        for kind in ("issue", "discussion"):
            with self.subTest(kind=kind):
                api = FakeAPI()
                api.comments.append({"id": 9, "body": "Existing comment", "updated_at": "now", "user": BOT})
                req = {"repository": bridge.REPOSITORY, "kind": kind, "number": 7}
                source = bridge.collect(api, req, "main")
                result = hash_snapshot.hash_snapshot(bridge.canonical(source).encode())
                self.assertEqual(result, {"head": HEAD, "source_hash": bridge.digest(source)})
        for raw in (b"{", b"[]", b"x" * (bridge.MAX_SNAPSHOT + 1), b'{"repository":"other/repo"}'):
            with self.subTest(raw=raw[:30]), self.assertRaises((bridge.Rejected, ValueError)):
                hash_snapshot.hash_snapshot(raw)

    def test_workflow_least_privilege_and_active(self):
        root = Path(__file__).resolve().parents[2]
        path = root / ".github/workflows/actions-bridge.yml"
        self.assertTrue(path.exists())
        self.assertFalse(path.with_suffix(".yml22").exists())
        source = path.read_text()
        self.assertIn("permissions: {}", source)
        self.assertNotIn("pull_request_target:", source)
        self.assertNotIn("${{ inputs.request }}", source)
        self.assertNotIn("write-all", source)
        jobs = source.split("jobs:\n", 1)[1]
        self.assertNotIn("  snapshot:", jobs)
        issue_job, discussion_job = jobs.split("  discussion_comment:\n", 1)
        self.assertIn("issues: write", issue_job)
        self.assertNotIn("discussions:", issue_job)
        self.assertIn("discussions: write", discussion_job)
        self.assertNotIn("issues:", discussion_job)
        for job, operation in ((issue_job, "issue_comment"), (discussion_job, "discussion_comment")):
            self.assertIn(f"inputs.operation == '{operation}'", job)
            self.assertIn("github.actor_id == '13182387'", job)
            self.assertIn("persist-credentials: false", job)
            self.assertIn("environment: actions-bridge", job)
            self.assertIn("vars.ACTIONS_BRIDGE_ENABLED == 'approved'", job)
        self.assertTrue(bridge.ENABLED)


if __name__ == "__main__":
    unittest.main()
