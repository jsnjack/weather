# weather-widget — Android home-screen widget for `weather`

A native Android widget that calls the `weather serve` JSON API
(`/api/v1/glance`) and renders the 2-hour Buienalarm + Buienradar
mini-chart on the home screen. Tap → opens the detailed today forecast for the
widget's own location in the browser.

When nearby rain is present, a right-hand radar island shows the latest KNMI
observation within a 50 km radius, with a location marker.
All existing measurements remain in a compact native left panel. Radar taps
open the same local map in the web view. Widgets smaller than 340×210dp (or too
short for the chosen font size) keep the original layout. Unavailable, empty or
stale radar data also keeps the original layout, including with older servers.
Deploy the updated server manually to enable `/api/v1/radar/local`.

The server caches KNMI WMS requests in memory and on disk and embeds its land outline. Anonymous WMS
access works without configuration but has a shared quota; set the server's
`KNMI_WMS_API_KEY` environment variable for a dedicated quota. Images older
than 20 minutes are rejected rather than shown as current observations.

> **Target device:** a modern Samsung (One UI 8 / Galaxy A56, Android 16).
> `minSdk` is 31 (Android 12) — there are no compatibility shims for older
> releases. It installs on anything API 31+, but that's the floor we test.

> **Target host:** Fedora Linux. All commands assume `dnf` and standard
> Fedora layout. The project author runs Fedora on every dev machine;
> ports to other distros are out of scope.

## Pinned versions

`build.gradle.kts` and `gradle/wrapper/gradle-wrapper.properties` expect
these. Bootstrap must satisfy them. The compile SDK is 37; the target SDK remains
34 and the minimum supported Android version remains 12 (API 31).

| Component | Version |
|---|---|
| JDK | 21 (tested; Make also accepts JDK 17) |
| Android SDK platform | `android-37.0` |
| Android build-tools | `37.0.0` |
| Gradle | 9.8.1 (auto-fetched by `./gradlew` on first build) |
| Android Gradle Plugin | 9.4.1 |
| Kotlin | 2.4.21 (AGP built-in Kotlin) |

## Bootstrap a fresh Fedora machine

Two install paths. **Path A** is the agent's default — no GUI, smallest
footprint, fully scriptable. **Path B** is for humans who want Layout
Inspector / Profiler.

### Path A — cmdline-tools only (recommended for agents)

Copy-paste, top-to-bottom. ~15 min on first run, dominated by the SDK
download.

```bash
# 1. JDK
sudo dnf install -y java-21-openjdk-devel unzip
export JAVA_HOME=$(dirname $(dirname $(readlink -f $(command -v javac))))

# 2. Android SDK command-line tools
mkdir -p ~/Android/Sdk/cmdline-tools
curl -fL -o /tmp/cmdline-tools.zip \
    https://dl.google.com/android/repository/commandlinetools-linux-16111833_latest.zip
unzip -q /tmp/cmdline-tools.zip -d ~/Android/Sdk/cmdline-tools
mv ~/Android/Sdk/cmdline-tools/cmdline-tools ~/Android/Sdk/cmdline-tools/latest
rm /tmp/cmdline-tools.zip

# 3. SDK components pinned to this project
export ANDROID_HOME=$HOME/Android/Sdk
export PATH=$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH
yes | sdkmanager --licenses >/dev/null
sdkmanager 'platform-tools' 'platforms;android-37.0' 'build-tools;37.0.0'

# 4. Tell Gradle where the SDK lives (local.properties is gitignored)
echo "sdk.dir=$HOME/Android/Sdk" > /path/to/weather/android/local.properties

# 5. Persist env vars for future shells (optional — agents can re-export)
cat >> ~/.bashrc <<'EOF'
export JAVA_HOME=$(dirname $(dirname $(readlink -f $(command -v javac))))
export ANDROID_HOME=$HOME/Android/Sdk
export PATH=$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH
EOF

# 6. First build
cd /path/to/weather/android
./gradlew :app:assembleDebug
```

After step 6 succeeds you have `app/build/outputs/apk/debug/app-debug.apk`.
The Gradle 9.8.1 distribution (~150 MB) is now cached in
`~/.gradle/wrapper/dists/` and is shared across all subsequent builds
on this machine.

### Path B — Android Studio (humans only)

```bash
flatpak install -y flathub com.google.AndroidStudio
flatpak run com.google.AndroidStudio   # Open the project folder, let it sync
```

Studio writes `local.properties` itself and installs the SDK
automatically. After first sync you can close Studio and use the same
`./gradlew` flow as Path A — set `JAVA_HOME` to the bundled JBR:

```bash
export JAVA_HOME=$(find /var/lib/flatpak/app/com.google.AndroidStudio \
    -maxdepth 8 -type d -name jbr 2>/dev/null | head -1)
```

## Phone setup (one-time, per device)

1. **Developer options** — Settings → About phone → Software information
   → tap "Build number" 7×.
2. **USB debugging** — Settings → Developer options → USB debugging ON.
3. **Plug in** — accept the "Allow USB debugging?" RSA prompt on the
   phone. Tick **Always allow from this computer**.
4. **Samsung Auto Blocker** (if Samsung) — Settings → Security and
   privacy → Auto Blocker → toggle off **"Block commands by USB cable"**.
   Master toggle can stay on.
5. **Verify** — `adb devices` should list a `device` line (not
   `unauthorized` or `offline`).

## Build + install + refresh loop

