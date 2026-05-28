# weather-widget — Android home-screen widget for `weather`

A native Android widget that calls the `weather serve` JSON API
(`/api/v1/glance`) and renders the 2-hour Buienalarm + Buienradar
mini-chart on the home screen. Tap → opens the hourly forecast for the
widget's own location in the browser.

> **Target device:** a modern Samsung (One UI 8 / Galaxy A56, Android 16).
> `minSdk` is 31 (Android 12) — there are no compatibility shims for older
> releases. It installs on anything API 31+, but that's the floor we test.

> **Target host:** Fedora Linux. All commands assume `dnf` and standard
> Fedora layout. The project author runs Fedora on every dev machine;
> ports to other distros are out of scope.

## Pinned versions

`build.gradle.kts` and `gradle/wrapper/gradle-wrapper.properties` expect
these. Bootstrap must satisfy them.

| Component | Version |
|---|---|
| JDK | 17 or 21 — **not** newer; Gradle 8.9 rejects JDK 22+ (use the Studio JBR) |
| Android SDK platform | `android-34` |
| Android build-tools | `34.0.0` |
| Gradle | 8.9 (auto-fetched by `./gradlew` on first build) |
| Android Gradle Plugin | 8.5.2 |
| Kotlin | 2.0.20 |

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
    https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip
unzip -q /tmp/cmdline-tools.zip -d ~/Android/Sdk/cmdline-tools
mv ~/Android/Sdk/cmdline-tools/cmdline-tools ~/Android/Sdk/cmdline-tools/latest
rm /tmp/cmdline-tools.zip

# 3. SDK components pinned to this project
export ANDROID_HOME=$HOME/Android/Sdk
export PATH=$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH
yes | sdkmanager --licenses >/dev/null
sdkmanager 'platform-tools' 'platforms;android-34' 'build-tools;34.0.0'

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
The Gradle 8.9 distribution (~150 MB) is now cached in
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

> **JDK — read this first.** Gradle 8.9 runs on JDK 17–21. A newer *system*
> JDK (Fedora currently ships `java-25`) makes Gradle fail before it even
> evaluates the build scripts. Point `JAVA_HOME` at Android Studio's bundled
> JBR 21 before building:
>
> ```bash
> export JAVA_HOME=$(find /var/lib/flatpak/app/com.google.AndroidStudio \
>     -maxdepth 8 -type d -name jbr 2>/dev/null | head -1)
> ```
>
> To avoid re-exporting every shell, pin it once in `~/.gradle/gradle.properties`
> (per-machine, outside the repo): `org.gradle.java.home=/…/extra/jbr`.

The standard dev loop, from `android/`:

```bash
./gradlew :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

`adb install -r` redelivers `APPWIDGET_UPDATE` to existing widget instances,
so reinstalling already forces a refresh. Manual refresh now lives on a
**non-exported** receiver (`ManualRefreshReceiver`) so other apps can't
trigger it — which also means the old
`am broadcast … ACTION_MANUAL_REFRESH` command no longer reaches it from the
adb shell. To force a refetch without reinstalling, lock/unlock the phone
(the `USER_PRESENT` receiver) or tap the timestamp pill.

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

## Runtime debugging

Logcat — widgets only briefly host a process during refresh, so live
`--pid` filtering usually catches nothing. Read the buffer after the
fact instead:

```bash
adb logcat -d | grep -E 'AndroidRuntime|RainWidget|surfly\.weather'
```

WorkManager state (queued refresh work, recent failures):

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

Tap the chart to open the PWA in the browser. Tap the small refresh
icon (top-right) to force an immediate fetch.

## Cleartext HTTP — intentional

`res/xml/network_security_config.xml` permits cleartext to **any host**
via `<base-config cleartextTrafficPermitted="true">`. One APK works for
a LAN IP, a VPN-internal hostname, or a public HTTPS URL with no
rebuild. HTTPS still validates against the system CA store. This is for
personal sideloading — don't redistribute this APK.
