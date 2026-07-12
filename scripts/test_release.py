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
grammar_tags = (root / "scripts/grammar_tags.txt").read_text().strip()
grammar_tag_list = grammar_tags.split(",")
if not grammar_tags or grammar_tag_list[0] != "grammar_subset":
    raise SystemExit("scripts/grammar_tags.txt must select embedded subset mode")
if any(not re.fullmatch(r"grammar_subset(?:_[a-z0-9_]+)?", tag) for tag in grammar_tag_list):
    raise SystemExit("scripts/grammar_tags.txt contains an invalid grammar build tag")
if len(grammar_tag_list) != len(set(grammar_tag_list)):
    raise SystemExit("scripts/grammar_tags.txt contains duplicate grammar build tags")
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
skill_text = (source / "skill.md").read_text()
for required_instruction in (
    "run `ravel update <target>` once before reading or querying the graph",
    "Do not start `ravel watch`, install Git hooks, or leave background processes running automatically",
    "ask before the initial `ravel build <target>`",
):
    if required_instruction not in skill_text:
        raise SystemExit(f"canonical skill omits graph-refresh policy: {required_instruction}")
expected_platforms = {
    "ravel_darwin_amd64": ("darwin", "amd64"),
    "ravel_darwin_arm64": ("darwin", "arm64"),
    "ravel_linux_amd64": ("linux", "amd64"),
    "ravel_linux_arm64": ("linux", "arm64"),
    "ravel_windows_amd64.exe": ("windows", "amd64"),
    "ravel_windows_arm64.exe": ("windows", "arm64"),
}
expected = set(expected_platforms)


def assert_tree_equal(source_dir: Path, target_dir: Path) -> None:
    source_entries = {path.relative_to(source_dir) for path in source_dir.rglob("*")}
    target_entries = {path.relative_to(target_dir) for path in target_dir.rglob("*")}
    if source_entries != target_entries:
        raise SystemExit(f"{target_dir}: package entries differ from {source_dir}")
    for relative in sorted(source_entries):
        source_path = source_dir / relative
        target_path = target_dir / relative
        if source_path.is_symlink() or target_path.is_symlink():
            raise SystemExit(f"{target_path}: symlinks are not allowed in packaged skill trees")
        if source_path.is_dir() != target_path.is_dir():
            raise SystemExit(f"{target_path}: package entry type differs")
        if source_path.is_file():
            if not filecmp.cmp(source_path, target_path, shallow=False):
                raise SystemExit(f"{target_path}: package file differs")
            if (source_path.stat().st_mode & 0o111) != (target_path.stat().st_mode & 0o111):
                raise SystemExit(f"{target_path}: executable mode differs")


for target in targets:
    if (target / "SKILL.md").read_bytes() != (source / "skill.md").read_bytes():
        raise SystemExit(f"{target}: stale SKILL.md")
    for directory in ("references", "agents", "scripts"):
        assert_tree_equal(source / directory, target / directory)
    if (target / "THIRD_PARTY_NOTICES.md").read_bytes() != (source / "THIRD_PARTY_NOTICES.md").read_bytes():
        raise SystemExit(f"{target}: stale THIRD_PARTY_NOTICES.md")
    if (target / "VERSION").read_bytes() != (source / "VERSION").read_bytes():
        raise SystemExit(f"{target}: stale VERSION")
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
        for field in (f"GOOS={system}", f"GOARCH={arch}", "CGO_ENABLED=0", f"-tags={grammar_tags}"):
            if field not in info.stdout:
                raise SystemExit(f"{binary}: missing build setting {field}")
        # sync-packages builds with both the source default and a linker -X
        # value. Requiring both occurrences catches an accidentally different
        # linker value, which a single raw-string presence check cannot detect.
        if binary.read_bytes().count(f"v{version}".encode()) < 2:
            raise SystemExit(f"{binary}: embedded CLI/linker version is not v{version}")

codex_target = root / ".codex/skills/ravel"
if (codex_target / "SKILL.md").read_bytes() != (source / "skill.md").read_bytes():
    raise SystemExit(f"{codex_target}: stale SKILL.md")
for directory in ("references", "agents", "scripts"):
    assert_tree_equal(source / directory, codex_target / directory)
