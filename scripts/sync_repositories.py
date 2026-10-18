#!/usr/bin/env python3
"""Propose bidirectional default-branch synchronization without overwriting refs."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, quote
from urllib.request import Request, urlopen

PERSONAL = "cuijianzhuang"
ORGANIZATION = "PolarisPanelOrg"
REPOSITORIES = ("Polaris", "polaris-license", "polaris-pro", "polaris-plugins")
TOKEN_VARIABLES = {
    PERSONAL: "PERSONAL_REPO_SYNC_TOKEN",
    ORGANIZATION: "ORG_REPO_SYNC_TOKEN",
}


class SyncError(Exception):
    pass


class Synchronizer:
    def __init__(self, tokens, apply=False):
        self.tokens = tokens
        self.apply = apply

    def api(self, owner, path, payload=None):
        request = Request(
            "https://api.github.com" + path,
            data=None if payload is None else json.dumps(payload).encode(),
            headers={
                "Authorization": "Bearer " + self.tokens[owner],
                "Accept": "application/vnd.github+json",
                "Content-Type": "application/json",
                "X-GitHub-Api-Version": "2022-11-28",
                "User-Agent": "PolarisPanelOrg-repository-sync",
            },
        )
        try:
            with urlopen(request, timeout=30) as response:
                return json.load(response)
        except HTTPError as error:
            # Do not print response bodies, headers, or private repository data.
            raise SyncError(f"GitHub HTTP {error.code}: {path.split('?')[0]}") from None
        except (URLError, TimeoutError):
            raise SyncError("GitHub connection failed") from None

    def remote_url(self, owner, repository):
        return f"https://github.com/{owner}/{repository}.git"

    def git(self, directory, *arguments, owner=None, allow_failure=False):
        env = os.environ.copy()
        env.update({
            "GIT_TERMINAL_PROMPT": "0",
            "GIT_ASKPASS": str(Path(directory) / "askpass.py"),
            "POLARIS_SYNC_CURRENT_TOKEN": self.tokens.get(owner, ""),
        })
        result = subprocess.run(
            ["git", "-c", "credential.helper=", "-c", "core.hooksPath=/dev/null",
             "-C", str(directory), *arguments],
            env=env, capture_output=True, text=True, timeout=180,
        )
        if result.returncode and not allow_failure:
            raise SyncError(f"git {arguments[0]} failed; check repository access and branch rules")
        return result

    def validate_metadata(self, repository):
        metadata = {}
        for owner in (PERSONAL, ORGANIZATION):
            item = self.api(owner, f"/repos/{owner}/{repository}")
            # Reject redirects to a transferred or unrelated repository.
            if item["full_name"].lower() != f"{owner}/{repository}".lower():
                raise SyncError("Repository owner/name differs from the configured pair")
            if item.get("archived") or item.get("disabled"):
                raise SyncError("Repository is archived or disabled")
            if not item.get("default_branch"):
                raise SyncError("Repository has no default branch; initialize the copy first")
            if not item.get("permissions", {}).get("push"):
                raise SyncError(f"Missing write access to {owner}/{repository}")
            metadata[owner] = item
        if metadata[PERSONAL]["private"] != metadata[ORGANIZATION]["private"]:
            raise SyncError("Visibility differs; refusing to copy between public and private repositories")
        return metadata

    def sync(self, repository):
        metadata = self.validate_metadata(repository)
        with tempfile.TemporaryDirectory(prefix="polaris-sync-") as directory:
            askpass = Path(directory) / "askpass.py"
            askpass.write_text(
                "#!/usr/bin/env python3\nimport os, sys\n"
                "print('x-access-token' if 'username' in sys.argv[1].lower() "
                "else os.environ['POLARIS_SYNC_CURRENT_TOKEN'])\n"
            )
            askpass.chmod(0o700)
            self.git(directory, "init", "--bare", "--quiet")
            commits = {}
            for owner in (PERSONAL, ORGANIZATION):
                branch = metadata[owner]["default_branch"]
                self.git(directory, "fetch", "--quiet", "--no-tags",
                         self.remote_url(owner, repository), f"refs/heads/{branch}", owner=owner)
                commit = self.git(directory, "rev-parse", "FETCH_HEAD").stdout.strip()
                self.git(directory, "update-ref", f"refs/heads/{owner}", commit)
                commits[owner] = commit
            common = self.git(directory, "merge-base", commits[PERSONAL], commits[ORGANIZATION],
                              allow_failure=True)
            if common.returncode:
                raise SyncError("No shared Git history; create the organization copy from the original")
            self.direction(directory, repository, PERSONAL, ORGANIZATION, metadata, commits)
            self.direction(directory, repository, ORGANIZATION, PERSONAL, metadata, commits)

    def direction(self, directory, repository, source, target, metadata, commits):
        source_commit, target_commit = commits[source], commits[target]
        included = self.git(directory, "merge-base", "--is-ancestor", source_commit,
                            target_commit, allow_failure=True)
        if included.returncode == 0:
            print(f"{repository}: {target} already contains {source}")
            return
        if included.returncode != 1:
            raise SyncError("Cannot determine commit ancestry")
        difference = self.git(directory, "diff", "--quiet", source_commit, target_commit,
                              allow_failure=True)
        if difference.returncode == 0:
            print(f"{repository}: equivalent files; no synchronization PR needed")
            return
        if difference.returncode != 1:
            raise SyncError("Cannot compare repository trees")
        base = metadata[target]["default_branch"]
        # A fixed branch per direction avoids creating duplicate PRs. Only a
        # fast-forward update is allowed; an edited/diverged sync branch fails.
        branch = f"sync/from-{source}"
        if base == branch:
            raise SyncError("Refusing to push to the default branch")
        if not self.apply:
            print(f"{repository}: would propose {source} -> {target}:{base}")
            return
        path = f"/repos/{target}/{repository}"
        query = urlencode({"state": "open", "base": base, "head": f"{target}:{branch}"})
        existing = self.api(target, f"{path}/pulls?{query}")
        # Push objects using the source owner's read token and the destination
        # owner's write token separately. No force, deletions or default writes.
        self.git(directory, "push", "--quiet", self.remote_url(target, repository),
                 f"{source_commit}:refs/heads/{branch}", owner=target)
        if existing:
            print(f"{repository}: updated existing synchronization PR in {target}")
            return
        source_branch = metadata[source]["default_branch"]
        self.api(target, f"{path}/pulls", {
            "title": f"sync: {source}/{repository} -> {target}/{repository}",
            "head": branch,
            "base": base,
            "body": (
                f"Synchronize `{source}/{repository}:{source_branch}` at `{source_commit}` "
                f"into `{target}/{repository}:{base}`.\n\n"
                "This PR preserves commits from both repositories. Review changes and CI "
                "before merging; resolve conflicts explicitly. Automation does not merge "
                "this PR, force-push, delete refs, or publish releases."
            ),
        })
        # Avoid writing private commit or PR details into public Actions logs.
        print(f"{repository}: created synchronization PR in {target}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true", help="push sync branches and open PRs")
    args = parser.parse_args()
    tokens = {owner: os.environ.get(variable, "") for owner, variable in TOKEN_VARIABLES.items()}
    missing = [TOKEN_VARIABLES[owner] for owner, token in tokens.items() if not token]
    if missing:
        print("Missing Actions secrets: " + ", ".join(missing), file=sys.stderr)
        return 1
    synchronizer = Synchronizer(tokens, apply=args.apply)
    failed = False
    for repository in REPOSITORIES:
        try:
            synchronizer.sync(repository)
        except (SyncError, subprocess.TimeoutExpired) as error:
            failed = True
            # TimeoutExpired includes command arguments, so use a fixed message.
            message = "Git operation timed out" if isinstance(error, subprocess.TimeoutExpired) else str(error)
            print(f"{repository}: {message}", file=sys.stderr)
    return int(failed)


if __name__ == "__main__":
    sys.exit(main())
