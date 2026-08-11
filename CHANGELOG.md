# Changelog

All notable changes to MoltSSH are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
while the public API and wire protocol remain in the `0.x` development series.

## [Unreleased]

## [0.4.1] - 2026-08-11

### Added

- Maintainer guidance for curated release preambles and pull request labels that
  drive GitHub-generated release-note categories.

### Changed

- Tagged releases now combine an optional checked-in preamble with
  GitHub-generated pull request categories, contributor credits, and the full
  changelog link.
- Rerunning a tagged release refreshes the generated notes before updating the
  existing GitHub Release.

## [0.4.0] - 2026-08-11

### Added

- A generated architecture diagram for the English and Chinese READMEs.
- Root and command-specific CLI help, including `moltssh help COMMAND`.
- `moltssh version` and `moltssh --version` output with release version,
  source commit, and Go toolchain metadata.
- Actionable troubleshooting hints for configuration, probe, proxy, and server
  failures.
- A security warning when the raw server listener is not bound to loopback.
- Contributor, code-of-conduct, security, changelog, and release-note guidance.
- Installation through
  `go install github.com/jingyijun/moltssh/cmd/moltssh@latest`.

### Changed

- Release binaries now embed the release version and full source commit.
- Tagged releases use checked-in release notes when available and otherwise use
  GitHub-generated notes.
- GitHub Actions now use the Node 24-based `actions/checkout@v7` and
  `actions/setup-go@v7` while preserving `go.sum` cache invalidation.

### Fixed

- Failed path probes now return a non-zero command status without leaking
  endpoint query values into the summarized error.

## [0.3.1] - 2026-08-06

### Fixed

- Activated the first available healthy path immediately instead of waiting
  for every path probe to finish.

## [0.3.0] - 2026-08-06

### Added

- Parallel phased dialing, reusable probe connections, active-path heartbeat,
  last-known-good startup, and detailed dial-phase timing records.

### Changed

- Documented path performance, cache behavior, and reconnect backoff.

## [0.2.0] - 2026-07-09

### Added

- Defaults for optional resume, probe, and path configuration fields.

## [0.1.1] - 2026-07-09

### Added

- Docker SSH smoke coverage for failover between two WebSocket relay paths.

## [0.1.0] - 2026-07-09

### Added

- Initial WebSocket MVP with OpenSSH `ProxyCommand`, session resume, path
  probing, path switching, CI, and cross-platform release binaries.

[Unreleased]: https://github.com/JingYiJun/MoltSSH/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/JingYiJun/MoltSSH/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/JingYiJun/MoltSSH/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/JingYiJun/MoltSSH/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/JingYiJun/MoltSSH/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/JingYiJun/MoltSSH/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/JingYiJun/MoltSSH/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/JingYiJun/MoltSSH/releases/tag/v0.1.0
