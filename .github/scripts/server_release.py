"""Validate server release tags and the exact set of publishable assets."""

import argparse
import hashlib
from pathlib import Path
import re
import tarfile
import zipfile


def version(tag):
    match = re.fullmatch(
        r"server/v((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
        r"(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)", tag
    )
    if not match:
        raise ValueError("expected a server/vX.Y.Z tag (optional SemVer prerelease)")
    result = match.group(1)
    if "-" in result:
        for part in result.split("-", 1)[1].split("."):
            if part.isdigit() and len(part) > 1 and part.startswith("0"):
                raise ValueError("numeric prerelease identifiers cannot have leading zeros")
    return result


def archive_names(release_version):
    version("server/v" + release_version)
    return {
        f"go-apple-dm-server_{release_version}_{system}_{arch}.{suffix}"
        for system, suffix in [("linux", "tar.gz"), ("darwin", "tar.gz"), ("windows", "zip")]
        for arch in ["amd64", "arm64"]
    }


def verify(directory, release_version):
    """Return an explicit upload list after checking hashes and archive contents."""
    expected = archive_names(release_version)
    checksum = directory / f"go-apple-dm-server_{release_version}_checksums.txt"
    if checksum.is_symlink() or not checksum.is_file():
        raise ValueError("checksum file must be a regular file")
    found = set()
    for line in checksum.read_text(encoding="ascii").splitlines():
        match = re.fullmatch(r"([a-f0-9]{64})  ([^/\\]+)", line)
        if not match:
            raise ValueError("invalid checksum line")
        digest, name = match.groups()
        if name not in expected or name in found:
            raise ValueError(f"unexpected or duplicate checksum entry: {name}")
        found.add(name)
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"archive must be a regular file: {name}")
        with path.open("rb") as stream:
            if hashlib.file_digest(stream, "sha256").hexdigest() != digest:
                raise ValueError(f"checksum mismatch: {name}")
        windows = name.endswith(".zip")
        suffix = ".exe" if windows else ""
        wanted = {"LICENSE", "server-releases.md", "dmserver" + suffix, "dmctl" + suffix}
        if windows:
            with zipfile.ZipFile(path) as archive:
                members = archive.infolist()
                names = [member.filename for member in members]
                regular = all(
                    not member.is_dir() and member.file_size > 0
                    and (member.external_attr >> 16) & 0o170000 in (0, 0o100000)
                    for member in members
                )
        else:
            with tarfile.open(path, "r:gz") as archive:
                members = archive.getmembers()
                names = [member.name for member in members]
                regular = all(member.isfile() and member.size > 0 for member in members)
        if not regular or len(names) != len(wanted) or set(names) != wanted:
            raise ValueError(f"unexpected archive contents: {name}")
    if found != expected:
        raise ValueError("checksum file must cover all six platform archives")
    return [directory / name for name in sorted(expected)] + [checksum]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    tag_command = commands.add_parser("version")
    tag_command.add_argument("tag")
    verify_command = commands.add_parser("verify")
    verify_command.add_argument("directory", type=Path)
    verify_command.add_argument("release_version")
    args = parser.parse_args()
    try:
        if args.command == "version":
            print(version(args.tag))
        else:
            for path in verify(args.directory, args.release_version):
                print(path)
    except (ValueError, OSError, tarfile.TarError, zipfile.BadZipFile) as error:
        parser.exit(1, f"server release: {error}\n")


if __name__ == "__main__":
    main()
