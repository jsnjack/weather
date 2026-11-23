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
| `make release` | Validate, build the signed release APK, upload it to the app's GitHub release and the install page. |
| `make publish-apk` | Put the signed release APK on the install page again, to retry that step. |
| `make standards` | Refresh the committed convention files from `jsnjack/standards`. |

Run `make check` after changes. In mixed-language repositories, also run
`make android-check` when Android code, toolchain or release behavior changes.
Releases must pass both gates. Test behavior and failure cases; do not add tests
that merely restate configuration. Changes that affect shrinking, widgets,
permissions or external app integration also need a device smoke test of the
release build; report when that was not done.

## Versioning

Derive the release version from `monova` and the universal `M` / `m` / `p`
commit prefixes. Pass it to Gradle as `-PappVersion=major.minor.patch`; use
that value for `versionName` in both local APKs and hosted releases.

Use this numeric mapping for `versionCode`:

```text
major * 1,000,000 + minor * 1,000 + patch
```

Minor and patch must each be below 1,000. Keep the result within Android's
version-code limit. Every release must have a code greater than the last
released one. Preserve any initial version-code floor needed
to update existing installations. A development fallback is allowed when no
version property is supplied; document it and use Make for release builds.

Check the latest published code before installing a local APK over a
released build. Its code must be at least as high and its signing certificate
must match. Do not uninstall an existing app to hide a signing or version problem.

## Signing and private files

Use a persistent app signing key across releases and distribution channels.
For new apps, create a dedicated app key. Existing personal apps may retain
their original debug certificate to preserve update compatibility; treat that
existing keystore as a permanent signing asset. A fresh CI runner's generated
debug key cannot update those installations.

Release builds are optimized and non-debuggable, built locally by `make
release` and signed with the existing app key (`apksigner` from the pinned
build tools). Build type and signing certificate are separate choices.

Keep keystores, passwords and SDK paths out of Git, public artifacts and logs.
Use ignored local files with restricted permissions. Back up signing assets in
encrypted storage. Check certificate fingerprints rather than printing private
key material. APK contents are downloadable from a release; obtain the
owner's agreement before publishing a private app for the first time.

## Release

Apps are published as signed APKs on their GitHub releases and on the shared
install page https://jsnjack.github.io/android-apps/, where a phone or tablet
installs them with one tap. There is no F-Droid repository and no GitHub
Actions workflow.

1. The user commits the app changes and pushes the release commit to
   `origin/master`; an agent does this only when explicitly asked.
   `make release` must reject a dirty checkout or a commit that differs from
   the remote release branch.
2. Run the required validation, build the release APK and sign it with the
   app key, then verify the signature (`apksigner verify`).
3. Upload the APK and any other release artifacts with the project's existing
   `grm release` command to the release `v<monova version>`. Give the APK a
   fixed file name so the latest release's download link stays stable.
4. Last, `make publish-apk` replaces the app's APK on the `apps` release of
   `jsnjack/android-apps`, which the install page offers, with the version as
   the asset's label:
   `gh release upload apps bin/release/<app>.apk#$(monova) --clobber --repo jsnjack/android-apps`.
   It needs an authenticated GitHub CLI; run it again to retry. A new app
   also needs a card in that repository's `index.html`. `make status` there
   lists apps whose latest release is not on the page.

## Keeping conventions current

Commit `AGENTS.android.md` beside `AGENTS.universal.md` in each Android app
repository and link both from its root `AGENTS.md`. Mixed Go/Android projects
also retain `AGENTS.go.md`. Update `make standards` to download all applicable
files from the standards repository; use failing HTTP requests so an error page
does not silently replace a convention file.

Project documentation must state the APK path, build prerequisites, signing
policy, version stamping and release command. Keep app-specific exceptions
explicit rather than duplicating the shared rules.

References: [Android app signing](https://developer.android.com/studio/publish/app-signing),
[Android versioning](https://developer.android.com/studio/publish/versioning).
