#!/usr/bin/env python3
"""Synchronize the canonical Ravel skill into marketplace plugin packages."""

from pathlib import Path
import shutil
import os
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "skills" / "ravel"
TARGETS = [
    ROOT / "plugins" / "ravel" / "skills" / "ravel",
    ROOT / ".agents" / "plugins" / "plugins" / "ravel" / "skills" / "ravel",
]


def copy_tree(target: Path) -> None:
    target.mkdir(parents=True, exist_ok=True)
    for name in ("references", "agents", "scripts"):
        destination = target / name
        if destination.exists():
            shutil.rmtree(destination)
        shutil.copytree(SOURCE / name, destination)
    shutil.copy2(SOURCE / "skill.md", target / "SKILL.md")


def build_binaries(destination: Path, cache: Path) -> None:
    destination.mkdir()
    source = (ROOT / "internal" / "cli" / "commands.go").read_text()
    match = re.search(r'var Version = "([^"]+)"', source)
    if not match:
        raise SystemExit("cannot find CLI version")
    version = match.group(1)
    for system, arch, suffix in (
        ("darwin", "amd64", ""),
        ("darwin", "arm64", ""),
        ("linux", "amd64", ""),
        ("linux", "arm64", ""),
        ("windows", "amd64", ".exe"),
        ("windows", "arm64", ".exe"),
    ):
        output = destination / f"ravel_{system}_{arch}{suffix}"
        env = os.environ | {
            "GOOS": system,
            "GOARCH": arch,
            "CGO_ENABLED": "0",
            "GOCACHE": str(cache),
        }
        subprocess.run(
            ["go", "build", "-buildvcs=false", "-trimpath", "-ldflags", f"-s -w -X github.com/12ya/reporavel/internal/cli.Version={version}", "-o", output, "./cmd/ravel"],
            cwd=ROOT,
            env=env,
            check=True,
        )
        if sys.platform == "darwin" and system == "darwin":
            subprocess.run(
                ["codesign", "--force", "--sign", "-", output],
                check=True,
            )
        output.chmod(0o755)


with tempfile.TemporaryDirectory(prefix="ravel-go-cache-") as cache:
    binaries = Path(cache) / "bin"
    build_binaries(binaries, Path(cache))
    for destination in TARGETS:
        copy_tree(destination)
        packaged_binaries = destination / "bin"
        if packaged_binaries.exists():
            shutil.rmtree(packaged_binaries)
        shutil.copytree(binaries, packaged_binaries)

# The repository-local Codex skill mirrors the canonical instructions but uses
# the checkout's launcher instead of carrying release binaries.
copy_tree(ROOT / ".codex" / "skills" / "ravel")

# Claude discovers plugin-native subagents from the plugin root.
claude_agents = ROOT / "plugins" / "ravel" / "agents"
if claude_agents.exists():
    shutil.rmtree(claude_agents)
shutil.copytree(SOURCE / "agents", claude_agents, ignore=shutil.ignore_patterns("openai.yaml"))
