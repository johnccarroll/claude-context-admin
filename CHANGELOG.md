# Changelog

What changed in each release, newest first. The release workflow copies a version's section into
its GitHub release notes, where line breaks show, so each entry is one line.

## 0.2.0 (2026-10-22)

- The memory map opens in 3D: memories glow in project clusters with their names on the map, it turns until you touch it, and opening a memory flies to it. 2D is one tap away and is used without a graphics card or with Reduce Motion. Arrow keys move between memories in both.
- Filters are labelled (Loads in, Kind) and say what's selected, with Clear filters beside the count.
- The editor slides out cleanly; before, the whole window could jump sideways while it opened.
- Buttons and rows answer when pressed, menus grow from where they open, hover effects no longer stick on touch screens, and Reduce Motion cross-fades instead of moving.
- Review shows what a suggestion would change as a clear before and after.
- Built with Go 1.26.10, which fixes a certificate-verification advisory in the standard library (GO-2026-6809).
- The map joins a memory written in two projects with a dotted line, and its preview says where else it's written. Review's duplicate check and the map now use the same rule.

## 0.1.5 (2026-10-12)

- Each release now comes with an SBOM, `claude-context-admin-<version>.cdx.json` (CycloneDX 1.6), listing everything inside cca at the versions that were built, and with a signed SBOM attestation for every download.
- CI runs the real binary in a network namespace that has only loopback: it must work with no network, listen on 127.0.0.1 only and never try to reach anything else.
- CI and the daily canary run `bun audit` on the web UI's packages.

There is no 0.1.4: its publish job stopped before releasing anything.

## 0.1.3 (2026-10-12)

- The Homebrew cask installs without admin rights: when `/Applications` is read-only the app goes to `~/Applications`, so no password is asked for.
- Built with Go 1.26.9, which fixes ten standard library advisories.
- A test fails the build on any network use beyond 127.0.0.1 in cca's own code.
- `govulncheck` also runs daily, not only on a push.

## 0.1.2 (2026-10-07)

- Signed with the Developer ID of JC Technologies LLC (team `JZZSSM8VY6`).
- CI runs `govulncheck`; `golang.org/x/sys` updated.

## 0.1.1 (2026-10-02)

- Every name that becomes a file path is checked, and the server acts only on paths it owns.
- The editor docks beside the list and resizes; back and forward between memories.
- Compare duplicate memories side by side; projects ranked by what they load.
- Memory links show as named chips, with a `[[` picker.
- Unknown memory types and the keys of a broken header are kept when a memory is saved.

## 0.1.0 (2026-10-02)

First release.
