# Android Project Conventions

Use alongside `AGENTS.universal.md` and any other language conventions that
apply. These rules cover standalone Android apps and Android modules inside
other projects. Keep app-specific paths, package IDs and setup instructions in
the project's `AGENTS.md` and README.

## Project and toolchain

- Use Kotlin and the committed Gradle wrapper. Pin Gradle, Android Gradle
  Plugin, Kotlin, SDK platform, build tools and dependency versions in the
  project. Upgrade them together after checking compatibility.
- Builds must work from a terminal without Android Studio. Respect `JAVA_HOME`;
  otherwise look for a compatible JDK in `~/.jdks`. The current apps use JDK 21.
  Do not assume the system Java or Studio's bundled JBR matches the wrapper.
- Locate the SDK with `ANDROID_HOME` or ignored `local.properties`. Document
  a command-line bootstrap for the project's pinned components. Missing tools
  print an actionable install command and fail; do not install them silently.
- Reuse the user's Gradle caches. Do not run `clean`, disable caches or create
  a separate Gradle home for routine builds.
- Keep the application ID stable so updates preserve installed app data.
  Document the supported Android versions and the devices used for testing.
  Change SDK requirements deliberately, with the resulting compatibility
  changes stated in the project documentation.

## Application code

- Keep Activities, widgets and receivers thin. Put parsing, calculations and
  other logic that does not need Android in plain Kotlin with JVM tests.
- Run network and disk operations away from the main thread. Give requests
  timeouts and handle unavailable networks without crashing the app.
- Use lifecycle-aware work and WorkManager for persistent background jobs.
  Document widget refresh cadence and permissions; device background limits
  can delay scheduled work.
- Request only needed permissions. Export only components that must receive
  external intents, and validate those inputs. Document intentional network
  security exceptions such as configurable HTTP server access.
- Use resources for UI text, colours and themes. Check light and dark themes
  and relevant screen or widget sizes when changing the UI.
- Use a documented logcat tag. The universal CLI debug flags apply to any CLI
  in the repo; Android app diagnostics use logcat and device inspection.
  Avoid logging secrets or personal location data.

## Make commands and validation

Expose these commands from the repository root:

| Command | Convention |
|---------|------------|
| `make build` | Build the primary project artifact; a standalone app produces an APK in `bin/`. |
| `make apk` | Build the Android APK in a mixed-language repo, using the same version stamping as releases. |
| `make test` / `make lint` | Run JVM tests and Android lint in a standalone app. |
| `make android-check` | Build the APK, run JVM tests and Android lint in a mixed-language repo. |
| `make check` | Run the primary project's validation gate. Android-only projects include APK build, JVM tests and Android lint. |
| `make install` | Explicitly install with `adb install -r`; never install as a build or check side effect. |
| `make release` | Validate, upload the app's GitHub release, then queue F-Droid publication. |
| `make fdroid-publish` | Queue F-Droid publication again without recreating the GitHub release. |
| `make standards` | Refresh the committed convention files from `jsnjack/standards`. |

Run `make check` after changes. In mixed-language repositories, also run
`make android-check` when Android code, toolchain or release behavior changes.
Releases must pass both gates. Test behavior and failure cases; do not add tests
that merely restate configuration. A release APK must build successfully in
the publisher. Changes that affect shrinking, widgets, permissions or external
app integration also need a device smoke test; report when that was not done.

## Versioning

Derive the release version from `monova` and the universal `M` / `m` / `p`
commit prefixes. Pass it to Gradle as `-PappVersion=major.minor.patch`; use
that value for `versionName` in both local APKs and hosted releases.

Use this numeric mapping for `versionCode`:

```text
major * 1,000,000 + minor * 1,000 + patch
```

Minor and patch must each be below 1,000. Keep the result within Android's
version-code limit. The publisher uses at least this value and a code greater
than the last published code. Preserve any initial version-code floor needed
to update existing installations. A development fallback is allowed when no
version property is supplied; document it and use Make for release builds.

