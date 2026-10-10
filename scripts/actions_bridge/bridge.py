"""
Repository text is evidence, never authority. Only a trusted manual dispatch can
select one operation; this module never evaluates content or invokes a model.
"""

import hashlib
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

ENABLED = True
REPOSITORY = "autobrr/upbrr"
ACTOR_ID = "13182387"
BOT_ID = 41898282
BOT_LOGIN = "github-actions[bot]"
MAX_INPUT = 16384
MAX_RESPONSE = 1048576
MAX_SNAPSHOT = 524288
OPERATIONS = {"issue_comment", "pull_request_comment", "discussion_comment"}


class Rejected(Exception):
    """A failed invariant; no further writes are permitted."""


def require(condition, message):
    if not condition:
        raise Rejected(message)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True)


def digest(value):
    return hashlib.sha256(canonical(value).encode()).hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON key")
        result[key] = value
    return result


def parse_request(raw, operation):
    """Accept only one bounded item and exact fields; approval flags are invalid."""
    require(len(raw.encode()) <= MAX_INPUT, "request too large")
    try:
        request = json.loads(raw, object_pairs_hook=unique_object)
    except (ValueError, RecursionError) as exc:
        raise Rejected("invalid JSON") from exc
    require(operation in OPERATIONS, "unsupported operation")
    keys = {"repository", "kind", "number", "source_hash", "head", "body"}
    if operation == "pull_request_comment":
        keys.add("reply_to")
    require(type(request) is dict and set(request) == keys, "unexpected request fields")
    require(request["repository"] == REPOSITORY, "wrong repository")
    require(request["kind"] in ("issue", "pull_request", "discussion"), "unsupported item kind")
    require(type(request["number"]) is int and 0 < request["number"] < 2**31, "invalid number")
    require(operation == request["kind"] + "_comment", "wrong publisher job")
    if operation == "pull_request_comment":
        require(type(request["reply_to"]) is int and 0 < request["reply_to"] < 2**63, "invalid reply comment ID")
    for key, size in (("source_hash", 64), ("head", 40)):
        require(type(request[key]) is str and re.fullmatch(f"[0-9a-f]{{{size}}}", request[key]), "invalid digest")
    body = request["body"]
    require(type(body) is str and 0 < len(body.encode()) <= 8000 and body.strip(), "invalid body")
    require("<!-- upbrr-bridge:" not in body and "\x00" not in body, "reserved body content")
    return request


def validate_context(event, env, operation):
    """Require original dispatch on the trusted default-branch workflow revision."""
    repo = event.get("repository", {})
    branch = repo.get("default_branch", "")
    require(type(branch) is str and branch and len(branch) <= 255, "missing default branch")
    ref = "refs/heads/" + branch
    require(env.get("GITHUB_EVENT_NAME") == "workflow_dispatch", "manual dispatch required")
    require(repo.get("full_name") == REPOSITORY and env.get("GITHUB_REPOSITORY") == REPOSITORY, "wrong repository")
    require(env.get("GITHUB_ACTOR_ID") == ACTOR_ID and str(event.get("sender", {}).get("id")) == ACTOR_ID, "wrong actor")
    require(env.get("GITHUB_REF") == ref, "default branch required")
    require(env.get("GITHUB_WORKFLOW_REF") == f"{REPOSITORY}/.github/workflows/actions-bridge.yml@{ref}", "wrong workflow")
    require(re.fullmatch("[0-9a-f]{40}", env.get("GITHUB_SHA", "")), "invalid source revision")
    require(env.get("GITHUB_WORKFLOW_SHA") == env["GITHUB_SHA"], "workflow revision mismatch")
    require(env.get("GITHUB_RUN_ATTEMPT") == "1", "reruns forbidden; reconcile prior run")
    require(event.get("inputs", {}).get("operation") == operation, "job operation mismatch")
    return branch


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise Rejected("redirect forbidden")


