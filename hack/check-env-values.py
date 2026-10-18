#!/usr/bin/env python3
"""Keeps environment files thin.

deploy/envs/<env>/<app>.yaml may only set ALLOWED_KEYS; everything else (image, probes, resources,
security context) belongs to the chart release or deploy/apps/<app>.yaml. Credentials must come from
a Secret, never from env values. deploy/envs/<env>/version.yaml holds only a semver version or "".

YAML is converted with yq (local binary, or the pinned container), so only the stdlib is needed here.
"""
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys

YQ_IMAGE = "mikefarah/yq:4.54.1"
ALLOWED_KEYS = {"replicas", "env", "secretEnvFrom", "scenarios"}
SECRET_LIKE = re.compile(r"(SECRET|PASSWORD|TOKEN|ACCESS_ID|PRIVATE_KEY|API_KEY)$")
SEMVER = re.compile(r"^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$")
SKIP = {"namespace.yaml"}


def load(path: pathlib.Path):
    cmd = ["yq", "-o=json", "."] if shutil.which("yq") else ["docker", "run", "--rm", "-i", YQ_IMAGE, "-o=json", "."]
    out = subprocess.run(cmd, stdin=path.open("rb"), capture_output=True, check=True).stdout
    return json.loads(out or b"null") or {}


def main(root: pathlib.Path) -> int:
    errors = []
    apps = {p.stem for p in (root / "apps").glob("*.yaml")}
    for f in sorted((root / "envs").glob("*/*.yaml")):
        if f.name in SKIP:
            continue
        rel = f.relative_to(root)
        doc = load(f)
        if f.name == "version.yaml":
            if set(doc) != {"version"}:
                errors.append(f"{rel}: must contain only 'version'")
            elif doc["version"] not in ("", None) and not SEMVER.match(str(doc["version"])):
                errors.append(f"{rel}: version {doc['version']!r} is not semver")
            continue
        if f.stem not in apps:
            errors.append(f"{rel}: no deploy/apps/{f.name} for this app")
        for key in sorted(set(doc) - ALLOWED_KEYS):
            errors.append(f"{rel}: '{key}' may not be set per environment (allowed: {', '.join(sorted(ALLOWED_KEYS))})")
        for name in sorted((doc.get("env") or {})):
            if SECRET_LIKE.search(name):
                errors.append(f"{rel}: env {name} looks like a credential; use secretEnvFrom")
    for e in errors:
        print(f"::error::{e}" if "GITHUB_ACTIONS" in os.environ else f"error: {e}")
    print(f"env values: {'FAILED' if errors else 'ok'} ({len(errors)} problem(s))")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main(pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else "deploy")))
