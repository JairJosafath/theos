#!/usr/bin/env python3
"""Ask the OSV database about every dependency a lockfile pins.

Usage: osvcheck.py [--ignore-file osv-scanner.toml] go.mod|package-lock.json ...

It reads what osv-scanner reads from each file: the modules a go.mod requires
and the Go version it names (as the standard library), and every package of
a package-lock.json. It prints each advisory with the package and version it
is for, and exits 1 when any is left after the ignores of an osv-scanner.toml
([[IgnoredVulns]] id = "...").
"""
import json
import re
import sys
import urllib.request

API = "https://api.osv.dev/v1/querybatch"


def go_mod(path):
    queries = []
    in_require = False
    toolchain = None
    go_version = None
    for raw in open(path, encoding="utf-8"):
        line = raw.split("//")[0].strip()
        if not line:
            continue
        if line.startswith("go "):
            go_version = line.split()[1]
        elif line.startswith("toolchain "):
            toolchain = line.split()[1].removeprefix("go")
        elif line.startswith("require ("):
            in_require = True
        elif in_require and line == ")":
            in_require = False
        elif in_require or line.startswith("require "):
            parts = line.removeprefix("require ").split()
            if len(parts) >= 2:
                queries.append(("Go", parts[0], parts[1].removeprefix("v")))
    stdlib = toolchain or go_version
    if stdlib:
        queries.append(("Go", "stdlib", stdlib))
    return queries


def package_lock(path):
    lock = json.load(open(path, encoding="utf-8"))
    queries = set()
    for where, entry in (lock.get("packages") or {}).items():
        if not where or entry.get("link"):
            continue
        name = entry.get("name") or where.split("node_modules/")[-1]
        version = entry.get("version")
        if version:
            queries.add(("npm", name, version))
    return sorted(queries)


def ignored(path):
    ids = set()
    if path:
        for match in re.finditer(r'^\s*id\s*=\s*"([^"]+)"', open(path, encoding="utf-8").read(), re.M):
            ids.add(match.group(1))
    return ids


def ask(queries):
    found = []
    for start in range(0, len(queries), 500):
        chunk = queries[start : start + 500]
        body = json.dumps(
            {"queries": [{"package": {"ecosystem": e, "name": n}, "version": v} for e, n, v in chunk]}
        ).encode()
        request = urllib.request.Request(API, data=body, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=60) as response:
            results = json.load(response)["results"]
        for query, result in zip(chunk, results):
            for vuln in result.get("vulns") or []:
                found.append((vuln["id"], query))
    return found


def main(argv):
    ignore_file = None
    files = []
    args = iter(argv)
    for arg in args:
        if arg == "--ignore-file":
            ignore_file = next(args)
        else:
            files.append(arg)
    skip = ignored(ignore_file)
    left = 0
    for path in files:
        queries = go_mod(path) if path.endswith("go.mod") else package_lock(path)
        found = ask(queries)
        ids = sorted({vuln for vuln, _ in found})
        kept = [vuln for vuln in ids if vuln not in skip]
        print(f"{path}: {len(queries)} packages, {len(ids)} advisories, {len(ids) - len(kept)} ignored, {len(kept)} left")
        for vuln, (_, name, version) in sorted(found):
            mark = "ignored" if vuln in skip else "LEFT"
            print(f"  {mark:7} {vuln:22} {name} {version}")
        left += len(kept)
    return 1 if left else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