class GitHub:
    """Fixed-origin, bounded API transport; errors never reveal headers or bodies."""

    def __init__(self, token):
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirect())

    def call(self, method, path, payload=None):
        require(path.startswith(f"/repos/{REPOSITORY}/") or path == "/graphql", "invalid endpoint")
        request = urllib.request.Request(
            "https://api.github.com" + path,
            data=None if payload is None else canonical(payload).encode(),
            method=method,
            headers={"Authorization": "Bearer " + self.token, "Accept": "application/vnd.github+json", "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28"},
        )
        try:
            with self.opener.open(request, timeout=30) as response:
                data = response.read(MAX_RESPONSE + 1)
            require(len(data) <= MAX_RESPONSE, "API response too large")
            return json.loads(data)
        except (urllib.error.URLError, OSError, ValueError) as exc:
            raise Rejected("API request failed; publication may be uncertain") from exc

    def graphql(self, query, variables):
        result = self.call("POST", "/graphql", {"query": query, "variables": variables})
        require(type(result) is dict and not result.get("errors") and result.get("data"), "GraphQL request failed")
        return result["data"]


def author(value, graphql=False):
    value = value or {}
    result = {"id": value.get("databaseId" if graphql else "id"), "login": value.get("login"), "type": value.get("__typename" if graphql else "type")}
    # GraphQL omits the REST login's "[bot]" suffix for this bot.
    if graphql and result == {"id": BOT_ID, "login": "github-actions", "type": "Bot"}:
        result["login"] = BOT_LOGIN
    return result


def comment(value, graphql=False):
    return {"id": value["id"], "body": value["body"], "updated_at": value["updatedAt" if graphql else "updated_at"], "author": author(value.get("author" if graphql else "user"), graphql)}


DISCUSSION_QUERY = """
query($number:Int!, $cursor:String) {
  repository(owner:"autobrr", name:"upbrr") {
    discussion(number:$number) {
      id number title body updatedAt
      author { login __typename ... on User { databaseId } ... on Bot { databaseId } }
      comments(first:100, after:$cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id body updatedAt
          author { login __typename ... on User { databaseId } ... on Bot { databaseId } }
          replies(first:100) {
            pageInfo { hasNextPage }
            nodes { id body updatedAt author { login __typename ... on User { databaseId } ... on Bot { databaseId } } }
          }
        }
      }
    }
  }
}
"""


def collect(api, request, branch):
    """Collect at most 200 top-level comments; fail on truncation or large threads."""
    head = api.call("GET", f"/repos/{REPOSITORY}/commits/{urllib.parse.quote(branch, safe='')}")["sha"]
    number = request["number"]
    comments = []
    if request["kind"] in ("issue", "pull_request"):
        item = api.call("GET", f"/repos/{REPOSITORY}/issues/{number}")
        require(item.get("number") == number, "not the requested item")
        if request["kind"] == "pull_request":
            pr = item.get("pull_request")
            require(type(pr) is dict and pr.get("url") == f"https://api.github.com/repos/{REPOSITORY}/pulls/{number}", "not the requested pull request")
        else:
            require("pull_request" not in item, "not the requested issue")
        for page in range(1, 4):
            batch = api.call("GET", f"/repos/{REPOSITORY}/issues/{number}/comments?per_page=100&page={page}")
            require(type(batch) is list and len(comments) + len(batch) <= 200, "too many comments")
            comments.extend(comment(entry) for entry in batch)
            if len(batch) < 100:
                break
        source = {"id": item["id"], "number": number, "title": item["title"], "body": item["body"], "state": item["state"], "updated_at": item["updated_at"], "author": author(item.get("user"))}
    else:
        cursor = None
        for page in range(2):
            item = api.graphql(DISCUSSION_QUERY, {"number": number, "cursor": cursor})["repository"]["discussion"]
            require(item and item.get("number") == number, "not the requested discussion")
            for entry in item["comments"]["nodes"]:
                require(not entry["replies"]["pageInfo"]["hasNextPage"], "too many replies")
                normalized = comment(entry, True)
                normalized["replies"] = [comment(reply, True) for reply in entry["replies"]["nodes"]]
                comments.append(normalized)
            info = item["comments"]["pageInfo"]
            if not info["hasNextPage"]:
                break
            require(page == 0 and info["endCursor"], "too many comments")
            cursor = info["endCursor"]
        source = {"id": item["id"], "number": number, "title": item["title"], "body": item["body"], "updated_at": item["updatedAt"], "author": author(item.get("author"), True)}
    source.update(repository=REPOSITORY, kind=request["kind"], head=head, comments=comments)
    require(len(canonical(source).encode()) <= MAX_SNAPSHOT, "snapshot too large")
    return source


