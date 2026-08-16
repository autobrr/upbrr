#!/usr/bin/env python3
r"""Fetch release metadata through the upbrr workflow API.

PowerShell:
    $env:UPBRR_API_TOKEN = Read-Host "API token"
    python .\scripts\fetch_metadata_api.py "D:\Media\Example.Release.2026.1080p-GRP.mkv"

Or pass the token directly:
    python .\scripts\fetch_metadata_api.py `
        "D:\Media\Example.Release.2026.1080p-GRP.mkv" --token "YOUR_API_TOKEN"

Create a suitable token with ``workflow:read`` and ``workflow:write`` scopes in
Web UI Settings > API Tokens.

The input path must exist on the machine running ``upbrr serve``.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import uuid
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import Request, urlopen


NON_TERMINAL_OPERATION_STATUSES = {"pending", "queued", "ready", "running"}


class APIError(RuntimeError):
    """A safe-to-display API failure."""


def request_json(
    method: str,
    url: str,
    token: str,
    *,
    payload: dict[str, Any] | None = None,
    idempotency_key: str | None = None,
    expected_revision: int | None = None,
) -> dict[str, Any]:
    """Send one JSON request with optional mutation authority headers."""
    headers = {
        "Accept": "application/json",
        "Authorization": f"Bearer {token}",
    }
    data = None
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if idempotency_key is not None:
        headers["Idempotency-Key"] = idempotency_key
    if expected_revision is not None:
        headers["If-Match"] = f'"{expected_revision}"'

    request = Request(url, data=data, headers=headers, method=method)
    try:
        with urlopen(request) as response:
            result = json.load(response)
    except HTTPError as exc:
        try:
            failure = json.load(exc)
            message = failure.get("error") if isinstance(failure, dict) else None
        except (json.JSONDecodeError, UnicodeDecodeError):
            message = None
        raise APIError(f"API returned HTTP {exc.code}: {message or exc.reason}") from None
    except URLError as exc:
        raise APIError(f"API request failed: {exc.reason}") from None

    if not isinstance(result, dict):
        raise APIError("API returned a non-object JSON response")
    return result


def fetch_metadata(
    api_url: str,
    token: str,
    source_path: str,
    poll_interval: float,
    timeout: float,
) -> dict[str, Any]:
    continuations_url = f"{api_url.rstrip('/')}/continuations"
    idempotency_key = str(uuid.uuid4())
    payload: dict[str, Any] = {
        "goal": "prepared",
        "intent": {
            "preparation": {
                "SourcePath": source_path,
                "Intent": "preview",
            }
        },
    }

    current = request_json(
        "POST",
        continuations_url,
        token,
        payload=payload,
        idempotency_key=idempotency_key,
    )
    workflow = current.get("workflow")
    if not isinstance(workflow, dict) or not workflow.get("id") or workflow.get("revision") is None:
        raise APIError("API response omitted workflow authority")

    payload["authority"] = {
        "workflowId": workflow["id"],
        "expectedRevision": workflow["revision"],
    }
    current = request_json(
        "POST",
        continuations_url,
        token,
        payload=payload,
        idempotency_key=idempotency_key,
    )

    operation = current.get("operation")
    deadline = time.monotonic() + timeout
    while isinstance(operation, dict) and operation.get("status") in NON_TERMINAL_OPERATION_STATUSES:
        operation_id = operation.get("id")
        if not operation_id:
            raise APIError("API response omitted operation ID")
        if time.monotonic() >= deadline:
            raise APIError(f"metadata preparation timed out after {timeout:g} seconds")
        time.sleep(poll_interval)
        workflow_id = quote(str(workflow["id"]), safe="")
        encoded_operation_id = quote(str(operation_id), safe="")
        operation = request_json(
            "GET",
            f"{api_url.rstrip('/')}/workflows/{workflow_id}/operations/{encoded_operation_id}",
            token,
        )

    workflow_id = quote(str(workflow["id"]), safe="")
    current = request_json(
        "GET",
        f"{api_url.rstrip('/')}/workflows/{workflow_id}",
        token,
    )
    release = current.get("release")
    if not isinstance(release, dict):
        continuation = current.get("continuation")
        actions = continuation.get("requiredActions", []) if isinstance(continuation, dict) else []
        raise APIError(f"metadata unavailable; required actions: {json.dumps(actions)}")

    canonical_release = release.get("release")
    if not isinstance(canonical_release, dict):
        canonical_release = {}
    return {
        "workflowId": workflow["id"],
        "display": release.get("display"),
        "identity": canonical_release.get("Identity"),
        "providerMetadata": canonical_release.get("ProviderMetadata"),
        "diagnostics": release.get("diagnostics", []),
    }


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
        "--poll-interval",
        type=float,
        default=1.0,
        help="operation poll interval in seconds (default: %(default)s)",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=600.0,
        help="operation timeout in seconds (default: %(default)s)",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    token = args.token or os.environ.get("UPBRR_API_TOKEN")
    if not token:
        print("error: pass --token or set UPBRR_API_TOKEN", file=sys.stderr)
        return 2
    if args.poll_interval <= 0 or args.timeout <= 0:
        print("error: --poll-interval and --timeout must be greater than zero", file=sys.stderr)
        return 2

    try:
        metadata = fetch_metadata(
            args.api_url,
            token,
            args.input_path,
            args.poll_interval,
            args.timeout,
        )
    except APIError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    json.dump(metadata, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
