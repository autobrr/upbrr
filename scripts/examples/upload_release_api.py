#!/usr/bin/env python3
r"""Drive one release through the composite upload and feedback endpoints.

Safe strict-unattended debug run with no client injection:

PowerShell:
    python .\scripts\upload_release_api.py `
        "D:\Media\Example.Release.2026.1080p-GRP.mkv" `
        --mode debug --no-seed --tracker "TRACKER_ID"

To demonstrate required-action feedback, add ``--unattended-confirm``. The
script polls the accepted operation, reloads the workflow, inspects
``workflow.requiredActions``, submits one typed response to
``POST /uploads/{workflowId}/feedback``, then repeats with the new operation.
There is no separate feedback-poll endpoint: pending actions are part of the
authoritative workflow returned by ``GET /workflows/{workflowId}``.

WARNING: ``--mode upload`` permits real tracker submissions. Without
``--no-seed``, successful tracker uploads are also injected into the configured
torrent client.

Create a suitable token with ``workflow:read`` and ``workflow:execute`` scopes
in Web UI Settings > API Tokens.

The input path must exist on the machine running ``upbrr serve``.
"""

from __future__ import annotations

import argparse
from getpass import getpass
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


MAX_TRANSITIONS = 64


def capabilities_url(api_url: str) -> str:
    """Return the authenticated capability-probe URL."""
    return f"{api_url.rstrip('/')}/capabilities"


def workflow_url(api_url: str, workflow_id: object) -> str:
    """Return the owner-scoped workflow URL."""
    return f"{api_url.rstrip('/')}/workflows/{quote(str(workflow_id), safe='')}"


def operation_url(api_url: str, workflow_id: object, operation_id: object) -> str:
    """Return the authoritative operation polling URL."""
    return (
        f"{workflow_url(api_url, workflow_id)}/operations/"
        f"{quote(str(operation_id), safe='')}"
    )


def feedback_url(api_url: str, workflow_id: object) -> str:
    """Return the composite feedback URL."""
    encoded_workflow_id = quote(str(workflow_id), safe="")
    return f"{api_url.rstrip('/')}/uploads/{encoded_workflow_id}/feedback"


def require_object(value: object, label: str) -> dict[str, Any]:
    """Return one JSON object or raise a stable contract error."""
    if not isinstance(value, dict):
        raise APIError(f"API response omitted {label}")
    return value


def wait_for_operation(
    api_url: str,
    token: str,
    current: dict[str, Any],
    poll_interval: float,
    timeout: float,
) -> dict[str, Any]:
    """Poll the attached operation to terminal state, then reload its workflow."""
    operation = current.get("operation")
    if not isinstance(operation, dict):
        return current

    workflow = require_object(current.get("workflow"), "workflow")
    workflow_id = workflow.get("id")
    operation_id = operation.get("id")
    if not workflow_id or not operation_id:
        raise APIError("API response omitted workflow or operation ID")

    deadline = time.monotonic() + timeout
    last_progress: tuple[object, object, object] | None = None
    while operation.get("status") in NON_TERMINAL_OPERATION_STATUSES:
        progress = (
            operation.get("status"),
            operation.get("phase"),
            operation.get("progress"),
        )
        if progress != last_progress:
            print(
                "operation "
                f"status={progress[0]} phase={progress[1] or '-'} "
                f"progress={progress[2] if progress[2] is not None else '-'}",
                file=sys.stderr,
            )
            last_progress = progress
        if time.monotonic() >= deadline:
            raise APIError(f"workflow operation timed out after {timeout:g} seconds")
        time.sleep(poll_interval)
        operation = request_json(
            "GET",
            operation_url(api_url, workflow_id, operation_id),
            token,
        )

    print(
        f"operation status={operation.get('status')} "
        f"message={operation.get('message') or '-'}",
        file=sys.stderr,
    )
    return request_json("GET", workflow_url(api_url, workflow_id), token)


def pending_actions(current: dict[str, Any]) -> list[dict[str, Any]]:
    """Return pending actions from the authoritative workflow projection."""
    workflow = require_object(current.get("workflow"), "workflow")
    actions = workflow.get("requiredActions", [])
    if not isinstance(actions, list):
        raise APIError("API response contained invalid required actions")
    return [
        action
        for action in actions
        if isinstance(action, dict) and action.get("status") == "pending"
    ]


def prompt_line(message: str) -> str:
    """Read one interactive value without mixing prompts into JSON stdout."""
    print(message, end="", file=sys.stderr, flush=True)
    try:
        return input()
    except EOFError as exc:
        raise APIError("interactive feedback input ended unexpectedly") from exc


def require_confirmation(message: str) -> None:
    """Require an explicit yes before constructing affirmative feedback."""
    answer = prompt_line(f"{message} [y/N]: ").strip().lower()
    if answer not in {"y", "yes"}:
        raise APIError("required action was not confirmed")


