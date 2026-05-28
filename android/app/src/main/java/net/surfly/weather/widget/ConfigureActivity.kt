package net.surfly.weather.widget

import android.Manifest
import android.app.Activity
import android.appwidget.AppWidgetManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.RadioGroup
import android.widget.Toast
import androidx.core.content.ContextCompat

class ConfigureActivity : Activity() {

    companion object {
        private const val REQ_LOCATION = 1001
        private const val REQ_BACKGROUND = 1002
    }

    private var appWidgetId = AppWidgetManager.INVALID_APPWIDGET_ID

    private lateinit var urlField: EditText
    private lateinit var modeGroup: RadioGroup
    private lateinit var nameField: EditText
    private lateinit var coordsRow: LinearLayout
    private lateinit var latField: EditText
    private lateinit var lonField: EditText

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setResult(RESULT_CANCELED)
        setContentView(R.layout.activity_configure)

        appWidgetId = intent?.extras?.getInt(
            AppWidgetManager.EXTRA_APPWIDGET_ID, AppWidgetManager.INVALID_APPWIDGET_ID
        ) ?: AppWidgetManager.INVALID_APPWIDGET_ID
        if (appWidgetId == AppWidgetManager.INVALID_APPWIDGET_ID) {
            finish(); return
        }

        urlField = findViewById(R.id.url)
        modeGroup = findViewById(R.id.location_mode)
        nameField = findViewById(R.id.location_name)
        coordsRow = findViewById(R.id.coords_row)
        latField = findViewById(R.id.location_lat)
        lonField = findViewById(R.id.location_lon)

        val current = WidgetPrefs.load(this, appWidgetId)
        urlField.setText(if (current.serverUrl.isBlank()) WidgetPrefs.defaultUrl(this) else current.serverUrl)
        nameField.setText(current.name)
        if (current.lat != 0.0) latField.setText(current.lat.toString())
        if (current.lon != 0.0) lonField.setText(current.lon.toString())
        when (current.mode) {
            LocationMode.AUTO -> modeGroup.check(R.id.mode_auto)
            LocationMode.NAME -> modeGroup.check(R.id.mode_name)
            LocationMode.COORDS -> modeGroup.check(R.id.mode_coords)
            LocationMode.IP -> modeGroup.check(R.id.mode_ip)
        }
        modeGroup.setOnCheckedChangeListener { _, checkedId -> updateModeUI(checkedId) }
        updateModeUI(modeGroup.checkedRadioButtonId)

        findViewById<Button>(R.id.save).setOnClickListener { onSave() }
    }

    private fun updateModeUI(checkedId: Int) {
        nameField.visibility = if (checkedId == R.id.mode_name) View.VISIBLE else View.GONE
        coordsRow.visibility = if (checkedId == R.id.mode_coords) View.VISIBLE else View.GONE
        if (checkedId == R.id.mode_auto) {
            ensureLocationPermissions()
        }
    }

    /**
     * Auto mode wants a *current* fix during background WorkManager one-shots,
     * which on Android 11+ requires ACCESS_BACKGROUND_LOCATION — and only after
     * foreground location is already granted.
     *
     * On Android 11+, the system does NOT allow apps to request background
     * location via a permission dialog; users must grant "Allow all the time"
     * manually in the app's Settings page. We walk the user there directly.
     *
     * Without background permission the widget still falls back to:
     *   1. A last-known fix ≤30 min old (often refreshed by Maps/other apps), or
     *   2. The last coords saved from a previous successful widget refresh.
     * So "No location" should only appear on a completely fresh install with
     * no prior fix and no background permission.
     */
    private fun ensureLocationPermissions() {
        if (!granted(Manifest.permission.ACCESS_COARSE_LOCATION)) {
            requestPermissions(arrayOf(Manifest.permission.ACCESS_COARSE_LOCATION), REQ_LOCATION)
            return
        }
        if (!granted(Manifest.permission.ACCESS_BACKGROUND_LOCATION)) {
            // Android 11+: permission dialog can't offer "Allow all the time" —
            // the only reliable path is the app's system settings page.
            Toast.makeText(this, R.string.cfg_background_denied, Toast.LENGTH_LONG).show()
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                startActivity(
                    Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS).apply {
                        data = Uri.fromParts("package", packageName, null)
                        flags = Intent.FLAG_ACTIVITY_NEW_TASK
                    }
                )
            } else {
                requestPermissions(arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION), REQ_BACKGROUND)
            }
        }
    }

    private fun granted(perm: String): Boolean =
        ContextCompat.checkSelfPermission(this, perm) == PackageManager.PERMISSION_GRANTED

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        val ok = grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED
        when (requestCode) {
            REQ_LOCATION -> {
                if (ok) {
                    ensureLocationPermissions() // chain into the background-location step
                } else {
                    Toast.makeText(this, R.string.cfg_location_denied, Toast.LENGTH_LONG).show()
                }
            }
            REQ_BACKGROUND -> {
                if (!ok) {
                    Toast.makeText(this, R.string.cfg_background_denied, Toast.LENGTH_LONG).show()
                }
            }
        }
    }

    private fun onSave() {
        val url = urlField.text.toString().trim()
        if (!url.startsWith("http://") && !url.startsWith("https://")) {
            Toast.makeText(this, R.string.cfg_invalid_url, Toast.LENGTH_LONG).show()
            return
        }
        val mode = when (modeGroup.checkedRadioButtonId) {
            R.id.mode_name -> LocationMode.NAME
            R.id.mode_coords -> LocationMode.COORDS
            R.id.mode_ip -> LocationMode.IP
            else -> LocationMode.AUTO
        }
        var lat = 0.0
        var lon = 0.0
        if (mode == LocationMode.COORDS) {
            lat = latField.text.toString().toDoubleOrNull()
                ?: run { Toast.makeText(this, R.string.cfg_invalid_coords, Toast.LENGTH_LONG).show(); return }
            lon = lonField.text.toString().toDoubleOrNull()
                ?: run { Toast.makeText(this, R.string.cfg_invalid_coords, Toast.LENGTH_LONG).show(); return }
        }

        WidgetPrefs.save(
            this, appWidgetId,
            WidgetConfig(
                serverUrl = url,
                mode = mode,
                name = nameField.text.toString().trim(),
                lat = lat,
                lon = lon,
            )
        )

        RainWidgetScheduler.enqueuePeriodic(this)
        RainWidgetScheduler.enqueueOneShot(this, appWidgetId)

        val resultValue = Intent().putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, appWidgetId)
        setResult(RESULT_OK, resultValue)
        finish()
    }
}
