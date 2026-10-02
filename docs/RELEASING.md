# Releasing Claude Context Admin

A release is a version tag. `.github/workflows/release.yml` builds, signs and publishes; the only
step that needs a person is approving the signing job.

## Where things live

| What | Where |
|---|---|
| Source, releases, plugin marketplace | `github.com/johnccarroll/claude-context-admin` (public, MIT) |
| The Mac app (the recommended macOS install) | Homebrew cask in `github.com/johnccarroll/homebrew-tap`, `Casks/claude-context-admin.rb` |
| npm (macOS and Linux, in the browser) | `claude-context-admin` and `claude-context-admin-<os>-<arch>` |

## How a release runs

| Job | Runs on | Secrets | Does |
|---|---|---|---|
| `build` | Linux | none | tests; the cca binaries; Linux archives; npm packages |
| `build-mac` | macOS | none | the app, unsigned (records its SHA-256) |
| `sign-mac` | macOS, environment **`release`** | Developer ID, notary key | **waits for your approval**; checks the app is the one build-mac made; signs and notarizes the app and the macOS npm binaries in a throwaway keychain; checks every signature names team `JZZSSM8VY6`; deletes the keychain and keys |
| `publish` | Linux, environment **`publish`** | tap token | GitHub Release with provenance attestations; npm through trusted publishing (no token); the cask |

`sign-mac` installs and runs nothing but this repo's signing scripts and Apple's tools, so no
third-party build code ever runs next to the signing key. macOS runners are free for public
repositories; the macOS jobs never run from a private copy.

## Each release

1. Bump `"version"` in `plugin/context-admin/.claude-plugin/plugin.json` and commit (the workflow
   refuses a tag that doesn't match it).
2. `git tag v0.1.0 && git push origin v0.1.0`
3. GitHub emails you that `release` is waiting. **Actions → the run → Review deployments →
   Approve.** Everything else is automatic.

**Actions → release → Run workflow** is a dry run: both builds, nothing signed or published.

## One-time setup

1. **Repos.** `gh repo create johnccarroll/homebrew-tap --public` (an empty `Casks/` folder).
2. **Lock both down.** `scripts/repo-setup.sh` sets, and re-sets on every run: protected `main`;
   `v*` tags only by admins; the `release` (your approval) and `publish` environments limited to
   `v*` tags; Actions with a read-only token, SHA pinning and an allow-list; private vulnerability
   reporting, Dependabot, secret scanning with push protection, and CodeQL.
3. **npm.** Each of the five packages trusts `release.yml` in the `publish` environment
   (npmjs.com → package → Settings → Trusted Publisher), and its Publishing access is
   "Require two-factor authentication and disallow bypass 2fa tokens". No npm token exists.
4. **Secrets.** `scripts/repo-setup.sh secrets` asks for each one (hidden) and hands it to `gh`,
   which encrypts it before upload:
   - `release`: `MACOS_CERT_P12` (Keychain Access → "Developer ID Application: John Carroll" →
     Export as .p12), `MACOS_CERT_PASSWORD`, `NOTARY_KEY_P8`, `NOTARY_KEY_ID`, `NOTARY_ISSUER_ID`
     (an App Store Connect team API key with the **Developer** role: it can notarize and nothing
     else).
   - `publish`: `TAP_DEPLOY_KEY`, an SSH deploy key with write access to `homebrew-tap` and
     nothing else (`scripts/repo-setup.sh` shows the three commands).

   Delete the exported `.p12` afterwards (`rm -P`).
5. **Screenshots** when the UI changes: `scripts/build-mac-app.sh 0.0.0-dev && scripts/screenshots.sh`
   (made-up data only, never a real home).

Releases are `X.Y.Z` only; there are no prereleases. Once a release is out, the GitHub release
can't change (immutable releases). If a run fails partway, use **Re-run failed jobs**: re-running
everything would re-sign the app, and the workflow refuses to publish files that differ from the
release's.

## If a key leaks

- **Developer ID:** revoke the certificate (developer.apple.com → Certificates), which invalidates
  every signature it made; create a new one; redo the `MACOS_CERT_*` secrets. Apple's notary
  history lists every submission.
- **Notary key:** revoke it in App Store Connect → Users and Access → Integrations; make a new
  Developer-role key.
- **Tap deploy key:** delete it in the tap's Settings → Deploy keys; check the tap's history.

## Building by hand

```bash
scripts/release.sh 0.1.0        # CLI binaries, Linux archives, npm packages → dist/release/0.1.0
scripts/build-mac-app.sh 0.1.0  # the app → dist/mac; with CCA_SIGN_ID and CCA_NOTARY it is
                                # signed, notarized and zipped (scripts/sign-mac-app.sh)
```

A Mac set up once for local signing: `xcrun notarytool store-credentials cca-notary` with an App
Store Connect key, then
`CCA_SIGN_ID="Developer ID Application: John Carroll (JZZSSM8VY6)" CCA_NOTARY=cca-notary`.

## Verify on a clean machine

```bash
brew install --cask johnccarroll/tap/claude-context-admin && cca version && cca doctor
spctl --assess --type execute --verbose "/Applications/Claude Context Admin.app"   # Notarized Developer ID
gh attestation verify ClaudeContextAdmin-0.1.0-macos.zip --repo johnccarroll/claude-context-admin
npm i -g claude-context-admin && cca version
claude plugin marketplace add johnccarroll/claude-context-admin && claude plugin install context-admin@context-admin
claude mcp list | grep context-admin   # ✔ Connected
```

## Names

| What | Name |
|---|---|
| Product, cask, npm package | Claude Context Admin, `claude-context-admin` |
| Command | `cca` |
| Plugin and marketplace | `context-admin` (Claude Code's validator rejects third-party names starting with `claude-`) |
