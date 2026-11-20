"""Exercise the contributor harness checker on temporary repository fixtures."""

from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


CHECKER = Path(__file__).with_name("verify_harness.py")
SKILL = ".agents/skills/sdk-test-writer/SKILL.md"
ROLE = ".codex/agents/reviewer.toml"
ENTRY = f"[Testing]({SKILL})\n[Reviewer]({ROLE})\nRun `make verify-harness`.\n"
VALID_FILES = {
    "AGENTS.md": ENTRY,
    "CLAUDE.md": "@AGENTS.md\n",
    "README.md": (
        '[Architecture](docs/architecture.md "Architecture map")\n'
        "[Contributing](CONTRIBUTING.md 'Contributor guide')\n"
        "[Approvals](docs/approvals.md (Approval decisions))\n"
    ),
    "CONTRIBUTING.md": "```bash\nGOWORK=off make verify-harness\n```\n",
    "docs/approvals.md": "[Protocol](wire-protocol.md#approvals)\n",
    "docs/architecture.md": "[Manifest](../go.mod)\n",
    "docs/wire-protocol.md": "[Usage transcript](../internal/events/testdata/transcript.jsonl)\n",
    "internal/events/testdata/transcript.jsonl": "{}\n",
    ".github/pull_request_template.md": "[Contributor guidance](../CONTRIBUTING.md)\n",
    "go.mod": "module example.com/sdk\n",
    "Makefile": "verify-harness:\n\t@true\nhelp:\n\t@true\n",
    SKILL: '---\nname: sdk-test-writer\ndescription: "Write SDK tests."\n---\n',
    ROLE: 'name = "reviewer"\ndescription = "Review patches."\n'
    'developer_instructions = "Read [the entry point](AGENTS.md)."\n',
}


class HarnessTest(unittest.TestCase):
    def check(self, root: Path) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, "-B", str(CHECKER), str(root)],
            capture_output=True,
            text=True,
            check=False,
        )

    def test_rejects_broken_harness_and_accepts_corrections(self):
        cases = (
            ("Claude import", "CLAUDE.md", "@missing.md\n", "expected @AGENTS.md"),
            ("Missing doc", "README.md", "[Architecture](docs/missing.md)\n", "link target does not exist"),
            (
                "Missing link with double-quoted title",
                "README.md",
                '[Architecture](docs/missing.md "Guide")\n',
                "link target does not exist",
            ),
            (
                "Missing link with single-quoted title",
                "README.md",
                "[Architecture](docs/missing.md 'Guide')\n",
                "link target does not exist",
            ),
            (
                "Missing link with parenthesized title",
                "README.md",
                "[Architecture](docs/missing.md (Guide))\n",
                "link target does not exist",
            ),
            (
                "Missing protocol evidence",
                "docs/wire-protocol.md",
                VALID_FILES["docs/wire-protocol.md"].replace("transcript.jsonl", "missing.jsonl"),
                "docs/wire-protocol.md: link target does not exist",
            ),
            ("Absolute path", "README.md", "[Private plan](/private/plan.md)\n", "repository-relative"),
            ("Parent checkout", "README.md", "[Sibling SDK](../sdk)\n", "leaves the repository"),
            ("Missing skill route", "AGENTS.md", f"[Reviewer]({ROLE})\n", f"missing route to {SKILL}"),
            ("Wrong skill name", SKILL, VALID_FILES[SKILL].replace("sdk-test-writer", "other"), "expected frontmatter"),
            ("Invalid description", SKILL, VALID_FILES[SKILL].replace('"Write SDK tests."', "[]"), "description must be"),
            ("Missing role route", "AGENTS.md", f"[Testing]({SKILL})\n", f"missing route to {ROLE}"),
            ("Invalid role TOML", ROLE, "name = [\n", "invalid TOML"),
            ("Invalid role field", ROLE, VALID_FILES[ROLE] + "model = 123\n", "expected only nonblank"),
            ("Invalid role name", ROLE, VALID_FILES[ROLE].replace('"reviewer"', '"researcher"'), "expected role name"),
            (
                "Invalid role link",
                ROLE,
                VALID_FILES[ROLE].replace("AGENTS.md", "missing.md"),
                f"{ROLE}: link target does not exist",
            ),
            ("Unknown Make target", "AGENTS.md", ENTRY.replace("verify-harness", "missing-target"), "make missing-target"),
            (
                "Unknown second Make goal",
                "AGENTS.md",
                ENTRY.replace("make verify-harness", "make verify-harness missing-target"),
                "make missing-target",
            ),
            (
                "Unknown fenced Make goal after option and assignment",
                "CONTRIBUTING.md",
                "```bash\nGOWORK=off make -j 2 verify-harness GOWORK=off missing-target\n```\n",
                "make missing-target",
            ),
            (
                "Unknown inline env-prefixed Make goal",
                "AGENTS.md",
                ENTRY.replace("make verify-harness", "GOWORK=off GOTOOLCHAIN=auto make verify-harness missing-target"),
                "make missing-target",
            ),
            ("Make variable is not a target", "Makefile", "verify-harness:=true\n", "make verify-harness does not exist"),
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for relative, content in VALID_FILES.items():
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content, encoding="utf-8")

            valid = self.check(root)
            self.assertEqual(valid.returncode, 0, valid.stderr)
            for name, relative, content, diagnostic in cases:
                with self.subTest(name=name):
                    path = root / relative
                    path.write_text(content, encoding="utf-8")
                    broken = self.check(root)
                    path.write_text(VALID_FILES[relative], encoding="utf-8")
                    self.assertNotEqual(broken.returncode, 0)
                    self.assertIn(diagnostic, broken.stderr)
                    corrected = self.check(root)
                    self.assertEqual(corrected.returncode, 0, corrected.stderr)

            valid_commands = (
                "make -j2 verify-harness help",
                "make --jobs=2 --no-print-directory verify-harness help",
                "GOWORK=off make -j 2 verify-harness help",
                "make verify-harness help && echo done",
            )
            for command in valid_commands:
                with self.subTest(command=command):
                    (root / "AGENTS.md").write_text(ENTRY.replace("make verify-harness", command), encoding="utf-8")
                    valid = self.check(root)
                    self.assertEqual(valid.returncode, 0, valid.stderr)


if __name__ == "__main__":
    unittest.main()