Check the latest published code before installing a local APK over an F-Droid
build. Its code must be at least as high and its signing certificate must
match. Do not uninstall an existing app to hide a signing or version problem.

## Signing and private files

Use a persistent app signing key across releases and distribution channels.
For new apps, create a dedicated app key. Existing personal apps may retain
their original debug certificate to preserve update compatibility; treat that
existing keystore as a permanent signing asset. A fresh CI runner's generated
debug key cannot update those installations.

Build an optimized, non-debuggable release APK for F-Droid and sign it with the
existing app key. Build type and signing certificate are separate choices.
Keep a separate key for signing the F-Droid repository index.

Keep keystores, passwords, source-access keys and SDK paths out of Git, public
artifacts and logs. Use ignored local files with restricted permissions and
encrypted GitHub Actions secrets. Back up signing assets in encrypted storage.
Check certificate fingerprints rather than printing private key material.
Use a read-only deploy key to fetch private source. Public APK hosting does
not make private source public, but APK contents themselves are downloadable;
obtain the owner's agreement before publishing a private app for the first time.

## Release and F-Droid deployment

The shared publisher is `jsnjack/android-apps`. Its client URL is
`https://jsnjack.github.io/android-apps/fdroid/repo/`; the install page at
`https://jsnjack.github.io/android-apps/` provides the fingerprint and QR code.
Adding a future app requires an entry in the publisher's trusted configuration
and an initial publication before it can use the individual release flow.

1. The user commits the app changes and pushes the release commit to
   `origin/master`; an agent does this only when explicitly asked.
   `make release` must reject a dirty checkout or a commit that differs from
   the remote release branch, and check that GitHub CLI authentication works.
2. Run the required validation and build the normal GitHub release artifacts.
   Upload them with the project's existing `grm release` command.
3. Only after that upload succeeds, dispatch `publish.yml` in the shared repo
   with the app selector, full source commit SHA and semantic release version.
   The publisher builds that exact revision, rather than whichever commit is
   newest when the job starts.
4. A successful dispatch means the job was queued. Follow GitHub Actions until
   deployment succeeds before reporting the APK as published. A failed
   dispatch must fail the Make command; retry with `make fdroid-publish` from
   the same release commit. Retry a failed Actions job through its run page.

The Make dispatch uses this interface, where `<app>` is the app's selector
in the publisher's `apps.json`:

```sh
gh workflow run publish.yml --repo jsnjack/android-apps \
  -f app=<app> -f revision="$(git rev-parse HEAD)" \
  -f release_version="$(monova)"
```

Use release-triggered publication and manual dispatch. Do not add scheduled
polling or rebuild on every ordinary source push. A selected app release keeps
the other apps' previously published APKs. An explicit manual publication may
update all apps from their configured branches.

The publisher serializes runs, restores its previous signed snapshot, retains
three APK versions per app, and validates package IDs, monotonically increasing
version codes, signing certificates and non-debuggable builds. Verify signed
F-Droid indices and APK checksums before deploying the complete site through
GitHub Pages. Failed builds must leave the deployed site usable. Store durable
snapshots in GitHub Releases and keep temporary Actions artifacts short-lived.

## Keeping conventions current

Commit `AGENTS.android.md` beside `AGENTS.universal.md` in each Android app
repository and link both from its root `AGENTS.md`. Mixed Go/Android projects
also retain `AGENTS.go.md`. Update `make standards` to download all applicable
files from the standards repository; use failing HTTP requests so an error page
does not silently replace a convention file.

Project documentation must state the APK path, build prerequisites, signing
policy, version stamping, release command, retry command and Actions URL. Keep
app-specific exceptions explicit rather than duplicating the shared rules.

References: [Android app signing](https://developer.android.com/studio/publish/app-signing),
[Android versioning](https://developer.android.com/studio/publish/versioning),
[F-Droid repository setup](https://f-droid.org/en/docs/Setup_an_F-Droid_App_Repo/).
