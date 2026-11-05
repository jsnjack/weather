plugins {
    id("com.android.application")
    kotlin("plugin.serialization")
}

android {
    namespace = "net.surfly.weather.widget"
    compileSdk = 37
    buildToolsVersion = "37.0.0"

    defaultConfig {
        applicationId = "net.surfly.weather.widget"
        // minSdk 35 (Android 15): the widget picker's live preview
        // (AppWidgetManager.setWidgetPreview) is an Android 15 API; the
        // day/night images, exact-size layouts and launcher corner radii
        // are Android 12 APIs. The app targets the user's Galaxy A56 on
        // One UI 9 (Android 17) and must keep working on One UI 8.5
        // (Android 16), so nothing here needs Android 17, and there are no
        // version-guard shims for older releases.
        minSdk = 35
        targetSdk = 37
        val appVersion = (project.findProperty("appVersion") as String?)?.takeIf { it != "0.0.0" }
        versionName = appVersion ?: "0.2"
        versionCode = if (appVersion != null) {
            require(Regex("\\d+\\.\\d+\\.\\d+").matches(appVersion)) {
                "appVersion must be major.minor.patch"
            }
            val (major, minor, patch) = appVersion.split('.').map(String::toLong)
            require(minor < 1_000 && patch < 1_000 && major <= 2_100) {
                "appVersion exceeds Android version-code limits"
            }
            val code = major * 1_000_000 + minor * 1_000 + patch
            require(code <= 2_100_000_000) { "appVersion exceeds Android version-code limits" }
            maxOf(2, code.toInt())
        } else {
            2
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
        debug {
            isMinifyEnabled = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures { viewBinding = true }
}

dependencies {
    implementation("androidx.core:core-ktx:1.19.1")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.work:work-runtime-ktx:2.12.0")
    implementation("com.google.android.gms:play-services-location:21.4.0")
    implementation("com.squareup.okhttp3:okhttp:5.5.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-play-services:1.11.0")
    testImplementation("junit:junit:4.13.2")
}
