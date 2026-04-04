#!/usr/bin/env python3
"""
Convert a legacy Upload Assistant `config.py` style config into the current
upbrr YAML config shape.

The converter intentionally ignores legacy keys that do not exist in the new
config model.
"""

from __future__ import annotations

import argparse
import ast
import copy
import pathlib
import sys
from typing import Any


ROOT = pathlib.Path(__file__).resolve().parents[1]
TEMPLATE_PATH = ROOT / "internal" / "config" / "defaults" / "example.yaml"


SECTION_ORDER = [
    "main_settings",
    "image_hosting",
    "metadata",
    "screenshot_handling",
    "description_settings",
    "client_setup",
    "arr_integration",
    "torrent_creation",
    "post_upload",
    "logging",
    "trackers",
    "torrent_clients",
]


LEGACY_DEFAULT_SECTION_BY_KEY = {
    "update_notification": "main_settings",
    "verbose_notification": "main_settings",
    "tmdb_api": "main_settings",
    "tracker_pass_checks": "main_settings",
    "db_path": "main_settings",
    "img_host_1": "image_hosting",
    "img_host_2": "image_hosting",
    "img_host_3": "image_hosting",
    "img_host_4": "image_hosting",
    "img_host_5": "image_hosting",
    "img_host_6": "image_hosting",
    "imgbb_api": "image_hosting",
    "ptpimg_api": "image_hosting",
    "lensdump_api": "image_hosting",
    "ptscreens_api": "image_hosting",
    "onlyimage_api": "image_hosting",
    "dalexni_api": "image_hosting",
    "passtheima_ge_api": "image_hosting",
    "zipline_url": "image_hosting",
    "zipline_api_key": "image_hosting",
    "seedpool_cdn_api": "image_hosting",
    "sharex_url": "image_hosting",
    "sharex_api_key": "image_hosting",
    "utppm_api": "image_hosting",
    "btn_api": "metadata",
    "skip_auto_torrent": "metadata",
    "use_largest_playlist": "metadata",
    "keep_images": "metadata",
    "only_id": "metadata",
    "user_overrides": "metadata",
    "ping_unit3d": "metadata",
    "get_bluray_info": "metadata",
    "bluray_score": "metadata",
    "bluray_single_score": "metadata",
    "check_predb": "metadata",
    "screens": "screenshot_handling",
    "min_successful_image_uploads": "screenshot_handling",
    "cutoff_screens": "screenshot_handling",
    "frame_overlay": "screenshot_handling",
    "overlay_text_size": "screenshot_handling",
    "process_limit": "screenshot_handling",
    "max_concurrent_uploads": "screenshot_handling",
    "ffmpeg_limit": "screenshot_handling",
    "tone_map": "screenshot_handling",
    "use_libplacebo": "screenshot_handling",
    "ffmpeg_compression": "screenshot_handling",
    "algorithm": "screenshot_handling",
    "desat": "screenshot_handling",
    "add_logo": "description_settings",
    "logo_size": "description_settings",
    "logo_language": "description_settings",
    "thumbnail_size": "description_settings",
    "screens_per_row": "description_settings",
    "episode_overview": "description_settings",
    "tonemapped_header": "description_settings",
    "multiScreens": "description_settings",
    "pack_thumb_size": "description_settings",
    "charLimit": "description_settings",
    "fileLimit": "description_settings",
    "processLimit": "description_settings",
    "custom_description_header": "description_settings",
    "screenshot_header": "description_settings",
    "disc_menu_header": "description_settings",
    "custom_signature": "description_settings",
    "add_bluray_link": "description_settings",
    "use_bluray_images": "description_settings",
    "bluray_image_size": "description_settings",
    "default_torrent_client": "client_setup",
    "injecting_client_list": "client_setup",
    "searching_client_list": "client_setup",
    "use_sonarr": "arr_integration",
    "sonarr_url": "arr_integration",
    "sonarr_api_key": "arr_integration",
    "sonarr_url_1": "arr_integration",
    "sonarr_api_key_1": "arr_integration",
    "sonarr_url_2": "arr_integration",
    "sonarr_api_key_2": "arr_integration",
    "sonarr_url_3": "arr_integration",
    "sonarr_api_key_3": "arr_integration",
    "use_radarr": "arr_integration",
    "radarr_url": "arr_integration",
    "radarr_api_key": "arr_integration",
    "radarr_url_1": "arr_integration",
    "radarr_api_key_1": "arr_integration",
    "radarr_url_2": "arr_integration",
    "radarr_api_key_2": "arr_integration",
    "radarr_url_3": "arr_integration",
    "radarr_api_key_3": "arr_integration",
    "emby_dir": "arr_integration",
    "emby_tv_dir": "arr_integration",
    "mkbrr_threads": "torrent_creation",
    "prefer_max_16_torrent": "torrent_creation",
    "rehash_cooldown": "torrent_creation",
    "inject_delay": "post_upload",
    "show_upload_duration": "post_upload",
    "print_tracker_messages": "post_upload",
    "print_tracker_links": "post_upload",
    "search_requests": "post_upload",
    "cross_seeding": "post_upload",
    "cross_seed_check_everything": "post_upload",
}


