# weather-widget — Android home-screen widget for `weather`

A native Android widget that calls the `weather serve` JSON API
(`/api/v1/rain`) and renders the 2-hour Buienalarm + Buienradar
mini-chart on the home screen. Tap → opens the PWA in the browser.

> **Target host:** Fedora Linux. All commands assume `dnf` and standard
> Fedora layout. The project author runs Fedora on every dev machine;
> ports to other distros are out of scope.

## Pinned versions

`build.gradle.kts` and `gradle/wrapper/gradle-wrapper.properties` expect
these. Bootstrap must satisfy them.

| Component | Version |
|---|---|
| JDK | 17 or 21 (AGP 8.5 requires ≥17) |
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

The standard agent dev loop, from `android/`:

```bash
./gradlew :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
# Force an immediate refresh of any placed widget instance:
adb shell am broadcast \
    -a net.surfly.weather.widget.ACTION_MANUAL_REFRESH \
    -n net.surfly.weather.widget/.RainWidgetProvider
```

`adb install -r` already delivers `APPWIDGET_UPDATE` to existing widget
instances, so the broadcast is usually optional. Use it to force an
immediate refetch without waiting for the next periodic tick.

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

WorkManager state (next periodic tick, recent failures):

```bash
adb shell dumpsys jobscheduler | grep -A 20 surfly.weather
```

## Widget configuration (per-instance, at runtime)

When the widget is dropped on the home screen, a small dialog opens:

- **Server URL** — pre-filled from the previous widget's value or the
  hardcoded `DEFAULT_URL` in `WidgetPrefs.kt`. Override at runtime per
  widget instance.
- **Location** —
  - *Auto* — last-known FLP coarse location each refresh; falls back
    silently to the server's IP geolocation if unavailable.
  - *Fixed name* — forward a place name to the server.
  - *Fixed coords* — explicit lat/lon.
  - *Server IP* — let the server's MaxMind fallback decide; least
    accurate on cellular, no permissions.
- **Refresh interval** — 15 / 30 / 60 / 120 min. Global across all
  widget instances; WorkManager honours one cadence per unique
  periodic-work name (most recent change wins).

Tap the chart to open the PWA in the browser. Tap the small refresh
icon (top-right) to force an immediate fetch.

## Cleartext HTTP — intentional

`res/xml/network_security_config.xml` permits cleartext to **any host**
via `<base-config cleartextTrafficPermitted="true">`. One APK works for
a LAN IP, a VPN-internal hostname, or a public HTTPS URL with no
rebuild. HTTPS still validates against the system CA store. This is for
personal sideloading — don't redistribute this APK.
