# Releasing Fanisi

Release only a clean commit on `main` that has passed the Linux/macOS CI checks
and independent review. Publishing a release does not replace an installed binary.
The initial supported archives are macOS/Linux on arm64 and amd64. Packaging uses
Go and Python 3 standard libraries; it adds no runtime dependency to Fanisi.

1. Check out the reviewed main commit in an isolated worktree. Follow the host's
   shared-build lease and load rules before cross-compilation.
2. Build a new external artifact directory:

   ```sh
   python3 scripts/package-release.py 0.1.0 /tmp/fanisi-0.1.0-assets
   ```

   The script refuses dirty source and existing output directories. Each archive
   contains `fanisi` and `BUILD-INFO.json` with the source commit, toolchain, target
   and version. Archives have fixed metadata; `SHA256SUMS` identifies the bytes.
   The Go build also retains VCS metadata, inspectable with `go version -m`.
3. Extract the native archive to a new temporary directory. From the same source
   commit, run:

   ```sh
   python3 scripts/release-smoke.py /tmp/fanisi-release-test/fanisi --expect-version 0.1.0
   ```

   The smoke uses only synthetic JSON and temporary Git repositories. It checks
   failed-attempt spend, duplicate receipts/imports, overlapping coordinator
   observations, nullable coverage, separate tooling investment, reviewed landing,
   content mismatch, equivalent unmerged content and revocation. It makes no
   inference or provider requests. CI runs this smoke on Linux and macOS too.
4. After gates pass, create the version tag at that exact reviewed commit and
   publish a GitHub release with the four archives and `SHA256SUMS`. Record the
   source SHA and validation evidence in the release notes. Never move a published
   tag or replace assets with different bytes; use a new version for corrections.
5. Download the published native archive and checksums into a fresh directory,
   verify its SHA-256 against `SHA256SUMS`, extract it there, and rerun the smoke.
   Record the released version and asset digest. This downloaded-artifact check,
   rather than a local build alone, closes distributed-binary verification.

Do not infer a published release from a successful source merge or a local binary.
Cross-compiled targets are build-verified; execution claims require running that
architecture. No project-wide or model savings verdict follows from this smoke.
