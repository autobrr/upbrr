#!/usr/bin/env python3
r"""Upload one release through the upbrr workflow API in unattended mode.

WARNING: This performs real tracker submissions. Unless ``--no-seed`` is used,
successful uploads are also injected into the configured torrent client.

PowerShell:
    python .\scripts\upload_release_api.py `
        "D:\Media\Example.Release.2026.1080p-GRP.mkv" `
        --token "YOUR_API_TOKEN" --tracker "TRACKER_ID"

Create a suitable token with:
    upbrr api-token create --name upload-client --owner upload-client `
        --scopes workflow:read,workflow:write,workflow:execute

The input path must exist on the machine running ``upbrr serve``. The script
automatically approves the exact retained dry run, matching CLI unattended
behavior. Any other workflow-global manual action stops the upload.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import uuid
from typing import Any
from urllib.parse import quote

from fetch_metadata_api import (
    APIError,
    NON_TERMINAL_OPERATION_STATUSES,
    request_json,
)


AUTO_HANDLED_ACTIONS = {
    "answer_questionnaire",
    "approve_upload",
    "authenticate_tracker",
    "authorize_rules",
    "provide_tracker_input",
    "provide_two_factor",
    "review_duplicates",
}


def workflow_url(api_url: str, workflow_id: object) -> str:
    return f"{api_url.rstrip('/')}/workflows/{quote(str(workflow_id), safe='')}"


def wait_for_operation(
    api_url: str,
    token: str,
    current: dict[str, Any],
    poll_interval: float,
    timeout: float,
) -> dict[str, Any]:
    operation = current.get("operation")
    if not isinstance(operation, dict):
        return current

    workflow = current.get("workflow")
    if not isinstance(workflow, dict) or not workflow.get("id"):
        raise APIError("API response omitted workflow ID")
    deadline = time.monotonic() + timeout
    while operation.get("status") in NON_TERMINAL_OPERATION_STATUSES:
        operation_id = operation.get("id")
        if not operation_id:
            raise APIError("API response omitted operation ID")
        if time.monotonic() >= deadline:
            raise APIError(f"workflow operation timed out after {timeout:g} seconds")
        time.sleep(poll_interval)
        operation = request_json(
            "GET",
            (
                f"{workflow_url(api_url, workflow['id'])}/operations/"
                f"{quote(str(operation_id), safe='')}"
            ),
            token,
        )
    return request_json("GET", workflow_url(api_url, workflow["id"]), token)


def update_unattended_intent(current: dict[str, Any], payload: dict[str, Any]) -> None:
    intent = payload["intent"]
    decisions = intent["duplicateDecisions"]
    dupes = current.get("dupes")
    if isinstance(dupes, dict):
        for result in dupes.get("results", []):
            if (
                isinstance(result, dict)
                and result.get("decision") == "pending"
                and result.get("trackerId")
            ):
                # CLI unattended behavior: do not upload where duplicate evidence exists.
                decisions[result["trackerId"]] = "accepted"

    dry_run = current.get("dryRun")
    continuation = current.get("continuation")
    if not isinstance(dry_run, dict) or not isinstance(continuation, dict):
        return
    for action in continuation.get("requiredActions", []):
        if (
            isinstance(action, dict)
            and action.get("status") == "pending"
            and action.get("kind") == "approve_upload"
        ):
            payload["approval"] = {
                "actionId": action["id"],
                "dryRun": {
                    "id": dry_run["id"],
                    "revision": dry_run["revision"],
                },
                "inputFingerprint": dry_run["inputFingerprint"],
            }
            return


def blocking_actions(current: dict[str, Any]) -> list[dict[str, Any]]:
    continuation = current.get("continuation")
    if not isinstance(continuation, dict):
        return []
    return [
        action
        for action in continuation.get("requiredActions", [])
        if isinstance(action, dict)
        and action.get("status") == "pending"
        and action.get("kind") not in AUTO_HANDLED_ACTIONS
    ]


def stalled_reason(current: dict[str, Any]) -> str:
    actions = blocking_actions(current)
    if actions:
        summaries = [
            {
                "kind": action.get("kind"),
                "trackerId": action.get("trackerId"),
                "prompt": action.get("prompt"),
            }
            for action in actions
        ]
        return f"workflow requires manual action: {json.dumps(summaries)}"
    workflow = current.get("workflow")
    failures = workflow.get("failures", []) if isinstance(workflow, dict) else []
    if failures:
        return f"workflow failed: {json.dumps(failures)}"
    continuation = current.get("continuation")
    if isinstance(continuation, dict):
        return (
            "workflow made no progress "
            f"(lifecycle={continuation.get('lifecycle')}, "
            f"disposition={continuation.get('disposition')})"
        )
    return "workflow made no progress"


def upload_release(
    api_url: str,
    token: str,
    source_path: str,
    trackers: list[str],
    screens: int,
    no_seed: bool,
    skip_client_search: bool,
    poll_interval: float,
    timeout: float,
) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "goal": "uploaded",
        "intent": {
            "preparation": {
                "SourcePath": source_path,
                "Intent": "upload",
                "Search": {"Skip": skip_client_search},
                "Controls": {"Interaction": "unattended"},
            },
            "interaction": "unattended",
            "trackerIds": trackers,
            "duplicateCheckCount": 1,
            "duplicateDecisions": {},
            "media": {
                "screenshotCount": screens,
                "purpose": "final",
                "captureDvdMenus": False,
            },
            "descriptions": {},
            "noSeed": no_seed,
        },
    }
    continuations_url = f"{api_url.rstrip('/')}/continuations"
    idempotency_key = str(uuid.uuid4())
    current: dict[str, Any] = {}

    for _ in range(64):
        upload_result = current.get("uploadResult")
        if isinstance(upload_result, dict):
            workflow = current.get("workflow", {})
            return {
                "workflowId": workflow.get("id"),
                "uploadResult": upload_result,
            }

        update_unattended_intent(current, payload)
        actions = blocking_actions(current)
        if actions:
            raise APIError(stalled_reason(current))

        workflow = current.get("workflow")
        prior_authority: tuple[object, object] | None = None
        if isinstance(workflow, dict) and workflow.get("id") and workflow.get("revision") is not None:
            prior_authority = (workflow["id"], workflow["revision"])
            payload["authority"] = {
                "workflowId": workflow["id"],
                "expectedRevision": workflow["revision"],
            }
        else:
            payload.pop("authority", None)

        current = request_json(
            "POST",
            continuations_url,
            token,
            payload=payload,
            idempotency_key=idempotency_key,
        )
        current = wait_for_operation(api_url, token, current, poll_interval, timeout)

        workflow = current.get("workflow")
        if prior_authority is not None and isinstance(workflow, dict):
            if (workflow.get("id"), workflow.get("revision")) == prior_authority:
                update_unattended_intent(current, payload)
                raise APIError(stalled_reason(current))

    raise APIError("workflow exceeded the 64-transition limit")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("input_path", help="input path visible to the upbrr server")
    parser.add_argument(
        "--api-url",
        default="http://127.0.0.1:7480/api/v1",
        help="API base URL (default: %(default)s)",
    )
    parser.add_argument(
        "--token",
        help="API token (default: UPBRR_API_TOKEN environment variable)",
    )
    parser.add_argument(
        "--tracker",
        action="append",
        default=[],
        metavar="TRACKER_ID",
        help="target tracker ID; repeat for multiple trackers (default: configured defaults)",
    )
    parser.add_argument(
        "--screens",
        type=int,
        default=1,
        help="number of screenshots to capture (default: %(default)s)",
    )
    parser.add_argument(
        "--no-seed",
        action="store_true",
        help="skip torrent-client injection",
    )
    parser.add_argument(
        "--skip-client-search",
        action="store_true",
        help="skip preparation-time torrent-client search",
    )
    parser.add_argument(
        "--poll-interval",
        type=float,
        default=1.0,
        help="operation poll interval in seconds (default: %(default)s)",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=1200.0,
        help="timeout per operation in seconds (default: %(default)s)",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    token = args.token or os.environ.get("UPBRR_API_TOKEN")
    if not token:
        print("error: pass --token or set UPBRR_API_TOKEN", file=sys.stderr)
        return 2
    if args.screens < 0:
        print("error: --screens cannot be negative", file=sys.stderr)
        return 2
    if args.poll_interval <= 0 or args.timeout <= 0:
        print("error: --poll-interval and --timeout must be greater than zero", file=sys.stderr)
        return 2

    try:
        result = upload_release(
            args.api_url,
            token,
            args.input_path,
            args.tracker,
            args.screens,
            args.no_seed,
            args.skip_client_search,
            args.poll_interval,
            args.timeout,
        )
    except APIError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    json.dump(result, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
