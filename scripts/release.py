#!/usr/bin/env python3
"""Synchronize and verify Ravel release versions using only the standard library."""

import argparse
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def normalized(value: str) -> str:
    value = value.removeprefix("v")
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", value):
        raise SystemExit(f"invalid semantic version: {value}")
    return value


def json_version(path: Path, version: str, marketplace: bool = False) -> None:
    value = json.loads(path.read_text())
    value["version"] = version
    if marketplace:
        for plugin in value.get("plugins", []):
            if plugin.get("name") == "ravel":
                plugin["version"] = version
    path.write_text(json.dumps(value, indent=2) + "\n")


def current_versions() -> dict[str, str]:
    source = (ROOT / "internal/cli/commands.go").read_text()
    match = re.search(r'var Version = "v([^"]+)"', source)
    if not match:
        raise SystemExit("cannot find CLI version")
    return {
        "cli": match.group(1),
        "skill": (ROOT / "skills/ravel/VERSION").read_text().strip().removeprefix("v"),
        "claude-marketplace": json.loads((ROOT / ".claude-plugin/marketplace.json").read_text())["version"],
        "claude-plugin": json.loads((ROOT / "plugins/ravel/.claude-plugin/plugin.json").read_text())["version"],
        "codex-plugin": json.loads((ROOT / ".agents/plugins/plugins/ravel/.codex-plugin/plugin.json").read_text())["version"].split("+", 1)[0],
    }


parser = argparse.ArgumentParser()
parser.add_argument("version")
parser.add_argument("--check", action="store_true")
args = parser.parse_args()
version = normalized(args.version)

if args.check:
    mismatches = {name: value for name, value in current_versions().items() if value != version}
    if mismatches:
        raise SystemExit(f"version mismatch: expected {version}, found {mismatches}")
    print(f"All release versions are {version}")
    raise SystemExit(0)

commands = ROOT / "internal/cli/commands.go"
commands.write_text(re.sub(r'var Version = "v[^"]+"', f'var Version = "v{version}"', commands.read_text(), count=1))
(ROOT / "skills/ravel/VERSION").write_text(version + "\n")
json_version(ROOT / ".claude-plugin/marketplace.json", version, marketplace=True)
json_version(ROOT / "plugins/ravel/.claude-plugin/plugin.json", version)
json_version(ROOT / ".agents/plugins/plugins/ravel/.codex-plugin/plugin.json", version)
subprocess.run(["python3", str(ROOT / "scripts/sync-packages.py")], check=True)
print(f"Prepared Ravel v{version}")
