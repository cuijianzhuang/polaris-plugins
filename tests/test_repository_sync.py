import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location(
    "repository_sync", Path(__file__).resolve().parents[1] / "scripts/sync_repositories.py"
)
sync = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(sync)


def git(path, *args):
    return subprocess.check_output(["git", "-C", str(path), *args], stderr=subprocess.PIPE, text=True).strip()


class LocalSynchronizer(sync.Synchronizer):
    def __init__(self, paths, apply=True):
        super().__init__({sync.PERSONAL: "personal-test-token", sync.ORGANIZATION: "org-test-token"}, apply)
        self.paths = paths
        self.metadata = {
            owner: {"full_name": f"{owner}/Polaris", "default_branch": "main", "private": True,
                    "permissions": {"push": True}}
            for owner in paths
        }
        self.created = []

    def remote_url(self, owner, repository):
        return str(self.paths[owner])

    def api(self, owner, path, payload=None):
        if "/pulls" not in path:
            return self.metadata[owner]
        if payload is None:
            return [body for target, body in self.created if target == owner]
        self.created.append((owner, payload))
        return {"number": len(self.created)}


class RepositorySyncTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.work = root / "work"
        self.work.mkdir()
        git(self.work, "init", "--initial-branch=main")
        git(self.work, "config", "user.name", "Sync tests")
        git(self.work, "config", "user.email", "sync@example.invalid")
        (self.work / "base.txt").write_text("shared initial content\n")
        git(self.work, "add", ".")
        git(self.work, "commit", "-m", "initial")
        self.initial = git(self.work, "rev-parse", "HEAD")
        self.paths = {owner: root / (owner + ".git") for owner in (sync.PERSONAL, sync.ORGANIZATION)}
        for path in self.paths.values():
            git(root, "clone", "--bare", str(self.work), str(path))
        self.runner = LocalSynchronizer(self.paths)

    def commit(self, owner, filename, content="new content\n"):
        git(self.work, "checkout", "-B", "main", self.head(owner))
        (self.work / filename).write_text(content)
        git(self.work, "add", filename)
        git(self.work, "commit", "-m", "change " + filename)
        commit = git(self.work, "rev-parse", "HEAD")
        git(self.work, "push", str(self.paths[owner]), "HEAD:refs/heads/main")
        return commit

    def head(self, owner, branch="main"):
        return git(self.paths[owner], "rev-parse", "refs/heads/" + branch)

    def run_sync(self):
        with contextlib.redirect_stdout(io.StringIO()):
            self.runner.sync("Polaris")

    def assert_no_sync_branch(self, owner):
        self.assertEqual(git(self.paths[owner], "for-each-ref", "--format=%(refname)", "refs/heads/sync/"), "")

    def test_one_direction_preserves_both_default_branches(self):
        commit = self.commit(sync.PERSONAL, "personal.txt")
        self.run_sync()
        self.assertEqual(self.head(sync.PERSONAL), commit)
        self.assertEqual(self.head(sync.ORGANIZATION), self.initial)
        self.assertEqual(self.head(sync.ORGANIZATION, "sync/from-" + sync.PERSONAL), commit)
        self.assertEqual(len(self.runner.created), 1)
        self.assertEqual(self.runner.created[0][0], sync.ORGANIZATION)
        self.assert_no_sync_branch(sync.PERSONAL)

    def test_diverged_histories_propose_two_prs_without_merging(self):
        personal = self.commit(sync.PERSONAL, "personal.txt")
        organization = self.commit(sync.ORGANIZATION, "organization.txt")
        self.run_sync()
        self.assertEqual(self.head(sync.PERSONAL), personal)
        self.assertEqual(self.head(sync.ORGANIZATION), organization)
        self.assertEqual({owner for owner, _ in self.runner.created}, set(self.paths))
        self.assertEqual(self.head(sync.PERSONAL, "sync/from-" + sync.ORGANIZATION), organization)

    def test_repeat_updates_existing_pr_without_duplicates(self):
        self.commit(sync.PERSONAL, "personal.txt")
        self.run_sync()
        latest = self.commit(sync.PERSONAL, "personal-2.txt")
        self.run_sync()
        self.run_sync()
        self.assertEqual(len(self.runner.created), 1)
        self.assertEqual(self.head(sync.ORGANIZATION, "sync/from-" + sync.PERSONAL), latest)

    def test_unchanged_repositories_create_nothing(self):
        self.run_sync()
        self.assertEqual(self.runner.created, [])
        for owner in self.paths:
            self.assert_no_sync_branch(owner)

    def test_merge_only_or_equivalent_trees_do_not_ping_pong(self):
        git(self.work, "commit", "--allow-empty", "-m", "metadata only")
        git(self.work, "push", str(self.paths[sync.PERSONAL]), "HEAD:main")
        self.run_sync()
        self.assertEqual(self.runner.created, [])
        self.assert_no_sync_branch(sync.ORGANIZATION)

    def test_dry_run_creates_neither_branch_nor_pr(self):
        self.commit(sync.PERSONAL, "personal.txt")
        self.runner.apply = False
        self.run_sync()
        self.assertEqual(self.runner.created, [])
        self.assert_no_sync_branch(sync.ORGANIZATION)

    def test_visibility_mismatch_blocks_all_copying(self):
        self.runner.metadata[sync.ORGANIZATION]["private"] = False
        with self.assertRaisesRegex(sync.SyncError, "Visibility differs"):
            self.run_sync()
        self.assert_no_sync_branch(sync.ORGANIZATION)

    def test_redirected_repository_is_rejected(self):
        self.runner.metadata[sync.PERSONAL]["full_name"] = "somebody-else/Polaris"
        with self.assertRaisesRegex(sync.SyncError, "owner/name differs"):
            self.run_sync()

    def test_missing_write_permission_is_rejected(self):
        self.runner.metadata[sync.ORGANIZATION]["permissions"]["push"] = False
        with self.assertRaisesRegex(sync.SyncError, "Missing write access"):
            self.run_sync()

    def test_unrelated_history_is_not_overwritten(self):
        git(self.work, "checkout", "--orphan", "unrelated")
        git(self.work, "commit", "-m", "unrelated root")
        git(self.work, "push", "--force", str(self.paths[sync.ORGANIZATION]), "HEAD:main")
        before = self.head(sync.ORGANIZATION)
        with self.assertRaisesRegex(sync.SyncError, "No shared Git history"):
            self.run_sync()
        self.assertEqual(self.head(sync.ORGANIZATION), before)
        self.assert_no_sync_branch(sync.ORGANIZATION)

    def test_manually_edited_sync_branch_is_never_force_pushed(self):
        first = self.commit(sync.PERSONAL, "personal.txt")
        self.run_sync()
        git(self.work, "checkout", "-B", "manual", first)
        (self.work / "resolution.txt").write_text("manual resolution\n")
        git(self.work, "add", ".")
        git(self.work, "commit", "-m", "manual conflict resolution")
        manual = git(self.work, "rev-parse", "HEAD")
        branch = "sync/from-" + sync.PERSONAL
        git(self.work, "push", str(self.paths[sync.ORGANIZATION]), f"HEAD:{branch}")
        self.commit(sync.PERSONAL, "later.txt")
        with self.assertRaisesRegex(sync.SyncError, "git push failed"):
            self.run_sync()
        self.assertEqual(self.head(sync.ORGANIZATION, branch), manual)
        self.assertEqual(len(self.runner.created), 1)

    def test_missing_tokens_fail_before_network_access(self):
        with patch.dict(sync.os.environ, {}, clear=True), patch.object(sync, "urlopen") as request:
            with patch.object(sync.sys, "argv", ["sync_repositories.py"]):
                with contextlib.redirect_stderr(io.StringIO()) as errors:
                    self.assertEqual(sync.main(), 1)
        request.assert_not_called()
        self.assertIn("PERSONAL_REPO_SYNC_TOKEN", errors.getvalue())


if __name__ == "__main__":
    unittest.main()
