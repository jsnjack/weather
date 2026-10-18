# weather-widget — Android home-screen widget for `weather`

A native Android widget that calls the `weather serve` JSON API
(`/api/v1/glance`) and renders the 2-hour Buienalarm + Buienradar
mini-chart on the home screen. Tap → opens the detailed today forecast for the
widget's own location in the browser.

When nearby rain is present, a right-hand radar map shows the latest KNMI
observation within a 50 km radius, with a location marker. The grey PDOK
background includes town names, roads and water; place labels remain above rain.
The map follows system dark mode: its basemap and labels are styled before
rain is drawn, keeping rain colours and the location marker unchanged. The web
player follows the browser's colour preference for both Local and Netherlands.
Both themes share upstream caches. The CLI exports dark PNG/GIF maps with
`weather radar --dark --output radar.png` or
`weather radar --dark --animate --output radar.gif`.
The square map has 16dp rounded corners and no surrounding panel or padding, so its aspect ratio stays
accurate without coloured bands above and below it.
All existing measurements remain in a compact native left panel. Radar taps
open the same local map in the browser and play from the latest observation
through the next hour, then stop. Replay, Pause, Now and the slider reuse the
loaded frames. Unavailable forecasts keep the current observation visible.
The browser starts after the observation and two forecast frames arrive, while
the remainder loads progressively. Its controls and time stay in one compact row.
Widgets smaller than 340×210dp (or too
short for the chosen font size) keep the original layout. Unavailable, empty or
stale radar data also keeps the original layout, including with older servers.
Deploy the updated server manually to enable `/api/v1/radar/local` and
`/api/v1/radar/local/timeline`.

Forecasts use KNMI's five-minute nowcast, pinned to one run through its
`reference_time` dimension. Frames load only on a map tap or Play, and reuse
the observation's projection and basemap. The server caches KNMI WMS requests
in memory and on disk. PDOK background
tiles have a separate seven-day cache; the embedded land outline remains the
fallback when map tiles cannot be fetched. Anonymous WMS access works without
configuration but has a shared quota; set the server's
`KNMI_WMS_API_KEY` environment variable for a dedicated quota. Images older
than 20 minutes are rejected rather than shown as current observations.
Request the **Web Map Service (WMS)** key in the KNMI API catalogue. Anonymous
access allows one request per second per IP and shares a 1,000-request hourly
quota; an API key allows twenty per second and 1,000 per hour. The server spaces
requests accordingly, retains the caches and honours rate-limit cooldowns.
Keep the key on the server, outside the browser and widget configuration.

Limits: https://developer.dataplatform.knmi.nl/wms

Redeploy the server to enable `theme=dark` on the radar image and timeline APIs.
While the app process is alive, a night-mode change queues an expedited widget
refresh; the existing periodic refresh handles updates after process death.

> **Target device:** a modern Samsung (One UI 8 / Galaxy A56, Android 16).
> `minSdk` is 31 (Android 12) — there are no compatibility shims for older
> releases. It installs on anything API 31+, but that's the floor we test.

> **Target host:** Fedora Linux. All commands assume `dnf` and standard
> Fedora layout. The project author runs Fedora on every dev machine;
> ports to other distros are out of scope.

## Pinned versions

`build.gradle.kts`, `gradle/wrapper/gradle-wrapper.properties` and
`gradle/gradle-daemon-jvm.properties` pin these. The compile SDK is 37; the
target SDK remains 34 and the minimum supported Android version remains 12
(API 31).

| Component | Version |
|---|---|
| Build JDK | 25 (Gradle downloads it when no JDK 25 is installed) |
| Android SDK platform | `android-37.0` (Gradle installs it when missing) |
| Android build-tools | `37.0.0` (Gradle installs it when missing) |
| Gradle | 9.8.1 (`./gradlew` downloads it on first build) |
| Android Gradle Plugin | 9.4.1 |
| Kotlin | 2.4.21 (AGP built-in Kotlin) |

## Bootstrap a fresh Fedora machine

Two install paths. **Path A** is the agent's default — no GUI, smallest
footprint, fully scriptable. **Path B** is for humans who want Layout
Inspector / Profiler.

### Path A — Android CLI (recommended for agents)

Copy-paste, top-to-bottom. The first build downloads Gradle, JDK 25 and the
pinned SDK packages, about 1 GB in total.

```bash
# 1. Any Java 17+ runtime starts ./gradlew; Gradle provisions the build JDK
sudo dnf install -y java-25-openjdk-headless

# 2. Android CLI (replaces the deprecated cmdline-tools sdkmanager)
mkdir -p ~/.local/bin
curl -fL -o ~/.local/bin/android \
    https://dl.google.com/android/cli/latest/linux_x86_64/android-cli
chmod +x ~/.local/bin/android

# 3. SDK with platform-tools. The first run shows the SDK terms and records
#    the licence, which lets Gradle install the pinned platform and build tools
android --no-metrics --sdk="$HOME/Android/Sdk" sdk install platform-tools

# 4. Tell Gradle where the SDK lives (local.properties is gitignored)
echo "sdk.dir=$HOME/Android/Sdk" > /path/to/weather/android/local.properties

# 5. Put adb on PATH for future shells (optional)
cat >> ~/.bashrc <<'BASHRC'
export ANDROID_HOME=$HOME/Android/Sdk
export PATH=$ANDROID_HOME/platform-tools:$PATH
BASHRC

# 6. First build, from the repository root
make apk
```

After step 6 succeeds you have `bin/weather-widget.apk`. Gradle caches its
distribution in `~/.gradle/wrapper/dists/` and the build JDK in
`~/.gradle/jdks/`; every later build on this machine reuses both.
`android update` updates the CLI itself; `android sdk update` updates the
installed SDK packages.

The CLI downloads from `dl.google.com` without falling back to IPv4. On a host
with broken IPv6 routing, download the CLI with `curl -4` and avoid the
`android` shim in cmdline-tools, which tries to fetch the CLI itself.

### Path B — Android Studio (humans only)

```bash
flatpak install -y flathub com.google.AndroidStudio
flatpak run com.google.AndroidStudio   # Open the project folder, let it sync
```

Studio writes `local.properties` itself and installs the SDK
automatically. After first sync you can close Studio and use the same
`make apk` / `./gradlew` flow as Path A; no `JAVA_HOME` is needed.

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

> **JDK.** `./gradlew` needs any Java 17+ runtime on `PATH` (or in
> `JAVA_HOME`) to start. The build itself runs on a JDK 25 found on the
> machine, or one Gradle downloads into `~/.gradle/jdks/`. After changing the
> JDK version, regenerate the download links with
> `./gradlew updateDaemonJvm --jvm-version=<version>`.

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
