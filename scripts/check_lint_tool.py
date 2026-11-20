"""Check the pinned golangci-lint release and its Go build compatibility."""

import json
from pathlib import Path
import re
import subprocess
import sys


if sys.version_info < (3, 11):
    sys.exit("Python 3.11+ is required for make lint")


def go_series(version: str) -> tuple[int, int] | None:
    match = re.match(r"go(\d+)\.(\d+)", version)
    if match is None:
        return None
    return int(match[1]), int(match[2])


def main() -> None:
    try:
        pin = Path(".golangci-lint-version").read_text(encoding="utf-8").strip()
    except OSError as error:
        sys.exit(f"Cannot read the golangci-lint pin: {error}")
    install = f"Install the official golangci-lint {pin} binary and put it on PATH"
    try:
        output = subprocess.check_output(["golangci-lint", "version", "--json"], text=True)
        metadata = json.loads(output)
    except FileNotFoundError:
        sys.exit(install)
    except (subprocess.CalledProcessError, UnicodeDecodeError, json.JSONDecodeError) as error:
        sys.exit(f"Cannot read golangci-lint version metadata. {install}. {error}")

    if not isinstance(metadata, dict):
        sys.exit(f"Invalid golangci-lint version metadata. {install}")
    version = metadata.get("version")
    built_with = metadata.get("goVersion")
    if not isinstance(version, str) or not isinstance(built_with, str):
        sys.exit(f"Invalid golangci-lint version metadata. {install}")
    if f"v{version}" != pin:
        sys.exit(f"Install golangci-lint {pin} (found v{version})")

    lint_series = go_series(built_with)
    if lint_series is None:
        sys.exit(f"Invalid golangci-lint goVersion {built_with!r}. {install}")
    try:
        sdk_go = subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip()
    except FileNotFoundError:
        sys.exit("Go is required for make lint")
    except subprocess.CalledProcessError as error:
        sys.exit(f"go env GOVERSION failed (exit {error.returncode}). Select the development toolchain from go.mod.")
    sdk_series = go_series(sdk_go)
    if sdk_series is None:
        sys.exit(f"Cannot determine the SDK toolchain from {sdk_go!r}")
    if lint_series < sdk_series:
        sys.exit(f"{install}, or build it with {sdk_go} or newer (found {built_with})")


if __name__ == "__main__":
    main()
