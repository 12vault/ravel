#!/usr/bin/env python3
import json
from pathlib import Path
import re
import sys
import filecmp
import os
import platform
import subprocess

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
targets = [root / "plugins/ravel/skills/ravel", root / ".agents/plugins/plugins/ravel/skills/ravel"]
expected_platforms = {
    "ravel_darwin_amd64": ("darwin", "amd64"),
    "ravel_darwin_arm64": ("darwin", "arm64"),
    "ravel_linux_amd64": ("linux", "amd64"),
    "ravel_linux_arm64": ("linux", "arm64"),
    "ravel_windows_amd64.exe": ("windows", "amd64"),
    "ravel_windows_arm64.exe": ("windows", "arm64"),
}
expected = set(expected_platforms)
for target in targets:
    if (target / "SKILL.md").read_bytes() != (source / "skill.md").read_bytes():
        raise SystemExit(f"{target}: stale SKILL.md")
    for directory in ("references", "agents", "scripts"):
        comparison = filecmp.dircmp(source / directory, target / directory)
        if comparison.left_only or comparison.right_only or comparison.diff_files or comparison.funny_files:
            raise SystemExit(f"{target / directory}: package drift")
    actual = {path.name for path in (target / "bin").iterdir() if path.is_file()}
    if actual != expected:
        raise SystemExit(f"{target / 'bin'}: expected {sorted(expected)}, found {sorted(actual)}")
    for name, (system, arch) in expected_platforms.items():
        binary = target / "bin" / name
        if binary.stat().st_size == 0:
            raise SystemExit(f"empty bundled binary: {binary}")
        if os.name != "nt" and binary.stat().st_mode & 0o111 == 0:
            raise SystemExit(f"bundled binary is not executable: {binary}")
        info = subprocess.run(
            ["go", "version", "-m", str(binary)],
            check=False,
            capture_output=True,
            text=True,
        )
        if info.returncode != 0:
            raise SystemExit(f"cannot inspect {binary}: {info.stderr.strip()}")
        for field in (f"GOOS={system}", f"GOARCH={arch}", "CGO_ENABLED=0"):
            if field not in info.stdout:
                raise SystemExit(f"{binary}: missing build setting {field}")
        if f"v{version}".encode() not in binary.read_bytes():
            raise SystemExit(f"{binary}: embedded CLI version is not v{version}")

codex_target = root / ".codex/skills/ravel"
if (codex_target / "SKILL.md").read_bytes() != (source / "skill.md").read_bytes():
    raise SystemExit(f"{codex_target}: stale SKILL.md")
for directory in ("references", "agents", "scripts"):
    comparison = filecmp.dircmp(source / directory, codex_target / directory)
    if comparison.left_only or comparison.right_only or comparison.diff_files or comparison.funny_files:
        raise SystemExit(f"{codex_target / directory}: package drift")

left = root / "plugins/ravel/skills/ravel/bin"
right = root / ".agents/plugins/plugins/ravel/skills/ravel/bin"
for name in expected:
    if not filecmp.cmp(left / name, right / name, shallow=False):
        raise SystemExit(f"bundled binary differs: {name}")

native_system = platform.system().lower()
native_arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
native_suffix = ".exe" if native_system == "windows" else ""
native = left / f"ravel_{native_system}_{native_arch}{native_suffix}" if native_arch else None
if native is not None and native.exists():
    version_result = subprocess.run([str(native), "version"], check=False, capture_output=True, text=True)
    if version_result.returncode != 0 or version_result.stdout != f"ravel v{version}\n":
        raise SystemExit(f"native binary version smoke test failed: {version_result.stdout}{version_result.stderr}")
    for command in ("context", "affected", "mcp", "benchmark"):
        result = subprocess.run([str(native), command, "--help"], check=False, capture_output=True, text=True)
        if result.returncode != 0 or "Usage:" not in result.stdout:
            raise SystemExit(f"native binary {command} help smoke test failed: {result.stdout}{result.stderr}")
        if command == "benchmark" and "--answers" not in result.stdout:
            raise SystemExit(f"native binary benchmark help omits --answers: {result.stdout}")
print(f"Release versions synchronized at {version}")
