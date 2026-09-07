#!/usr/bin/env python3
"""Build deterministic macOS/Linux archives from a clean, reviewed Git commit."""
import argparse
import gzip
import hashlib
import io
import json
import os
import pathlib
import re
import subprocess
import tarfile
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", help="release version, e.g. 0.1.0 (without v)")
    parser.add_argument("output", type=pathlib.Path, help="new directory outside the source checkout")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("version must be three decimal components")
    source = pathlib.Path(__file__).resolve().parent.parent
    output = args.output.resolve()
    if output == source or source in output.parents:
        parser.error("output must be outside the source checkout")
    if subprocess.check_output(["git", "status", "--porcelain"], cwd=source):
        parser.error("release source must be clean and committed")
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip()
    toolchain = subprocess.check_output(["go", "version"], text=True).strip()
    output.mkdir(parents=True, exist_ok=False)
    checksums = []
    for target_os, arch in (("darwin", "arm64"), ("darwin", "amd64"), ("linux", "arm64"), ("linux", "amd64")):
        with tempfile.TemporaryDirectory(prefix="fanisi-package-") as temporary:
            binary = pathlib.Path(temporary) / "fanisi"
            env = dict(os.environ, GOOS=target_os, GOARCH=arch, CGO_ENABLED="0")
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=true", "-ldflags",
                            f"-s -w -X main.version={args.version}", "-o", str(binary), "."],
                           cwd=source, env=env, check=True, timeout=300)
            info = {"version": args.version, "source_commit": commit, "toolchain": toolchain,
                    "goos": target_os, "goarch": arch, "cgo_enabled": False}
            archive = output / f"fanisi_{args.version}_{target_os}_{arch}.tar.gz"
            with archive.open("wb") as raw:
                with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
                    with tarfile.open(fileobj=compressed, mode="w") as package:
                        for name, data, mode in (("fanisi", binary.read_bytes(), 0o755),
                                                 ("BUILD-INFO.json", (json.dumps(info, sort_keys=True) + "\n").encode(), 0o644)):
                            entry = tarfile.TarInfo(name)
                            entry.size, entry.mode, entry.mtime = len(data), mode, 0
                            package.addfile(entry, io.BytesIO(data))
            checksums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
    (output / "SHA256SUMS").write_text("".join(sorted(checksums)))
    print(json.dumps({"version": args.version, "source_commit": commit, "archives": len(checksums)}))


if __name__ == "__main__":
    main()
