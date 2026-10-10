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
        // minSdk 31 (Android 12): the widget uses API-31 appwidget attributes
        // (targetCellWidth/Height, previewLayout, maxResize*). Targeting only
        // modern devices (this build is for a One UI 8 / Galaxy A56) means no
        // version-guard shims for older releases.
        minSdk = 31
        targetSdk = 34
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
    implementation("androidx.work:work-runtime-ktx:2.12.0")
    implementation("com.google.android.gms:play-services-location:21.4.0")
    implementation("com.squareup.okhttp3:okhttp:5.5.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-play-services:1.11.0")
}
