#!/usr/bin/env python3
"""
Copies useful cache files from Upload-Assistant tmp directory to upbrr tmp directory.

Features:
- Validates file sizes to exclude empty files (0 bytes).
- Renames top-level release directories and files to replace spaces with underscores.
- Skip logic by default to avoid re-copying existing releases.
"""

from __future__ import annotations

import argparse
import os
import pathlib
import re
import shutil
import sys
import time
from typing import Any

# Reconfigure stdout/stderr to use UTF-8 (fixes Windows cp1252 print encoding issues)
if sys.stdout.encoding and sys.stdout.encoding.lower() != 'utf-8':
    try:
        sys.stdout.reconfigure(encoding='utf-8')
        sys.stderr.reconfigure(encoding='utf-8')
    except AttributeError:
        # Fallback for Python versions older than 3.7
        pass

# Initialize ANSI colors on Windows automatically
if os.name == 'nt':
    os.system('')

COLOR_RESET = "\033[0m"
COLOR_CYAN = "\033[96m"
COLOR_GREEN = "\033[92m"
COLOR_YELLOW = "\033[93m"
COLOR_MAGENTA = "\033[95m"
COLOR_RED = "\033[91m"

def print_log(message: str, level: str = "info", quiet: bool = False) -> None:
    if quiet and level == "info":
        return

    if level == "header":
        print(f"\n{COLOR_CYAN}=== {message} ==={COLOR_RESET}")
    elif level == "success":
        print(f"{COLOR_GREEN}[SUCCESS] {message}{COLOR_RESET}")
    elif level == "warning":
        print(f"{COLOR_YELLOW}[WARNING] {message}{COLOR_RESET}")
    elif level == "dryrun":
        print(f"{COLOR_MAGENTA}[DRY-RUN] {message}{COLOR_RESET}")
    elif level == "error":
        print(f"{COLOR_RED}[ERROR]   {message}{COLOR_RESET}", file=sys.stderr)
    else:
        print(f"[INFO]    {message}")

def is_release_dir(path: pathlib.Path) -> bool:
    if path.is_dir():
        try:
            for item in path.iterdir():
                if item.is_file():
                    name_lower = item.name.lower()
                    if name_lower in ["image_data.json", "menu_images.json"] or re.match(r"^Disc\d+_\d+_FULL\.txt$", item.name, re.IGNORECASE):
                        return True
        except Exception:
            pass
    return False

def process_bdinfo_directory(
    src_dir: pathlib.Path,
    dst_dir: pathlib.Path,
    dry_run: bool,
    force: bool,
    quiet: bool,
    stats: dict[str, int],
) -> bool:
    try:
        items = list(src_dir.iterdir())
    except Exception as e:
        print_log(f"Failed to list directory '{src_dir}': {e}", level="warning", quiet=quiet)
        return False

    # Filter to files only
    files = [f for f in items if f.is_file()]

    # Find all Disc*_FULL.txt files
    disc_files: list[dict[str, Any]] = []
    for f in files:
        match = re.match(r"^Disc(\d+)_(\d+)_FULL\.txt$", f.name, re.IGNORECASE)
        if match:
            disc_files.append({
                'filename': f.name,
                'path': f,
                'disc_num': int(match.group(1)),
                'playlist_code': match.group(2)
            })

    has_json = any(f.name.lower() in ["image_data.json", "menu_images.json"] for f in files)
    if not disc_files and not has_json:
        return False

    if not dry_run and not dst_dir.exists():
        try:
            dst_dir.mkdir(parents=True, exist_ok=True)
        except Exception as e:
            print_log(f"Failed to create target directory '{dst_dir}': {e}", level="error")
            return False

    # Copy image_data.json and menu_images.json if present
    for json_name in ["image_data.json", "menu_images.json"]:
        exact_src_file = next((f for f in files if f.name.lower() == json_name.lower()), None)
        if exact_src_file:
            dst_file_path = dst_dir / json_name
            if dst_file_path.exists() and not force:
                continue

            try:
                size = exact_src_file.stat().st_size
            except Exception as e:
                print_log(f"Could not check size of '{exact_src_file}': {e}", level="warning", quiet=quiet)
                continue

            if size == 0:
                stats["empty_files_skipped"] += 1
                print_log(f"Skipped empty file: '{exact_src_file}' (0 bytes)", level="warning", quiet=quiet)
                continue

            stats["files_copied"] += 1
            if dry_run:
                print_log(f"Would copy: '{exact_src_file.name}' -> '{json_name}'", level="dryrun", quiet=quiet)
            else:
                print_log(f"Copying: '{exact_src_file.name}' -> '{json_name}'...", level="info", quiet=quiet)
                try:
                    shutil.copy2(exact_src_file, dst_file_path)
                except Exception as e:
                    print_log(f"Failed to copy file '{exact_src_file}': {e}", level="error", quiet=quiet)

    # Copy and rename matched files for each playlist code found
    for df in disc_files:
        disc_num = df['disc_num']
        playlist_code = df['playlist_code']
        disc_filename = df["filename"]
        YY = f"{disc_num - 1:02d}"

        # Define source and destination file names
        mappings = [
            (disc_filename, f"BD_SUMMARY_FULL_{playlist_code}.MPLS.txt"),
            (f"BD_SUMMARY_{YY}.txt", f"BD_SUMMARY_{playlist_code}.MPLS.txt"),
            (f"BD_SUMMARY_EXT_{YY}.txt", f"BD_SUMMARY_EXT_{playlist_code}.MPLS.txt")
        ]

        for src_name, dst_name in mappings:
            # Match case-insensitively
            exact_src_file = next((f for f in files if f.name.lower() == src_name.lower()), None)
            if not exact_src_file:
                continue

            dst_file_path = dst_dir / dst_name

            # Validate file size
            try:
                size = exact_src_file.stat().st_size
            except Exception as e:
                print_log(f"Could not check size of '{exact_src_file}': {e}", level="warning", quiet=quiet)
                continue

            if size == 0:
                stats['empty_files_skipped'] += 1
                print_log(f"Skipped empty file: '{exact_src_file}' (0 bytes)", level="warning", quiet=quiet)
                continue

            # Check if destination file already exists
            if dst_file_path.exists() and not force:
                stats['files_skipped'] += 1
                continue

            stats['files_copied'] += 1
            if dry_run:
                print_log(f"Would copy and rename: '{exact_src_file.name}' -> '{dst_name}'", level="dryrun", quiet=quiet)
            else:
                print_log(f"Copying and renaming: '{exact_src_file.name}' -> '{dst_name}'...", level="info", quiet=quiet)
                try:
                    shutil.copy2(exact_src_file, dst_file_path)
                except Exception as e:
                    print_log(f"Failed to copy file '{exact_src_file}': {e}", level="error", quiet=quiet)

    return True