def prompt_json_object(message: str) -> dict[str, Any]:
    """Read one JSON object for structured tracker or preparation input."""
    while True:
        raw = prompt_line(message).strip()
        try:
            value = json.loads(raw)
        except json.JSONDecodeError:
            print("Enter one valid JSON object.", file=sys.stderr)
            continue
        if isinstance(value, dict):
            return value
        print("Value must be a JSON object.", file=sys.stderr)


def action_options(action: dict[str, Any]) -> list[dict[str, str]]:
    """Return validated non-secret options from one required action."""
    raw_options = action.get("options", [])
    if not isinstance(raw_options, list):
        raise APIError("required action contained invalid options")
    options: list[dict[str, str]] = []
    for option in raw_options:
        if not isinstance(option, dict):
            continue
        value = option.get("value")
        if isinstance(value, str) and value:
            label = option.get("label")
            options.append(
                {
                    "value": value,
                    "label": label if isinstance(label, str) and label else value,
                }
            )
    return options


def select_action_options(
    action: dict[str, Any],
    *,
    multiple: bool,
    allow_all: bool = False,
) -> tuple[list[str], bool]:
    """Prompt for one or more backend-provided option values."""
    options = action_options(action)
    if not options:
        raise APIError("required action supplied no selectable options")
    for index, option in enumerate(options, start=1):
        print(f"  {index}: {option['label']} ({option['value']})", file=sys.stderr)
    suffix = "comma-separated numbers"
    if not multiple:
        suffix = "one number"
    if allow_all:
        suffix += " or 'all'"
    raw = prompt_line(f"Select {suffix}: ").strip().lower()
    if allow_all and raw == "all":
        return [], True
    parts = [part.strip() for part in raw.split(",") if part.strip()]
    if not multiple and len(parts) != 1:
        raise APIError("required action needs exactly one selection")
    if not parts:
        raise APIError("required action needs a selection")
    selected: list[str] = []
    for part in parts:
        try:
            index = int(part)
        except ValueError as exc:
            raise APIError("action selection must use the displayed numbers") from exc
        if index < 1 or index > len(options):
            raise APIError("action selection is out of range")
        value = options[index - 1]["value"]
        if value not in selected:
            selected.append(value)
    return selected, False


def action_identity(action: dict[str, Any]) -> dict[str, Any]:
    """Build exact action/revision authority for a feedback request."""
    action_id = action.get("id")
    revision = action.get("workflowRevision")
    if not isinstance(action_id, str) or not action_id:
        raise APIError("required action omitted its ID")
    if not isinstance(revision, int) or revision <= 0:
        raise APIError("required action omitted its workflow revision")
    return {
        "id": action_id,
        "workflowRevision": revision,
    }


