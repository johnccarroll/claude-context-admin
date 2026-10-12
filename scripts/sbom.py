#!/usr/bin/env python3
"""Prints the release SBOM (CycloneDX 1.6 JSON): everything that ships inside cca, at the exact
versions that were built. Same sources as notices.sh: the Go modules and Go version recorded in
the built binaries, and the web bundle's npm packages as web/bun.lock resolved them (the runtime
dependencies and everything they pull in; build-only packages are left out).

    scripts/sbom.py 0.1.0 dist/release/0.1.0/stage/*/cca > claude-context-admin-0.1.0.cdx.json
"""
import base64
import json
import os
import re
import subprocess
import sys
import uuid
from datetime import datetime, timezone

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def run(*cmd):
    return subprocess.run(cmd, check=True, capture_output=True, text=True, cwd=ROOT).stdout


def spdx(text):
    """The SPDX id of a license file's text; None when it isn't one of the few we ship."""
    ids = []
    if "Apache License" in text and "Version 2.0" in text:
        ids.append("Apache-2.0")
    if "Permission is hereby granted, free of charge" in text:
        ids.append("MIT")
    if "Permission to use, copy, modify, and/or distribute" in text:
        ids.append("ISC")
    if "Redistribution and use in source and binary forms" in text:
        ids.append("BSD-3-Clause" if "Neither the name" in text or "may be used to endorse" in text else "BSD-2-Clause")
    return " AND ".join(ids) or None


def licensed(component, expr):
    if expr and " " in expr:
        component["licenses"] = [{"expression": expr}]
    elif expr:
        component["licenses"] = [{"license": {"id": expr}}]
    return component


def go_components(binaries):
    modcache = run("go", "env", "GOMODCACHE").strip()
    go, mods = None, {}
    for b in binaries:
        lines = run("go", "version", "-m", b).splitlines()
        go = lines[0].split()[-1]  # "<path>: go1.26.9"
        for line in lines[1:]:
            f = line.split()
            if f and f[0] == "dep":
                mods[f[1]] = f[2]
    out = [licensed({"type": "library", "name": "stdlib", "version": go, "bom-ref": "pkg:golang/stdlib@v" + go[2:],
                     "purl": "pkg:golang/stdlib@v" + go[2:], "description": "The Go standard library and runtime"},
                    "BSD-3-Clause")]
    for path, version in sorted(mods.items()):
        text = ""
        # The module cache escapes capitals as "!x".
        folder = os.path.join(modcache, re.sub(r"[A-Z]", lambda m: "!" + m.group().lower(), path) + "@" + version)
        for name in sorted(os.listdir(folder)) if os.path.isdir(folder) else []:
            if name.upper().startswith("LICENSE"):
                with open(os.path.join(folder, name), encoding="utf-8", errors="replace") as fh:
                    text += fh.read()
        purl = "pkg:golang/" + path + "@" + version
        out.append(licensed({"type": "library", "name": path, "version": version, "bom-ref": purl, "purl": purl}, spdx(text)))
    return out


def web_components():
    with open(os.path.join(ROOT, "web", "bun.lock"), encoding="utf-8") as fh:
        lock = json.loads(re.sub(r",(\s*[}\]])", r"\1", fh.read()))  # bun.lock is JSON with trailing commas
    packages = lock["packages"]
    todo, seen = list(lock["workspaces"][""].get("dependencies", {})), set()
    while todo:
        name = todo.pop()
        if name in seen:
            continue
        seen.add(name)
        meta = packages[name][2]
        todo += list(meta.get("dependencies", {})) + list(meta.get("optionalDependencies", {}))
    out = []
    for name in sorted(seen):
        resolved, integrity = packages[name][0], packages[name][3]
        version = resolved.rsplit("@", 1)[1]
        purl = "pkg:npm/" + name.replace("@", "%40") + "@" + version
        c = {"type": "library", "name": name, "version": version, "bom-ref": purl, "purl": purl}
        algo, _, digest = integrity.partition("-")
        if algo == "sha512":
            c["hashes"] = [{"alg": "SHA-512", "content": base64.b64decode(digest).hex()}]
        pj = os.path.join(ROOT, "web", "node_modules", name, "package.json")
        lic = None
        if os.path.isfile(pj):
            with open(pj, encoding="utf-8") as fh:
                lic = json.load(fh).get("license")
        out.append(licensed(c, lic if isinstance(lic, str) else None))
    return out


def main():
    if len(sys.argv) < 3:
        sys.exit("usage: sbom.py <version> <cca binary>...")
    version = sys.argv[1]
    # The commit's time, so building the same commit twice gives the same file.
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH") or run("git", "log", "-1", "--format=%ct").strip())
    app = "pkg:github/johnccarroll/claude-context-admin@v" + version
    components = go_components(sys.argv[2:]) + web_components()
    # Derived from the commit, like the timestamp. GitHub's attestation action only accepts a
    # CycloneDX file that has a serial number.
    serial = uuid.uuid5(uuid.NAMESPACE_URL, app + "#" + run("git", "rev-parse", "HEAD").strip())
    json.dump({
        "bomFormat": "CycloneDX",
        "specVersion": "1.6",
        "serialNumber": "urn:uuid:" + str(serial),
        "version": 1,
        "metadata": {
            "timestamp": datetime.fromtimestamp(epoch, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "component": licensed({"type": "application", "name": "claude-context-admin", "version": version, "bom-ref": app,
                                   "purl": app}, "MIT"),
        },
        "components": components,
        "dependencies": [{"ref": app, "dependsOn": [c["bom-ref"] for c in components]}],
    }, sys.stdout, indent=2)
    print()


if __name__ == "__main__":
    main()