> **JDK.** The terminal build is tested with JDK 21. Make respects `JAVA_HOME`
> and otherwise looks for JDK 21 or 17 in `~/.jdks`. To unpack JDK 21 locally:
>
> ```bash
> mkdir -p ~/.jdks && curl -fL \
>     https://api.adoptium.net/v3/binary/latest/21/ga/linux/x64/jdk/hotspot/normal/eclipse \
>     | tar xz -C ~/.jdks
> export JAVA_HOME=$(ls -d ~/.jdks/jdk-21* | head -1)   # for ./gradlew run by hand
> ```

The standard dev loop, from `android/`:

```bash
./gradlew :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

`make release` (repo root) builds the same debug-signed APK and uploads it
to the GitHub release as `weather-widget.apk`, so a phone can sideload it
straight from the release page. Debug-signed is deliberate: it matches dev
installs, so it updates an existing widget in place; a differently-signed
APK would require uninstalling first.

`make apk` and `make release` stamp the widget version from `monova`. The numeric
version code is `major * 1,000,000 + minor * 1,000 + patch`, matching F-Droid
publication. A direct Gradle build without `-PappVersion` keeps the original
development version; use `make apk` for builds intended to update a released widget.

Run `make android-check` from the repository root to build the widget APK and
run its JVM tests and Android lint. `make release` requires this Android gate
and the Go `make check` gate to pass before uploading artifacts.

Commit changes and push `master` before running `make release`. After uploading
the GitHub release, it queues the shared F-Droid publisher for this app's exact
source commit. GitHub Actions builds and signs the repository APK and deploys
it to https://jsnjack.github.io/android-apps/; Wahoo Share stays at its published
version. There is no scheduled polling. GitHub CLI (`gh`) must be authenticated
with access to `jsnjack/android-apps`. If only the F-Droid trigger fails, retry
with `make fdroid-publish` from the repository root. Follow the deployment in
https://github.com/jsnjack/android-apps/actions/workflows/publish.yml.

`adb install -r` redelivers `APPWIDGET_UPDATE` to existing widget instances,
so reinstalling already forces a refresh and re-arms the 15-minute periodic
WorkManager refresh. Manual refresh now lives on a
**non-exported** receiver (`ManualRefreshReceiver`) so other apps can't
trigger it — which also means the old
`am broadcast … ACTION_MANUAL_REFRESH` command no longer reaches it from the
adb shell. To force a refetch without reinstalling, tap the timestamp pill.
Lock/unlock also triggers an expedited one-shot while the widget process is
alive via a context-registered `USER_PRESENT` receiver; the periodic worker is
the reliable automatic floor when Android has killed the process.

## Visual iteration via screenshots

`adb` can pull the phone's framebuffer — useful when an agent is
iterating on widget visuals and needs to *see* what changed:

```bash
adb shell input keyevent KEYCODE_WAKEUP    # if screen is off
adb exec-out screencap -p > /tmp/phone.png
```

The lock screen can't be bypassed via adb — the human has to unlock
the phone manually before the home screen is visible. After that, the
agent can `Read /tmp/phone.png` directly to view it.

> **Stale-frame gotcha.** Right after `adb install -r`, the One UI launcher
> can show a *composited/stale* widget frame for several seconds — old and new
> content superimposed (e.g. a doubled headline, overlapping panels), made
> worse by the Auto-mode two-pass refresh applying two full updates ~2 s apart.
> It is not a layout bug. Force a clean redraw before trusting a screenshot:
> `adb shell input swipe 900 1200 100 1200 200 && adb shell input swipe 100 1200 900 1200 200`
> (swipe to the next home page and back), then re-`screencap`.

## Runtime debugging

Logcat — widgets only briefly host a process during refresh, so live
`--pid` filtering usually catches nothing. Read the buffer after the
fact instead:

```bash
adb logcat -d | grep -E 'AndroidRuntime|RainWidget|surfly\.weather'
```

WorkManager state (next periodic tick, queued one-shots, recent failures):

```bash
adb shell dumpsys jobscheduler | grep -A 20 surfly.weather
```

## Widget configuration (per-instance, at runtime)

When the widget is dropped on the home screen, a small dialog opens:

- **Server URL** — pre-filled from the previous widget's value or the
  hardcoded `DEFAULT_URL` in `WidgetPrefs.kt`. Override at runtime per
  widget instance.
- **Location** —
  - *Auto* — a current coarse fix each refresh, re-requested whenever the
    last-known fix is stale (>30 min) or inaccurate (>5 km). Needs location
    permission, plus **Allow all the time** (background) for fresh fixes
    while the screen is off or you're travelling. When no trustworthy fix is
    available it shows **No location** rather than silently using the
    server's IP.
  - *Fixed name* — forward a place name to the server.
  - *Fixed coords* — explicit lat/lon.
  - *Server IP* — let the server's MaxMind fallback decide; least
    accurate on cellular, no permissions.
- **Refresh cadence** — fixed 15-minute WorkManager periodic refresh, plus
  expedited unlock-triggered one-shots while the widget process is alive and
  explicit tap-to-refresh.

Tap the chart to open the PWA in the browser. Tap the small refresh
icon (top-right) to force an immediate fetch.

## Cleartext HTTP — intentional

`res/xml/network_security_config.xml` permits cleartext to **any host**
via `<base-config cleartextTrafficPermitted="true">`. One APK works for
a LAN IP, a VPN-internal hostname, or a public HTTPS URL with no
rebuild. HTTPS still validates against the system CA store. APKs are distributed
through the project's GitHub releases and custom F-Droid repository; the user
chooses the server when configuring the widget.
