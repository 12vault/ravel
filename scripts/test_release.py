#!/usr/bin/env python3
import json
from pathlib import Path
import re
import sys
import filecmp

root = Path(__file__).resolve().parents[1]
source = (root / "internal/cli/commands.go").read_text()
version = re.search(r'var Version = "v([^"]+)"', source).group(1)
paths = [
    root / ".claude-plugin/marketplace.json",
    root / "plugins/ravel/.claude-plugin/plugin.json",
    root / ".agents/plugins/plugins/ravel/.codex-plugin/plugin.json",
]
for path in paths:
    actual = json.loads(path.read_text())["version"].split("+", 1)[0]
    if actual != version:
        print(f"{path}: {actual} != {version}", file=sys.stderr)
        raise SystemExit(1)

source = root / "skills/ravel"
for target in [root / "plugins/ravel/skills/ravel", root / ".agents/plugins/plugins/ravel/skills/ravel"]:
    if (target / "SKILL.md").read_bytes() != (source / "skill.md").read_bytes():
        raise SystemExit(f"{target}: stale SKILL.md")
    for directory in ("references", "agents", "scripts"):
        comparison = filecmp.dircmp(source / directory, target / directory)
        if comparison.left_only or comparison.right_only or comparison.diff_files or comparison.funny_files:
            raise SystemExit(f"{target / directory}: package drift")
    expected = {
        "ravel_darwin_amd64", "ravel_darwin_arm64",
        "ravel_linux_amd64", "ravel_linux_arm64",
        "ravel_windows_amd64.exe", "ravel_windows_arm64.exe",
    }
    actual = {path.name for path in (target / "bin").iterdir() if path.is_file()}
    if actual != expected:
        raise SystemExit(f"{target / 'bin'}: expected {sorted(expected)}, found {sorted(actual)}")

left = root / "plugins/ravel/skills/ravel/bin"
right = root / ".agents/plugins/plugins/ravel/skills/ravel/bin"
for name in expected:
    if not filecmp.cmp(left / name, right / name, shallow=False):
        raise SystemExit(f"bundled binary differs: {name}")
print(f"Release versions synchronized at {version}")
