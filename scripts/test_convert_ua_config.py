#!/usr/bin/env python3

import pathlib
import subprocess
import sys
import tempfile
import unittest

from convert_ua_config import load_python_config, parse_simple_yaml


class PythonConfigTests(unittest.TestCase):
    def test_convert_typed_and_untyped_configs(self):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "config.py"
            output = pathlib.Path(directory) / "config.yaml"
            for assignment in ("config", "other = config", "config: dict[str, Any]"):
                with self.subTest(assignment=assignment):
                    source.write_text(
                        f'{assignment} = {{"DEFAULT": {{"screens": 6}}}}\n',
                        encoding="utf-8",
                    )
                    self.assertEqual(load_python_config(source), {"DEFAULT": {"screens": 6}})
                    subprocess.run(
                        [sys.executable, str(pathlib.Path(__file__).with_name("convert_ua_config.py")),
                         str(source), "-o", str(output)],
                        check=True, capture_output=True, text=True,
                    )
                    self.assertEqual(parse_simple_yaml(output)["screenshot_handling"]["screens"], 6)

    def test_annotation_and_other_statements_are_not_executed(self):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "config.py"
            source.write_text(
                'raise RuntimeError("must not execute")\n'
                'config: unknown_annotation() = {"DEFAULT": {}}\n',
                encoding="utf-8",
            )
            self.assertEqual(load_python_config(source), {"DEFAULT": {}})

    def test_unassigned_annotation_does_not_hide_later_assignment(self):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "config.py"
            source.write_text('config: dict\nother: dict = {}\nconfig = {}\n', encoding="utf-8")
            self.assertEqual(load_python_config(source), {})
            source.write_text('config: dict\nother: dict = {}\n', encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "could not find"):
                load_python_config(source)

    def test_reject_non_dictionary_and_non_literal_values(self):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "config.py"
            for assignment in ("config", "config: dict"):
                for value in ("[]", "dict()"):
                    with self.subTest(assignment=assignment, value=value):
                        source.write_text(f"{assignment} = {value}\n", encoding="utf-8")
                        with self.assertRaises(ValueError):
                            load_python_config(source)


if __name__ == "__main__":
    unittest.main()