TORRENT_CLIENT_KEY_ALIASES = {
    "rtorrent_url": "url",
    "rtorrent_label": "category",
    "VERIFY_WEBUI_CERTIFICATE": "verify_webui_certificate",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Convert a legacy Upload Assistant config.py into upbrr YAML."
    )
    parser.add_argument("input", help="Path to the legacy config.py-style file")
    parser.add_argument(
        "-o",
        "--output",
        help="Path to write the converted YAML config",
        default="config.converted.yaml",
    )
    return parser.parse_args()


def load_python_config(path: pathlib.Path) -> dict[str, Any]:
    module = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    for node in module.body:
        if isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name) and target.id == "config":
                    value = ast.literal_eval(node.value)
                    if not isinstance(value, dict):
                        raise ValueError("legacy config variable is not a dictionary")
                    return value
    raise ValueError("could not find `config = {...}` in input file")


def parse_scalar(text: str) -> Any:
    stripped = text.strip()
    if stripped == "":
        return ""
    if stripped == "[]":
        return []
    if stripped == "{}":
        return {}
    if (stripped.startswith('"') and stripped.endswith('"')) or (
        stripped.startswith("'") and stripped.endswith("'")
    ):
        return stripped[1:-1]
    lowered = stripped.lower()
    if lowered == "true":
        return True
    if lowered == "false":
        return False
    if lowered == "null" or lowered == "none":
        return None
    if lowered.startswith("-") and lowered[1:].isdigit():
        return int(lowered)
    if lowered.isdigit():
        return int(lowered)
    try:
        if any(ch in stripped for ch in [".", "e", "E"]):
            return float(stripped)
    except ValueError:
        pass
    return stripped


def next_significant_line(lines: list[str], start_index: int) -> str | None:
    for index in range(start_index, len(lines)):
        candidate = lines[index]
        stripped = candidate.lstrip(" ")
        if not stripped or stripped.startswith("#"):
            continue
        return candidate.rstrip()
    return None


def parse_simple_yaml(path: pathlib.Path) -> dict[str, Any]:
    root: dict[str, Any] = {}
    stack: list[tuple[int, Any]] = [(-1, root)]
    lines = path.read_text(encoding="utf-8").splitlines()

    for index, raw_line in enumerate(lines):
        if not raw_line.strip():
            continue
        line = raw_line.rstrip()
        stripped = line.lstrip(" ")
        if stripped.startswith("#"):
            continue
        indent = len(line) - len(stripped)

        while stack and indent <= stack[-1][0]:
            stack.pop()

        parent = stack[-1][1]

        if stripped.startswith("- "):
            if not isinstance(parent, list):
                raise ValueError(f"unexpected list item in template: {raw_line}")
            parent.append(parse_scalar(stripped[2:]))
            continue

        if ":" not in stripped:
            raise ValueError(f"unsupported YAML syntax in template: {raw_line}")

        key, value = stripped.split(":", 1)
        key = key.strip()
        value = value.strip()

        if value == "":
            container: Any
            upcoming = next_significant_line(lines, index + 1)
            if upcoming is not None:
                next_stripped = upcoming.lstrip(" ")
                next_indent = len(upcoming) - len(next_stripped)
                if next_indent > indent and next_stripped.startswith("- "):
                    container = []
                else:
                    container = {}
            else:
                container = {}
            if not isinstance(parent, dict):
                raise ValueError(f"unexpected mapping entry in template: {raw_line}")
            parent[key] = container
            stack.append((indent, container))
            continue

        if not isinstance(parent, dict):
            raise ValueError(f"unexpected scalar entry in template: {raw_line}")
        parent[key] = parse_scalar(value)

    return root


def coerce_value(value: Any, template: Any) -> Any:
    if isinstance(template, bool):
        if isinstance(value, bool):
            return value
        if isinstance(value, str):
            lowered = value.strip().lower()
            if lowered in {"true", "1", "yes", "on"}:
                return True
            if lowered in {"false", "0", "no", "off", ""}:
                return False
        return bool(value)

    if isinstance(template, int) and not isinstance(template, bool):
        if isinstance(value, int) and not isinstance(value, bool):
            return value
        if isinstance(value, float):
            return int(value)
        if isinstance(value, str):
            stripped = value.strip()
            if stripped == "":
                return template
            return int(float(stripped))
        return int(value)

    if isinstance(template, float):
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            return float(value)
        if isinstance(value, str):
            stripped = value.strip()
            if stripped == "":
                return template
            return float(stripped)
        return float(value)

    if isinstance(template, list):
        if isinstance(value, list):
            return value
        if isinstance(value, tuple):
            return list(value)
        if isinstance(value, str):
            stripped = value.strip()
            if stripped == "":
                return []
            return [part.strip() for part in stripped.split(",") if part.strip()]
        return [value]

    if value is None:
        return ""

    return str(value) if isinstance(template, str) else value