def build_feedback(action: dict[str, Any]) -> dict[str, Any]:
    """Interactively construct the discriminated response for one action."""
    kind = action.get("kind")
    tracker_id = action.get("trackerId")
    prompt = action.get("prompt")
    print(
        f"required action kind={kind} tracker={tracker_id or '-'}\n"
        f"  {prompt or 'No prompt supplied.'}",
        file=sys.stderr,
    )
    identity = action_identity(action)

    if kind == "select_playlist":
        selected, use_all = select_action_options(
            action,
            multiple=True,
            allow_all=True,
        )
        response = {
            "kind": "playlistSelection",
            "playlistSelection": {
                "selected": selected,
                "useAll": use_all,
            },
        }
    elif kind == "select_metadata":
        options = action_options(action)
        if options:
            selected, _ = select_action_options(action, multiple=False)
            response = {
                "kind": "metadataSelection",
                "metadataSelection": {"selectedValues": selected},
            }
        else:
            facts = prompt_json_object(
                'Replacement preparation facts JSON (for example {"category":"movie"}): '
            )
            response = {
                "kind": "metadataSelection",
                "metadataSelection": {"facts": facts},
            }
    elif kind == "confirm_rescan":
        require_confirmation("Allow the requested source rescan?")
        response = {
            "kind": "rescanConfirmation",
            "rescanConfirmation": {"confirmed": True},
        }
    elif kind == "authenticate_tracker":
        if not isinstance(tracker_id, str) or not tracker_id:
            raise APIError("tracker authentication action omitted tracker ID")
        prompt_line(
            "Complete authentication through configured server credentials or the WebUI, "
            "then press Enter to revalidate. If authentication is still unavailable, "
            "this tracker will be skipped: "
        )
        response = {
            "kind": "trackerAuthentication",
            "trackerAuthentication": {"trackerId": tracker_id},
        }
    elif kind == "provide_two_factor":
        if not isinstance(tracker_id, str) or not tracker_id:
            raise APIError("two-factor action omitted tracker ID")
        options = action_options(action)
        if len(options) != 1:
            raise APIError("two-factor action omitted its active challenge")
        try:
            code = getpass("Two-factor code: ").strip()
        except EOFError as exc:
            raise APIError("two-factor input ended unexpectedly") from exc
        if not code:
            raise APIError("two-factor code is required")
        response = {
            "kind": "twoFactor",
            "twoFactor": {
                "trackerId": tracker_id,
                "challengeId": options[0]["value"],
                "code": code,
            },
        }
    elif kind == "provide_tracker_input":
        if not isinstance(tracker_id, str) or not tracker_id:
            raise APIError("tracker input action omitted tracker ID")
        projection = prompt_json_object(
            "Tracker projection JSON (uploadReleaseName, questionnaire, config, site): "
        )
        response = {
            "kind": "trackerInput",
            "trackerInput": {
                "trackerId": tracker_id,
                "projection": projection,
            },
        }
    elif kind == "answer_questionnaire":
        if not isinstance(tracker_id, str) or not tracker_id:
            raise APIError("questionnaire action omitted tracker ID")
        answers = prompt_json_object(
            'Questionnaire answers JSON (for example {"edition":"standard"}): '
        )
        response = {
            "kind": "questionnaire",
            "questionnaire": {
                "trackerId": tracker_id,
                "answers": answers,
            },
        }
    elif kind == "authorize_rules":
        require_confirmation("Acknowledge the waivable tracker rules?")
        response = {
            "kind": "ruleAuthorization",
            "ruleAuthorization": {"confirmed": True},
        }
    elif kind == "review_duplicates":
        decision = (
            prompt_line(
                "Duplicate decision: 'accepted' blocks this tracker; "
                "'ignored' permits upload: "
            )
            .strip()
            .lower()
        )
        if decision not in {"accepted", "ignored"}:
            raise APIError("duplicate decision must be accepted or ignored")
        duplicate_review: dict[str, Any] = {"decision": decision}
        if isinstance(tracker_id, str) and tracker_id:
            duplicate_review["trackerId"] = tracker_id
        response = {
            "kind": "duplicateReview",
            "duplicateReview": duplicate_review,
        }
    elif kind == "approve_trackers":
        selected, _ = select_action_options(action, multiple=True)
        response = {
            "kind": "trackerApproval",
            "trackerApproval": {
                "confirmed": True,
                "trackerIds": selected,
            },
        }
    elif kind == "reprepare":
        require_confirmation("Force a fresh prepared generation?")
        response = {
            "kind": "reprepare",
            "reprepare": {"confirmed": True},
        }
    elif kind == "reconcile_submission":
        confirmation = prompt_line(
            "Verify the external effect did NOT complete, then type 'not_completed': "
        ).strip()
        if confirmation != "not_completed":
            raise APIError("reconciliation requires exact not_completed confirmation")
        response = {
            "kind": "reconciliation",
            "reconciliation": {"selection": "not_completed"},
        }
    else:
        raise APIError(f"unsupported required action kind: {kind}")

    return {
        "action": identity,
        "response": response,
    }


def stalled_reason(current: dict[str, Any]) -> str:
    """Summarize safe retained failure state without exposing request secrets."""
    workflow = require_object(current.get("workflow"), "workflow")
    failures = workflow.get("failures")
    if isinstance(failures, list) and failures:
        return f"workflow failed: {json.dumps(failures)}"
    operation = current.get("operation")
    if isinstance(operation, dict):
        operation_failures = operation.get("failures")
        if isinstance(operation_failures, list) and operation_failures:
            return f"operation failed: {json.dumps(operation_failures)}"
        if operation.get("message"):
            return (
                f"operation stopped with status={operation.get('status')}: "
                f"{operation.get('message')}"
            )
    return f"workflow made no progress (status={workflow.get('status')})"


def completed_result(
    current: dict[str, Any],
    mode: str,
) -> dict[str, Any] | None:
    """Return the mode-specific terminal result when it is retained."""
    workflow = require_object(current.get("workflow"), "workflow")
    result_key = "dryRun" if mode == "debug" else "uploadResult"
    result = current.get(result_key)
    if not isinstance(result, dict):
        return None
    return {
        "workflowId": workflow.get("id"),
        result_key: result,
    }


def create_payload(
    source_path: str,
    trackers: list[str],
    mode: str,
    unattended_confirm: bool,
    screens: int,
    no_seed: bool,
    skip_client_search: bool,
    duplicate_policy: str | None,
) -> dict[str, Any]:
    """Build the typed composite upload request body."""
    payload: dict[str, Any] = {
        "source": {"path": source_path},
        "unattended": {"confirm": unattended_confirm},
        "execution": {"mode": mode},
        "media": {"screenshots": {"count": screens}},
        "client": {"noSeed": no_seed},
    }
    if trackers:
        payload["trackers"] = {"include": trackers}
    if skip_client_search:
        payload["preparation"] = {"clientSearch": {"skip": True}}
    if duplicate_policy is not None:
        payload["duplicates"] = {"onEvidence": duplicate_policy}
    return payload


