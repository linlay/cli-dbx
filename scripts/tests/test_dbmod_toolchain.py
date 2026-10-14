import importlib.util
import json
import os
import pathlib
import tempfile
import unittest
from unittest.mock import patch


ROOT = pathlib.Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("dbmod_toolchain", ROOT / "scripts/prepare-dbmod-toolchain.py")
toolchain = importlib.util.module_from_spec(spec)
spec.loader.exec_module(toolchain)
spec = importlib.util.spec_from_file_location("shared_workspace", ROOT / "scripts/prepare-shared-workspace.py")
workspace = importlib.util.module_from_spec(spec)
spec.loader.exec_module(workspace)


class ToolchainTests(unittest.TestCase):
    def make_sdk(self, directory):
        sdk = pathlib.Path(directory) / "installed"
        (sdk / "src/cmd/internal/sys").mkdir(parents=True)
        (sdk / "src/cmd/internal/sys/source.go").write_text("package sys\n")
        (sdk / "go.mod").write_text("original SDK module, never overwrite\n")
        return sdk

    def prepare(self, sdk, version, output):
        def query(args, **kwargs):
            return str(sdk) if args[-1] == "GOROOT" else version
        with patch.object(toolchain.subprocess, "check_output", side_effect=query):
            toolchain.prepare(output)

    def test_accepts_go_126_patch_releases_and_keeps_exact_sdk_identity(self):
        for version in ("go1.26.6", "go1.26.7", "go1.26.8", "go1.26.10"):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as directory:
                sdk = self.make_sdk(directory)
                output = pathlib.Path(directory) / "private"
                try:
                    self.prepare(sdk, version, output)
                except SystemExit as error:
                    self.fail(f"Go 1.26 patch release rejected: {version}: {error}")
                identity = json.loads((output / ".dbx-toolchain.json").read_text())
                self.assertEqual(identity["version"], version)
                self.assertEqual((sdk / "go.mod").read_text(), "original SDK module, never overwrite\n")
                self.assertIn("go 1.26.6", (output / "go.mod").read_text())
                self.prepare(sdk, version, output)
                with self.assertRaisesRegex(SystemExit, "identity changed"):
                    self.prepare(sdk, "go1.26.11", output)

    def test_rejects_other_minor_versions_and_prerelease_toolchains(self):
        for version in ("go1.25.9", "go1.27.0", "go1.260.1", "go1.26rc1", "devel go1.26", "go1.26.6-custom"):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as directory:
                sdk = self.make_sdk(directory)
                output = pathlib.Path(directory) / "private"
                with self.assertRaisesRegex(SystemExit, "Go 1.26"):
                    self.prepare(sdk, version, output)
                self.assertFalse(output.exists())

    def test_oracle_dependency_minimum_is_not_an_exact_patch_pin(self):
        for version in ("go1.26", "go1.26.0", "go1.26.4", "go1.26.5"):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as directory:
                sdk = self.make_sdk(directory)
                with self.assertRaisesRegex(SystemExit, "Oracle"):
                    self.prepare(sdk, version, pathlib.Path(directory) / "private")

    @unittest.skipIf(os.name == "nt", "POSIX read-only directory modes")
    def test_readonly_cached_sdk_stays_unmodified(self):
        with tempfile.TemporaryDirectory() as directory:
            sdk = self.make_sdk(directory)
            for path in sorted(sdk.rglob("*"), reverse=True):
                path.chmod(0o555 if path.is_dir() else 0o444)
            sdk.chmod(0o555)
            output = pathlib.Path(directory) / "private"
            try:
                self.prepare(sdk, "go1.26.7", output)
            except PermissionError as error:
                self.fail(f"cannot prepare read-only cached SDK: {error}")
            self.assertTrue((output / "src/cmd/objfile/sys/source.go").is_file())
            self.assertEqual((sdk / "go.mod").read_text(), "original SDK module, never overwrite\n")
            self.assertEqual(sdk.stat().st_mode & 0o777, 0o555)

    def test_workspace_uses_dependency_minimum_instead_of_exact_patch_pin(self):
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "go.work"
            workspace.prepare(output)
            self.assertTrue(output.read_text().startswith("go 1.26.6\n"))