if (codex_target / "THIRD_PARTY_NOTICES.md").read_bytes() != (source / "THIRD_PARTY_NOTICES.md").read_bytes():
    raise SystemExit(f"{codex_target}: stale THIRD_PARTY_NOTICES.md")
if (codex_target / "VERSION").read_bytes() != (source / "VERSION").read_bytes():
    raise SystemExit(f"{codex_target}: stale VERSION")

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
    for command in ("context", "affected", "mcp", "benchmark", "update-check"):
        result = subprocess.run([str(native), command, "--help"], check=False, capture_output=True, text=True)
        if result.returncode != 0 or "Usage:" not in result.stdout:
            raise SystemExit(f"native binary {command} help smoke test failed: {result.stdout}{result.stderr}")
        if command == "benchmark" and "--answers" not in result.stdout:
            raise SystemExit(f"native binary benchmark help omits --answers: {result.stdout}")
        if command == "update-check" and "--json" not in result.stdout:
            raise SystemExit(f"native binary update-check help omits --json: {result.stdout}")
    if os.name != "nt":
        for launcher in (source / "scripts" / "ravel.sh", root / ".codex/skills/ravel/scripts/ravel.sh"):
            launcher_result = subprocess.run([str(launcher), "version"], check=False, capture_output=True, text=True)
            if launcher_result.returncode != 0 or launcher_result.stdout != f"ravel v{version}\n":
                raise SystemExit(f"source-checkout launcher fallback failed for {launcher}: {launcher_result.stdout}{launcher_result.stderr}")

        # A stale global CLI must not shadow the bundled skill binary. A newer
        # global CLI may be selected, and neither case performs a network call.
        import tempfile
        with tempfile.TemporaryDirectory(prefix="ravel-launcher-test-") as temporary:
            fake = Path(temporary) / "ravel"
            fake.write_text("#!/bin/sh\nprintf 'ravel v%s\\n' \"${FAKE_RAVEL_VERSION}\"\n")
            fake.chmod(0o755)
            launcher = source / "scripts" / "ravel.sh"
            environment = os.environ | {"PATH": f"{temporary}{os.pathsep}{os.environ.get('PATH', '')}"}

            old = subprocess.run(
                [str(launcher), "version"],
                check=False,
                capture_output=True,
                text=True,
                env=environment | {"FAKE_RAVEL_VERSION": "0.1.1"},
            )
            if old.returncode != 0 or old.stdout != f"ravel v{version}\n":
                raise SystemExit(f"launcher did not replace stale global CLI: {old.stdout}{old.stderr}")
            expected_notice = f"Your global Ravel is v0.1.1; this skill requires v{version}."
            if expected_notice not in old.stderr or f"Using the bundled v{version} binary" not in old.stderr:
                raise SystemExit(f"launcher omitted stale-version notice: {old.stderr}")

            equal = subprocess.run(
                [str(launcher), "update-check", "--help"],
                check=False,
                capture_output=True,
                text=True,
                env=environment | {"FAKE_RAVEL_VERSION": version},
            )
            if equal.returncode != 0 or "Usage: ravel update-check" not in equal.stdout:
                raise SystemExit(f"launcher did not prefer its paired bundle for an equal version: {equal.stdout}{equal.stderr}")

            newer = subprocess.run(
                [str(launcher), "version"],
                check=False,
                capture_output=True,
                text=True,
                env=environment | {"FAKE_RAVEL_VERSION": "9.0.0"},
            )
            if newer.returncode != 0 or newer.stdout != "ravel v9.0.0\n" or newer.stderr:
                raise SystemExit(f"launcher did not accept newer global CLI: {newer.stdout}{newer.stderr}")

comparison = root / "benchmarks/compare_graphify.py"
if os.name != "nt" and comparison.stat().st_mode & 0o111 == 0:
    raise SystemExit(f"comparison adapter is not executable: {comparison}")
comparison_help = subprocess.run([sys.executable, str(comparison), "--help"], check=False, capture_output=True, text=True)
if comparison_help.returncode != 0 or "--graphify-graph" not in comparison_help.stdout:
    raise SystemExit(f"Graphify comparison adapter help smoke test failed: {comparison_help.stdout}{comparison_help.stderr}")
print(f"Release versions synchronized at {version}")