def upload_release(
    api_url: str,
    token: str,
    source_path: str,
    trackers: list[str],
    mode: str,
    unattended_confirm: bool,
    screens: int,
    no_seed: bool,
    skip_client_search: bool,
    duplicate_policy: str | None,
    poll_interval: float,
    timeout: float,
) -> dict[str, Any]:
    """Validate server capabilities, then create and drive one composite upload."""
    capabilities = request_json("GET", capabilities_url(api_url), token)
    api_version = capabilities.get("apiVersion")
    features = capabilities.get("features")
    scopes = capabilities.get("scopes")
    if not isinstance(api_version, str) or api_version.split(".", 1)[0] != "1":
        raise APIError("server does not expose a compatible API v1 capability contract")
    if not isinstance(features, dict) or not features.get("compositeUpload"):
        raise APIError("server does not support composite uploads")
    if not unattended_confirm and not features.get(
        "strictEligibleTrackerContinuation"
    ):
        raise APIError("server does not support strict eligible-tracker continuation")
    if not isinstance(scopes, list) or "workflow:execute" not in scopes:
        raise APIError("token does not grant workflow:execute")
    payload = create_payload(
        source_path,
        trackers,
        mode,
        unattended_confirm,
        screens,
        no_seed,
        skip_client_search,
        duplicate_policy,
    )
    current = request_json(
        "POST",
        f"{api_url.rstrip('/')}/uploads",
        token,
        payload=payload,
        idempotency_key=str(uuid.uuid4()),
    )

    for _ in range(MAX_TRANSITIONS):
        current = wait_for_operation(
            api_url,
            token,
            current,
            poll_interval,
            timeout,
        )
        actions = pending_actions(current)
        result = completed_result(current, mode)
        if result is not None and not actions:
            return result
        if not actions:
            raise APIError(stalled_reason(current))
        if not unattended_confirm:
            summaries = [
                {
                    "kind": action.get("kind"),
                    "trackerId": action.get("trackerId"),
                    "prompt": action.get("prompt"),
                }
                for action in actions
            ]
            raise APIError(
                "strict unattended workflow requires feedback; rerun with "
                f"--unattended-confirm: {json.dumps(summaries)}"
            )

        action = actions[0]
        feedback = build_feedback(action)
        identity = require_object(feedback.get("action"), "feedback action")
        revision = identity.get("workflowRevision")
        if not isinstance(revision, int):
            raise APIError("feedback action omitted workflow revision")
        workflow = require_object(current.get("workflow"), "workflow")
        workflow_id = workflow.get("id")
        if not workflow_id:
            raise APIError("API response omitted workflow ID")
        if workflow.get("revision") != revision:
            raise APIError("required action is stale; reload and retry")

        current = request_json(
            "POST",
            feedback_url(api_url, workflow_id),
            token,
            payload=feedback,
            idempotency_key=str(uuid.uuid4()),
            expected_revision=revision,
        )

    raise APIError(f"workflow exceeded the {MAX_TRANSITIONS}-transition limit")


def parse_args() -> argparse.Namespace:
    """Parse the example client's command-line options."""
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
        help=(
            "target tracker ID; repeat for multiple trackers "
            "(required in strict mode)"
        ),
    )
    parser.add_argument(
        "--mode",
        choices=("debug", "upload"),
        default="debug",
        help="debug dry-run or real tracker upload (default: %(default)s)",
    )
    parser.add_argument(
        "--unattended-confirm",
        action="store_true",
        help="interactively answer required actions; default strict mode never prompts",
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
        "--duplicate-policy",
        choices=("ask", "block", "upload"),
        help="override duplicate policy (default: ask in confirm mode, block in strict mode)",
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
    """Run the composite upload example."""
    args = parse_args()
    token = args.token or os.environ.get("UPBRR_API_TOKEN")
    if not token:
        print("error: pass --token or set UPBRR_API_TOKEN", file=sys.stderr)
        return 2
    if args.screens < 0:
        print("error: --screens cannot be negative", file=sys.stderr)
        return 2
    if args.poll_interval <= 0 or args.timeout <= 0:
        print(
            "error: --poll-interval and --timeout must be greater than zero",
            file=sys.stderr,
        )
        return 2
    if not args.unattended_confirm and not args.tracker:
        print("error: strict mode requires at least one --tracker", file=sys.stderr)
        return 2

    try:
        result = upload_release(
            args.api_url,
            token,
            args.input_path,
            args.tracker,
            args.mode,
            args.unattended_confirm,
            args.screens,
            args.no_seed,
            args.skip_client_search,
            args.duplicate_policy,
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
