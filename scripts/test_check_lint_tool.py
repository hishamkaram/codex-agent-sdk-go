"""Exercise the linter preflight through CLI metadata from a shell shim."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


PREFLIGHT = Path(__file__).with_name("check_lint_tool.py")


class LintPreflightTest(unittest.TestCase):
    def test_rejects_bad_linter_and_accepts_compatible_shim(self):
        compatible_metadata = json.dumps({"version": "2.12.2", "goVersion": "go1.26.2"})
        cases = (
            ("not JSON", "Cannot read golangci-lint version metadata"),
            (json.dumps({"version": "2.12.2"}), "Invalid golangci-lint version metadata"),
            (json.dumps({"version": "2.12.1", "goVersion": "go1.26.2"}), "found v2.12.1"),
            (json.dumps({"version": "2.12.2", "goVersion": "unknown"}), "Invalid golangci-lint goVersion"),
            (json.dumps({"version": "2.12.2", "goVersion": "go1.25.13"}), "build it with go1.26.11 or newer"),
            (compatible_metadata, ""),
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / ".golangci-lint-version").write_text("v2.12.2\n", encoding="utf-8")
            go = root / "go"
            go.write_text('#!/bin/sh\ntest "$*" = "env GOVERSION" || exit 1\nprintf "go1.26.11\\n"\n', encoding="utf-8")
            go.chmod(0o755)

            missing = self.run_preflight(root, "")
            self.assertNotEqual(missing.returncode, 0)
            self.assertIn("Install the official golangci-lint v2.12.2", missing.stderr)
            lint = root / "golangci-lint"
            lint.write_text(
                '#!/bin/sh\ntest "$*" = "version --json" || exit 1\nprintf "%s\\n" "$LINT_METADATA"\n',
                encoding="utf-8",
            )
            lint.chmod(0o755)
            for metadata, diagnostic in cases:
                with self.subTest(metadata=metadata):
                    result = self.run_preflight(root, metadata)
                    if diagnostic:
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn(diagnostic, result.stderr)
                    else:
                        self.assertEqual(result.returncode, 0, result.stderr)

            go.write_text("#!/bin/sh\nexit 42\n", encoding="utf-8")
            failed_go = self.run_preflight(root, compatible_metadata)
            self.assertNotEqual(failed_go.returncode, 0)
            self.assertIn("go env GOVERSION failed (exit 42)", failed_go.stderr)

            go.unlink()
            missing_go = self.run_preflight(root, compatible_metadata)
            self.assertNotEqual(missing_go.returncode, 0)
            self.assertIn("Go is required for make lint", missing_go.stderr)

    def run_preflight(self, root: Path, metadata: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, "-B", str(PREFLIGHT)],
            cwd=root,
            env={"PATH": str(root), "LINT_METADATA": metadata},
            capture_output=True,
            text=True,
            check=False,
        )


if __name__ == "__main__":
    unittest.main()