def main() -> int:
    # Determine default paths dynamically
    home = pathlib.Path.home()
    default_src = home / "Upload-Assistant" / "tmp"
    if not default_src.exists() and (home / "Upload-Assistant" / "tmp").exists():
        default_src = home / "Upload-Assistant" / "tmp"

    default_dst = home / ".upbrr" / "tmp"

    parser = argparse.ArgumentParser(
        description="Copy and rename BDInfo/Blu-ray folders replacing spaces with underscores, and validating file sizes."
    )
    parser.add_argument(
        "--source", "-s",
        default=str(default_src),
        help=f"Source directory path (default: {default_src})"
    )
    parser.add_argument(
        "--destination", "-d",
        default=str(default_dst),
        help=f"Destination directory path (default: {default_dst})"
    )
    parser.add_argument(
        "--dry-run", "-n",
        action="store_true",
        help="Preview actions without copying files"
    )
    parser.add_argument(
        "--force", "-f",
        action="store_true",
        help="Overwrite existing files/directories in destination"
    )
    parser.add_argument(
        "--quiet", "-q",
        action="store_true",
        help="Suppress informational console output"
    )

    args = parser.parse_args()

    # Resolve absolute paths
    source_path = pathlib.Path(args.source).resolve()
    dest_path = pathlib.Path(args.destination).resolve()

    print_log("Starting Release Copy & Rename Script", level="header", quiet=args.quiet)
    print_log(f"Source Directory      : {source_path}", level="info", quiet=args.quiet)
    print_log(f"Destination Directory : {dest_path}", level="info", quiet=args.quiet)
    print_log("Mode                  : Copy BDInfo and screenshot metadata", level="info", quiet=args.quiet)
    if args.dry_run:
        print_log("Dry Run Enabled       : No changes will be written to disk", level="warning", quiet=args.quiet)
    if args.force:
        print_log("Force Overwrite       : Enabled", level="warning", quiet=args.quiet)

    if not source_path.exists():
        print_log(f"Source directory does not exist: {source_path}", level="error")
        return 1

    if not args.dry_run and not dest_path.exists():
        try:
            dest_path.mkdir(parents=True, exist_ok=True)
            print_log(f"Created destination directory: {dest_path}", level="success", quiet=args.quiet)
        except Exception as e:
            print_log(f"Failed to create destination directory '{dest_path}': {e}", level="error")
            return 1

    try:
        items = list(source_path.iterdir())
    except Exception as e:
        print_log(f"Failed to list source directory '{source_path}': {e}", level="error")
        return 1

    stats = {
        'total_found': len(items),
        'folders_copied': 0,
        'files_copied': 0,
        'files_skipped': 0,
        'empty_files_skipped': 0,
        'filtered_out': 0
    }

    start_time = time.time()

    for item in items:
        # Match check (directories that are release dirs with metadata)
        if not item.is_dir() or not is_release_dir(item):
            stats['filtered_out'] += 1
            continue

        new_name = item.name.replace(" ", "_")
        target_path = dest_path / new_name

        if args.dry_run:
            print_log(f"Would process directory: '{item.name}' -> '{new_name}'", level="dryrun", quiet=args.quiet)
            processed = process_bdinfo_directory(item, target_path, dry_run=True, force=args.force, quiet=args.quiet, stats=stats)
            if processed:
                stats['folders_copied'] += 1
        else:
            print_log(f"Processing directory: '{item.name}' -> '{new_name}'...", level="info", quiet=args.quiet)
            processed = process_bdinfo_directory(item, target_path, dry_run=False, force=args.force, quiet=args.quiet, stats=stats)
            if processed:
                print_log(f"Processed directory: '{item.name}' to '{new_name}'", level="success", quiet=args.quiet)
                stats['folders_copied'] += 1

    duration = time.time() - start_time

    # Summary
    print_log("Execution Summary", level="header", quiet=args.quiet)
    print_log(f"Total items in Source : {stats['total_found']}", level="info", quiet=args.quiet)
    print_log(f"Filtered out          : {stats['filtered_out']}", level="info", quiet=args.quiet)
    print_log(f"Directories Processed : {stats['folders_copied']}", level="info", quiet=args.quiet)
    print_log(f"Files Copied          : {stats['files_copied']}", level="info", quiet=args.quiet)
    print_log(f"Files Skipped (exist) : {stats['files_skipped']}", level="info", quiet=args.quiet)
    print_log(f"Empty Files Skipped   : {stats['empty_files_skipped']}", level="info", quiet=args.quiet)
    print_log(f"Time Elapsed          : {duration:.2f} seconds", level="info", quiet=args.quiet)
    print_log("Execution completed successfully.", level="success", quiet=args.quiet)
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