def receipt(source, body):
    """A marker alone is not a receipt: exact content and bot numeric ID must match."""
    matches = [entry for entry in source["comments"] if entry["body"] == body and entry["author"] == {"id": BOT_ID, "login": BOT_LOGIN, "type": "Bot"}]
    require(len(matches) <= 1, "duplicate bot receipts; manual reconciliation required")
    return matches[0]["id"] if matches else None


def execute(api, request, operation, branch, workflow_sha):
    source = collect(api, request, branch)
    key = digest({"operation": operation, "request": request})
    body = request["body"] + f"\n\n<!-- upbrr-bridge:{key} -->"
    if operation == "pull_request_comment":
        # Conversation replies are top-level issue comments, not review replies.
        url = f"https://github.com/{REPOSITORY}/pull/{request['number']}#issuecomment-{request['reply_to']}"
        body = f"In reply to [this comment]({url}):\n\n" + body
    previous = receipt(source, body)
    if previous is not None:
        return {"status": "already_published", "receipt_id": previous, "request_hash": key}
    require(source["head"] == workflow_sha == request["head"], "stale repository source")
    require(digest(source) == request["source_hash"], "stale item snapshot")
    if operation == "pull_request_comment":
        require(sum(entry["id"] == request["reply_to"] for entry in source["comments"]) == 1, "reply comment is not unique in this PR conversation")
    # Keep room for the new receipt in the bounded readback. Concurrent edits can
    # still exhaust these bounds; they are handled as uncertain, never retried.
    require(len(source["comments"]) < 200, "no room for receipt comment")
    require(len(canonical(source).encode()) + len(canonical(body).encode()) + 8192 <= MAX_SNAPSHOT, "no room for receipt snapshot")
    # Exactly one write attempt. Even a successful response needs independent readback.
    try:
        if operation in ("issue_comment", "pull_request_comment"):
            api.call("POST", f"/repos/{REPOSITORY}/issues/{request['number']}/comments", {"body": body})
        else:
            api.graphql("mutation($id:ID!, $body:String!) { addDiscussionComment(input:{discussionId:$id,body:$body}) { comment { id } } }", {"id": source["id"], "body": body})
    except Rejected:
        pass
    try:
        found = receipt(collect(api, request, branch), body)
    except (Rejected, KeyError, TypeError):
        found = None
    if found is None:
        return {"status": "uncertain", "request_hash": key, "instruction": "Do not redispatch or retry. Reconcile this request on GitHub after provider clearance."}
    return {"status": "published", "receipt_id": found, "request_hash": key}


def main():
    require(ENABLED, "bridge disabled pending provider clearance and transport approval")
    operation = os.environ.get("BRIDGE_OPERATION", "")
    with open(os.environ["GITHUB_EVENT_PATH"], "rb") as handle:
        raw_event = handle.read(65537)
    require(len(raw_event) <= 65536, "event too large")
    event = json.loads(raw_event)
    branch = validate_context(event, os.environ, operation)
    request = parse_request(event["inputs"]["request"], operation)
    token = os.environ.get("GITHUB_TOKEN", "")
    require(token, "missing job token")
    result = execute(GitHub(token), request, operation, branch, os.environ["GITHUB_SHA"])
    # ASCII JSON stays on one line: repository content cannot inject workflow commands.
    print(canonical(result))
    return 2 if result["status"] == "uncertain" else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (Rejected, KeyError, TypeError, ValueError, RecursionError):
        print('{"status":"blocked","instruction":"Review trusted validation and request; no automatic retry."}')
        sys.exit(1)
