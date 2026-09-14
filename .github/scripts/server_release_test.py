"""Exercise tag rejection and the actual archive/upload boundary."""

import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

import server_release as release


class ServerReleaseTests(unittest.TestCase):
    def test_tags(self):
        for tag, want in [("server/v0.9.1", "0.9.1"), ("server/v1.0.0-rc.1", "1.0.0-rc.1")]:
            self.assertEqual(release.version(tag), want)
        for tag in ["v0.9.1", "server-v0.9.1", "main", "server/v01.0.0", "server/v1.0.0-01",
                    "server/v1.0.0-rc..1", "server/v1.0.0+build", "server/v1.0.0\n", "server/v1.0.0/../../x",
                    "server/v$(echo test)", "--help", "server/v1.0.0-"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                release.version(tag)

    def fixture(self, root, extra=None, symlink=False):
        lines = []
        for name in sorted(release.archive_names("0.9.1")):
            path = root / name
            windows = name.endswith(".zip")
            suffix = ".exe" if windows else ""
            members = ["LICENSE", "server-releases.md", "dmserver" + suffix, "dmctl" + suffix]
            if extra:
                members.append(extra)
            if windows:
                with zipfile.ZipFile(path, "w") as archive:
                    for member in members:
                        archive.writestr(member, b"fixture")
            else:
                with tarfile.open(path, "w:gz") as archive:
                    for member in members:
                        info = tarfile.TarInfo(member)
                        info.size = len(b"fixture")
                        if symlink and member == "dmserver":
                            info.type = tarfile.SYMTYPE
                            info.linkname = "/private/secret"
                        archive.addfile(info, io.BytesIO(b"fixture"))
            lines.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {name}\n")
        checksum = root / "go-apple-dm-server_0.9.1_checksums.txt"
        checksum.write_text("".join(lines))
        return checksum

    def test_complete_assets_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.fixture(root)
            # GoReleaser metadata is not an uploadable asset, nor are stray files.
            (root / "config.yaml").write_text("private configuration")
            assets = release.verify(root, "0.9.1")
            self.assertEqual(len(assets), 7)
            self.assertNotIn(root / "config.yaml", assets)

    def test_checksum_failures(self):
        for failure in ["missing", "duplicate", "traversal", "corrupt", "symlink", "invalid"]:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                checksum = self.fixture(root)
                lines = checksum.read_text().splitlines(keepends=True)
                archive = root / sorted(release.archive_names("0.9.1"))[0]
                if failure == "missing":
                    checksum.write_text("".join(lines[1:]))
                elif failure == "duplicate":
                    checksum.write_text("".join(lines + lines[:1]))
                elif failure == "traversal":
                    checksum.write_text("a" * 64 + "  ../secret\n")
                elif failure == "invalid":
                    checksum.write_text("not a checksum\n")
                elif failure == "corrupt":
                    archive.write_bytes(b"corruption")
                else:
                    moved = archive.rename(root / "elsewhere")
                    archive.symlink_to(moved)
                with self.assertRaises(ValueError):
                    release.verify(root, "0.9.1")

    def test_unsafe_archive_contents(self):
        for extra, symlink in [("private.key", False), ("../escape", False), ("dmserver", False), (None, True)]:
            with self.subTest(extra=extra, symlink=symlink), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                self.fixture(root, extra=extra, symlink=symlink)
                with self.assertRaises(ValueError):
                    release.verify(root, "0.9.1")


if __name__ == "__main__":
    unittest.main()