def migrate_defaults(legacy_defaults: dict[str, Any], template: dict[str, Any], out: dict[str, Any]) -> None:
    for key, value in legacy_defaults.items():
        section = LEGACY_DEFAULT_SECTION_BY_KEY.get(key)
        if not section:
            continue
        section_template = template.get(section, {})
        if key not in section_template:
            continue
        out[section][key] = coerce_value(value, section_template[key])


def migrate_trackers(legacy_trackers: dict[str, Any], template: dict[str, Any], out: dict[str, Any]) -> None:
    tracker_template = template["trackers"]
    out_trackers = out["trackers"]

    default_trackers = legacy_trackers.get("default_trackers")
    if "default_trackers" in tracker_template and default_trackers is not None:
        out_trackers["default_trackers"] = coerce_value(
            default_trackers,
            tracker_template["default_trackers"],
        )

    for tracker_name, tracker_values in legacy_trackers.items():
        if tracker_name == "default_trackers":
            continue
        if not isinstance(tracker_values, dict):
            continue
        if tracker_name not in tracker_template:
            continue

        new_tracker = out_trackers.setdefault(tracker_name, copy.deepcopy(tracker_template[tracker_name]))
        allowed = tracker_template[tracker_name]
        for key, value in tracker_values.items():
            if key not in allowed:
                continue
            new_tracker[key] = coerce_value(value, allowed[key])


def allowed_torrent_client_keys() -> dict[str, Any]:
    return {
        "type": "",
        "torrent_client": "",
        "url": "",
        "qui_proxy_url": "",
        "watch_folder": "",
        "torrent_storage_dir": "",
        "username": "",
        "password": "",
        "category": "",
        "tags": [],
        "tls_skip_verify": False,
        "qbit_url": "",
        "qbit_port": 0,
        "qbit_user": "",
        "qbit_pass": "",
        "qbit_cat": "",
        "qbit_tag": "",
        "qbit_tags": [],
        "verify_webui_certificate": True,
    }


def migrate_torrent_clients(legacy_clients: dict[str, Any], out: dict[str, Any]) -> None:
    allowed = allowed_torrent_client_keys()

    for client_name, client_values in legacy_clients.items():
        if not isinstance(client_values, dict):
            continue

        new_client: dict[str, Any] = {}
        for key, value in client_values.items():
            mapped_key = TORRENT_CLIENT_KEY_ALIASES.get(key, key)
            if mapped_key not in allowed:
                continue
            new_client[mapped_key] = coerce_value(value, allowed[mapped_key])

        if new_client:
            out["torrent_clients"][client_name] = new_client


def yaml_quote(value: str) -> str:
    escaped = value.replace("\\", "\\\\").replace('"', '\\"')
    return f'"{escaped}"'


def format_scalar(value: Any) -> str:
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return '""'
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return str(value)
    return yaml_quote(str(value))


def dump_yaml(value: Any, indent: int = 0) -> list[str]:
    prefix = " " * indent
    lines: list[str] = []

    if isinstance(value, dict):
        for key in SECTION_ORDER:
            if indent == 0 and key in value:
                lines.append(f"{key}:")
                lines.extend(dump_yaml(value[key], indent + 2))

        for key, item in value.items():
            if indent == 0 and key in SECTION_ORDER:
                continue
            if isinstance(item, dict):
                lines.append(f"{prefix}{key}:")
                lines.extend(dump_yaml(item, indent + 2))
            elif isinstance(item, list):
                if not item:
                    lines.append(f"{prefix}{key}: []")
                else:
                    lines.append(f"{prefix}{key}:")
                    for entry in item:
                        lines.append(f"{prefix}  - {format_scalar(entry)}")
            else:
                lines.append(f"{prefix}{key}: {format_scalar(item)}")
        return lines

    raise TypeError("dump_yaml expects dictionaries at the root")


def build_output(template: dict[str, Any]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key in SECTION_ORDER:
        if key in template:
            out[key] = copy.deepcopy(template[key])
    return out


def main() -> int:
    args = parse_args()
    input_path = pathlib.Path(args.input).resolve()
    output_path = pathlib.Path(args.output).resolve()

    if not input_path.exists():
        print(f"error: input file not found: {input_path}", file=sys.stderr)
        return 1
    if not TEMPLATE_PATH.exists():
        print(f"error: template file not found: {TEMPLATE_PATH}", file=sys.stderr)
        return 1

    legacy = load_python_config(input_path)
    template = parse_simple_yaml(TEMPLATE_PATH)
    out = build_output(template)

    migrate_defaults(legacy.get("DEFAULT", {}), template, out)
    migrate_trackers(legacy.get("TRACKERS", {}), template, out)
    migrate_torrent_clients(legacy.get("TORRENT_CLIENTS", {}), out)

    yaml_text = "\n".join(dump_yaml(out)) + "\n"
    output_path.write_text(yaml_text, encoding="utf-8")

    print(f"Converted legacy config: {input_path}")
    print(f"Wrote new config YAML: {output_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
