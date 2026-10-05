"""Decides whether a check has anything new to check on this push.

On a pull request GitHub matches a workflow's `paths` against the whole pull
request, so once it touches code every later push reruns the check, even one
that only edits docs. This compares the pull request's head with the last
commit the workflow passed on for its branch instead, through the workflow's
own `paths`, and writes changed=false when no file the check reads differs.
Anything else (a push to main, a first run, a rebase, an error) writes
changed=true.
"""

import json
import os
import re
import subprocess
from urllib.parse import quote


def regex(glob):
    """A `paths` glob as a regular expression: `**/` spans zero or more
    directories, `**` anything, `*` and `?` stay within one."""
    out, i = "", 0
    while i < len(glob):
        if glob.startswith("**/", i):
            out, i = out + "(?:.*/)?", i + 3
        elif glob.startswith("**", i):
            out, i = out + ".*", i + 2
        elif glob[i] == "*":
            out, i = out + "[^/]*", i + 1
        elif glob[i] == "?":
            out, i = out + "[^/]", i + 1
        else:
            out, i = out + re.escape(glob[i]), i + 1
    return re.compile(out + r"\Z")


def reads(path, patterns):
    """Whether `paths` takes in path; as in GitHub, the last match decides."""
    taken = False
    for pattern in patterns:
        if regex(pattern.lstrip("!")).match(path):
            taken = not pattern.startswith("!")
    return taken


def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def changed():
    """Whether to run the check, and why."""
    if os.environ["GITHUB_EVENT_NAME"] != "pull_request":
        return True, "not a pull request"
    workflow = os.environ["GITHUB_WORKFLOW_REF"].split("@")[0].split("/", 2)[2]
    patterns = json.loads(run(
        "yq", "-o=json", 'explode(.) | .on.pull_request.paths // ["**"]',
        os.path.join(os.environ["GITHUB_WORKSPACE"], workflow)))
    repo = os.environ["GITHUB_REPOSITORY"]
    passed = json.loads(run(
        "gh", "api",
        f"repos/{repo}/actions/workflows/{os.path.basename(workflow)}/runs"
        f"?event=pull_request&status=success&per_page=1"
        f"&branch={quote(os.environ['HEAD_REF'], safe='')}"))["workflow_runs"]
    if not passed:
        return True, "it has not passed on this branch yet"
    base, head = passed[0]["head_sha"], os.environ["HEAD_SHA"]
    diff = json.loads(run("gh", "api", f"repos/{repo}/compare/{base}...{head}"))
    if diff["status"] not in ("ahead", "identical"):
        return True, f"{head[:7]} does not build on {base[:7]}, the last commit it passed on"
    files = diff.get("files", [])
    if len(files) >= 300:
        return True, f"too many files changed since {base[:7]} to tell"
    paths = {f["filename"] for f in files}
    paths |= {f["previous_filename"] for f in files if "previous_filename" in f}
    hits = sorted(p for p in paths if reads(p, patterns))
    if hits:
        return True, f"{hits[0]} changed since {base[:7]}, the last commit it passed on"
    return False, f"nothing it reads changed since {base[:7]}, the last commit it passed on"


def main():
    try:
        go, why = changed()
    except Exception as err:  # running a check is never wrong; skipping one can be
        go, why = True, "could not tell what changed"
        detail = " ".join(str(getattr(err, "stderr", "") or err).split())
        print(f"::warning title=Changed::Running the check: could not tell what changed: {detail}")
    print(f"::notice title=Changed::{'Running' if go else 'Skipping'} the check: {why}")
    with open(os.environ["GITHUB_OUTPUT"], "a") as out:
        out.write(f"changed={str(go).lower()}\n")


if __name__ == "__main__":
    main()
