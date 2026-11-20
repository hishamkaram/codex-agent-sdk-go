"""Check the SDK's contributor harness links, skill metadata, roles and Make targets.

The link check covers simple inline Markdown destinations, including optional
titles. External links and heading fragments are outside this check.

The Make check reads literal goals in single-line inline code and shell or
unlabelled fenced code blocks. It handles assignments, flags, common option
values, and simple &&, ||, and ; chains. Alternate Makefiles or directories
and expanded goals are outside scope.
"""

from collections.abc import Iterator
import json
from pathlib import Path, PureWindowsPath
import re
import shlex
import sys
from urllib.parse import unquote

if sys.version_info < (3, 11):
    sys.exit("Python 3.11+ is required for make verify-harness")

import tomllib


HARNESS_DOCUMENTS = (
    "AGENTS.md",
    "README.md",
    "CONTRIBUTING.md",
    "docs/approvals.md",
    "docs/architecture.md",
    "docs/wire-protocol.md",
    ".github/pull_request_template.md",
)
INLINE_LINK = re.compile(
    r"""\]\([ \t]*(<[^>\r\n]+>|[^\s)]+)"""
    r"""(?:[ \t]+(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|\((?:\\.|[^()\\\r\n])*\)))?"""
    r"""[ \t]*\)"""
)
INLINE_CODE = re.compile(r"(?<!`)(`+)([^`\r\n]+)\1(?!`)")
CODE_FENCE = re.compile(r" {0,3}(`{3,}|~{3,})[ \t]*(.*)")
SHELL_FENCE_LANGUAGES = {"", "bash", "sh", "shell", "zsh"}
SHELL_VARIABLE = r"[A-Za-z_][A-Za-z0-9_]*="
SHELL_ASSIGNMENT = re.compile(rf"{SHELL_VARIABLE}.*")
SHELL_PUNCTUATION = "();<>|&"
# Consume whole words so quoted operators and word-internal hashes stay literal.
SHELL_CHUNK = re.compile(
    r"""(?P<chain>&&|\|\||;)|(?P<comment>\#.*)|[ \t]+|"""
    r"""(?:'[^']*'|"(?:\\.|[^"\\])*"|\\.|[^ \t'"\\();<>&|])+|."""
)
MAKE_COMMAND = re.compile(rf"(?:^|&&|\|\||;)[ \t]*(?:{SHELL_VARIABLE}\S*[ \t]+)*make[ \t]+")
MAKE_GOAL = re.compile(r"[\w./-]+")
MAKE_TARGET = re.compile(rf"^({MAKE_GOAL.pattern}):(?!=)", re.MULTILINE)
MAKE_NUMERIC_OPTION = {"-j", "--jobs", "-l", "--load-average", "--max-load"}
MAKE_OPTION_NUMBER = re.compile(r"\d+(?:\.\d+)?")
MAKE_VALUE_OPTION = {
    "-I", "--include-dir", "-E", "--eval", "-o", "--old-file", "--assume-old",
    "-W", "--what-if", "--new-file", "--assume-new",
}
MAKE_OTHER_CONTEXT = {"--directory", "--file", "--makefile"}
ROLE_FIELDS = {"name", "description", "developer_instructions"}


class HarnessError(Exception):
    """An actionable error in the contributor harness."""


def read(root: Path, path: Path) -> str:
    try:
        return (root / path).read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise HarnessError(f"{path}: cannot read: {error}") from error


def local_links(
    root: Path, source: Path, text: str, *, project_relative: bool = False
) -> set[Path]:
    base = root if project_relative else root / source.parent
    links = set()
    for match in INLINE_LINK.finditer(text):
        destination = match[1].removeprefix("<").removesuffix(">")
        if destination.startswith(("https://", "http://", "mailto:", "#")):
            continue
        path_text = unquote(destination.partition("#")[0])
        path = Path(path_text)
        if path.is_absolute() or PureWindowsPath(path_text).drive or "\\" in path_text:
            raise HarnessError(f"{source}: link must be repository-relative: {destination}")
        resolved = (base / path).resolve()
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


def documented_make_snippets(text: str) -> Iterator[str]:
    fence = ""
    in_shell = False
    for line in text.splitlines():
        marker = CODE_FENCE.fullmatch(line)
        if marker:
            delimiter, info = marker.groups()
            info = info.strip()
            if not fence:
                fence = delimiter
                language = info.split(maxsplit=1)[0] if info else ""
                in_shell = language.lower() in SHELL_FENCE_LANGUAGES
                continue
            if delimiter[0] == fence[0] and len(delimiter) >= len(fence) and not info:
                fence = ""
                continue

        if fence:
            snippets = (line,) if in_shell else ()
        else:
            snippets = (match[2] for match in INLINE_CODE.finditer(line))
        for snippet in snippets:
            if MAKE_COMMAND.search(snippet):
                yield snippet


def shell_segments(snippet: str) -> Iterator[str]:
    start = 0
    for chunk in SHELL_CHUNK.finditer(snippet):
        if chunk.lastgroup is None:
            continue
        yield snippet[start:chunk.start()]
        if chunk.lastgroup == "comment":
            return
        start = chunk.end()
    yield snippet[start:]


def shell_words(source: Path, segment: str) -> list[str]:
    lexer = shlex.shlex(segment, posix=True, punctuation_chars=SHELL_PUNCTUATION)
    lexer.whitespace_split = True
    lexer.commenters = ""
    try:
        return list(lexer)
    except ValueError as error:
        raise HarnessError(f"{source}: malformed make command: {error}") from error


def make_goals(command: list[str]) -> list[str]:
    arguments = iter(command)
    program = next(arguments, "")
    while SHELL_ASSIGNMENT.fullmatch(program):
        program = next(arguments, "")
    if program != "make":
        return []

    goals = []
    previous_option = ""
    options = True
    for word in arguments:
        if word and all(character in SHELL_PUNCTUATION for character in word):
            break
        if previous_option in MAKE_VALUE_OPTION or (
            previous_option in MAKE_NUMERIC_OPTION and MAKE_OPTION_NUMBER.fullmatch(word)
        ):
            previous_option = ""
            continue
        previous_option = ""
        if options and word.startswith("-"):
            if word == "--":
                options = False
            elif word.startswith(("-C", "-f")) or word.partition("=")[0] in MAKE_OTHER_CONTEXT:
                return []
            else:
                previous_option = word
        elif MAKE_GOAL.fullmatch(word):
            goals.append(word)
    return goals


def documented_make_goals(source: Path, text: str) -> Iterator[str]:
    for snippet in documented_make_snippets(text):
        for segment in shell_segments(snippet):
            yield from make_goals(shell_words(source, segment))


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
        links = local_links(root, path, text, project_relative=path in roles)
        if path == entry:
            for missing in sorted((skills | roles) - links):
                raise HarnessError(f"{entry}: missing route to {missing}")
        for target in documented_make_goals(path, text):
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
