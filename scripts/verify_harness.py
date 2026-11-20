"""Check the SDK's contributor harness links, skill metadata, roles and Make targets.

The link and Make checks cover inline Markdown destinations and literal Make
targets in the harness documents, including backticked make commands and recipe
blocks. External links and heading fragments are outside this check.
"""

import json
from pathlib import Path, PureWindowsPath
import re
import sys
from urllib.parse import unquote

if sys.version_info < (3, 11):
    sys.exit("Python 3.11+ is required for make verify-harness")

import tomllib


HARNESS_DOCUMENTS = (
    "AGENTS.md",
    "README.md",
    "CONTRIBUTING.md",
    "docs/architecture.md",
    ".github/pull_request_template.md",
)
INLINE_LINK = re.compile(r"\]\((<[^>\r\n]+>|[^\s)]+)\)")
MAKE_COMMAND = re.compile(
    r"(?:^[ \t]*(?:(?:GOWORK|GOTOOLCHAIN)=\S+[ \t]+)*|`)[ \t]*make[ \t]+([\w-]+)",
    re.MULTILINE,
)
MAKE_TARGET = re.compile(r"^([\w-]+):(?!=)", re.MULTILINE)
ROLE_FIELDS = {"name", "description", "developer_instructions"}


class HarnessError(Exception):
    """An actionable error in the contributor harness."""


def read(root: Path, path: Path) -> str:
    try:
        return (root / path).read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise HarnessError(f"{path}: cannot read: {error}") from error


def local_links(root: Path, source: Path, text: str) -> set[Path]:
    links = set()
    for match in INLINE_LINK.finditer(text):
        destination = match[1].removeprefix("<").removesuffix(">")
        if destination.startswith(("https://", "http://", "mailto:", "#")):
            continue
        path_text = unquote(destination.partition("#")[0])
        path = Path(path_text)
        if path.is_absolute() or PureWindowsPath(path_text).drive or "\\" in path_text:
            raise HarnessError(f"{source}: link must be repository-relative: {destination}")
        resolved = (root / source.parent / path).resolve()
        if not resolved.is_relative_to(root):
            raise HarnessError(f"{source}: link leaves the repository: {destination}")
        if not resolved.exists():
            raise HarnessError(f"{source}: link target does not exist: {destination}")
        links.add(resolved.relative_to(root))
    return links


def read_skill(root: Path, path: Path) -> str:
    text = read(root, path)
    lines = text.splitlines()
    expected_name = f"name: {path.parent.name}"
    if len(lines) < 4 or lines[0] != "---" or lines[3] != "---" or lines[1] != expected_name:
        raise HarnessError(f"{path}: expected frontmatter with {expected_name!r} and a description")
    if not lines[2].startswith("description: "):
        raise HarnessError(f"{path}: description must be a nonblank double-quoted string")
    try:
        description = json.loads(lines[2].removeprefix("description: "))
    except json.JSONDecodeError as error:
        raise HarnessError(f"{path}: description must be a nonblank double-quoted string") from error
    if not isinstance(description, str) or not description.strip():
        raise HarnessError(f"{path}: description must be a nonblank double-quoted string")
    return text


def read_role(root: Path, path: Path) -> str:
    try:
        role = tomllib.loads(read(root, path))
    except tomllib.TOMLDecodeError as error:
        raise HarnessError(f"{path}: invalid TOML: {error}") from error
    if role.keys() != ROLE_FIELDS or any(not isinstance(value, str) or not value.strip() for value in role.values()):
        raise HarnessError(f"{path}: expected only nonblank name, description and developer_instructions fields")
    if role["name"] != path.stem:
        raise HarnessError(f"{path}: expected role name {path.stem!r}")
    return role["developer_instructions"]


def verify(root: Path) -> None:
    if read(root, Path("CLAUDE.md")).strip() != "@AGENTS.md":
        raise HarnessError("CLAUDE.md: expected @AGENTS.md as the shared instructions import")

    skills = {path.relative_to(root) for path in root.glob(".agents/skills/*/SKILL.md")}
    roles = {path.relative_to(root) for path in root.glob(".codex/agents/*.toml")}
    if not skills or not roles:
        raise HarnessError("expected skills under .agents/skills and role files under .codex/agents")
    entry = Path("AGENTS.md")
    targets = set(MAKE_TARGET.findall(read(root, Path("Makefile"))))
    documents = {Path(path): read(root, Path(path)) for path in HARNESS_DOCUMENTS}
    documents.update((path, read_skill(root, path)) for path in sorted(skills))
    documents.update((path, read_role(root, path)) for path in sorted(roles))
    for path, text in documents.items():
        links = local_links(root, path, text)
        if path == entry:
            for missing in sorted((skills | roles) - links):
                raise HarnessError(f"{entry}: missing route to {missing}")
        for target in MAKE_COMMAND.findall(text):
            if target not in targets:
                raise HarnessError(f"{path}: make {target} does not exist")


if __name__ == "__main__":
    if len(sys.argv) > 2:
        sys.exit("usage: verify_harness.py [repository-root]")
    root = Path(sys.argv[1]).resolve() if len(sys.argv) == 2 else Path(__file__).resolve().parents[1]
    try:
        verify(root)
    except HarnessError as error:
        sys.exit(str(error))
    print("Contributor harness checks passed")
