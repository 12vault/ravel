#!/usr/bin/env python3
"""Synchronize the canonical Ravel skill into marketplace plugin packages."""

from pathlib import Path
import shutil

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


for destination in TARGETS:
    copy_tree(destination)

# Claude discovers plugin-native subagents from the plugin root.
claude_agents = ROOT / "plugins" / "ravel" / "agents"
if claude_agents.exists():
    shutil.rmtree(claude_agents)
shutil.copytree(SOURCE / "agents", claude_agents, ignore=shutil.ignore_patterns("openai.yaml"))
