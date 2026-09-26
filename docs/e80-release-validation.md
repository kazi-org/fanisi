# E80 distributed release verification

On 2026-09-07, [Fanisi v0.1.0](https://github.com/kazi-org/fanisi/releases/tag/v0.1.0) was published from reviewed source
`c1a02c215f53b4b8f6301dfc5ae45d4ebc9fee68` (PRs #5 and #6).
The main and tag CI runs passed on Linux and macOS, including race tests, vet,
formatting, native build, dry-run and the offline accounting smoke:
[main CI](https://github.com/kazi-org/fanisi/actions/runs/34119206481),
[tag CI](https://github.com/kazi-org/fanisi/actions/runs/34119478570).
Local full race validation executed 90 passing cases with zero failures.

Four macOS/Linux arm64/amd64 archives were built twice from the tagged commit
with Go 1.27.1 on macOS arm64. Both builds produced identical archive checksums.
Every archive contains `BUILD-INFO.json` identifying version, source, toolchain
and target. The binary retains `vcs.modified=false` and the exact source SHA.
Go's embedded main-module version and the CLI both identify v0.1.0; final builds
ran after tag creation because Go derives module version metadata from Git tags.

The four published archives and `SHA256SUMS` were downloaded into a fresh
isolated directory. All four downloaded SHA-256 digests matched. The native
`fanisi_0.1.0_darwin_arm64.tar.gz` archive digest was:

```text
76fb8b018c0e9952d4eb9a8e754d5429f00c58161d18cbd288f47f52caf6d458
```

Its extracted executable reported `fanisi 0.1.0` and passed all 18 checks in
`scripts/release-smoke.py` from the tagged source. The smoke checks synthetic
failed/repair spend, deduplication, overlapping coordinator observations,
unknown prices and request timing, separate tooling cost, reviewed landing,
changed content, equivalent unmerged content and revocation. Expected synthetic
variable-cost lower bound was $0.85; separately reported tooling was $7. Total
cost remained unknown. These are fixture values, not a production savings claim.

The downloaded macOS arm64 executable was run. Other target archives were
cross-compiled and checksum-verified; they were not executed locally. Linux and
macOS CI execute native source-built binaries separately. No active installation
was replaced, no provider/inference calls were made, and no service was deployed.
This closes the distributed-artifact verification portion of T80.5.
