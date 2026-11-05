package net.surfly.weather.widget

import android.Manifest
import android.appwidget.AppWidgetManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Bundle
import android.provider.Settings
import android.util.Log
import android.util.SizeF
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.TextView
import android.widget.Toast
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.updatePadding
import com.google.android.material.appbar.MaterialToolbar
import com.google.android.material.button.MaterialButtonToggleGroup
import com.google.android.material.card.MaterialCardView
import com.google.android.material.textfield.TextInputEditText
import com.google.android.material.textfield.TextInputLayout
import net.surfly.weather.widget.render.WidgetRenderer

/**
 * Widget settings: server URL and location mode. Opens when a widget is
 * added (if the launcher asks for it) and from the widget's long-press menu.
 * A preview of the widget, drawn from the last stored forecast, sits on top.
 */
class ConfigureActivity : AppCompatActivity() {

    private var appWidgetId = AppWidgetManager.INVALID_APPWIDGET_ID

    private lateinit var urlLayout: TextInputLayout
    private lateinit var urlField: TextInputEditText
    private lateinit var modeGroup: MaterialButtonToggleGroup
    private lateinit var modeHelp: TextView
    private lateinit var nameLayout: TextInputLayout
    private lateinit var nameField: TextInputEditText
    private lateinit var coordsRow: View
    private lateinit var latLayout: TextInputLayout
    private lateinit var latField: TextInputEditText
    private lateinit var lonLayout: TextInputLayout
    private lateinit var lonField: TextInputEditText
    private lateinit var permissionCard: MaterialCardView
    private lateinit var permissionStatus: TextView

    private val requestForeground = registerForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (!ok) Toast.makeText(this, R.string.cfg_location_denied, Toast.LENGTH_LONG).show()
        showPermission()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        setResult(RESULT_CANCELED)
        setContentView(R.layout.activity_configure)

        appWidgetId = intent?.extras?.getInt(AppWidgetManager.EXTRA_APPWIDGET_ID, AppWidgetManager.INVALID_APPWIDGET_ID)
            ?: AppWidgetManager.INVALID_APPWIDGET_ID
        if (appWidgetId == AppWidgetManager.INVALID_APPWIDGET_ID) {
            finish()
            return
        }

        val root = findViewById<View>(R.id.root)
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime())
            v.updatePadding(left = bars.left, top = bars.top, right = bars.right, bottom = bars.bottom)
            WindowInsetsCompat.CONSUMED
        }
        findViewById<MaterialToolbar>(R.id.toolbar).setNavigationOnClickListener { finish() }

        urlLayout = findViewById(R.id.url_layout)
        urlField = findViewById(R.id.url)
        modeGroup = findViewById(R.id.location_mode)
        modeHelp = findViewById(R.id.mode_help)
        nameLayout = findViewById(R.id.name_layout)
        nameField = findViewById(R.id.location_name)
        coordsRow = findViewById(R.id.coords_row)
        latLayout = findViewById(R.id.lat_layout)
        latField = findViewById(R.id.location_lat)
        lonLayout = findViewById(R.id.lon_layout)
        lonField = findViewById(R.id.location_lon)
        permissionCard = findViewById(R.id.permission_card)
        permissionStatus = findViewById(R.id.permission_status)

        val current = WidgetPrefs.load(this, appWidgetId)
        urlField.setText(current.serverUrl.ifBlank { WidgetPrefs.defaultUrl(this) })
        nameField.setText(current.name)
        if (current.lat != 0.0) latField.setText(current.lat.toString())
        if (current.lon != 0.0) lonField.setText(current.lon.toString())
        modeGroup.check(
            when (current.mode) {
                LocationMode.AUTO -> R.id.mode_auto
                LocationMode.NAME -> R.id.mode_name
                LocationMode.COORDS -> R.id.mode_coords
                LocationMode.IP -> R.id.mode_ip
            },
        )
        modeGroup.addOnButtonCheckedListener { _, checkedId, isChecked ->
            if (isChecked) showMode(checkedId, askForLocation = true)
        }
        showMode(modeGroup.checkedButtonId, askForLocation = false)

        findViewById<View>(R.id.permission_change).setOnClickListener { changePermission() }
        findViewById<View>(R.id.save).setOnClickListener { onSave() }
        showPreview()
    }

    override fun onResume() {
        super.onResume()
        // Returning from the system's app settings may have changed access.
        if (::permissionStatus.isInitialized) showPermission()
    }

    private fun showMode(checkedId: Int, askForLocation: Boolean) {
        nameLayout.visibility = if (checkedId == R.id.mode_name) View.VISIBLE else View.GONE
        coordsRow.visibility = if (checkedId == R.id.mode_coords) View.VISIBLE else View.GONE
        permissionCard.visibility = if (checkedId == R.id.mode_auto) View.VISIBLE else View.GONE
        modeHelp.setText(
            when (checkedId) {
                R.id.mode_name -> R.string.cfg_name_help
                R.id.mode_coords -> R.string.cfg_coords_help
                R.id.mode_ip -> R.string.cfg_ip_help
                else -> R.string.cfg_auto_help
            },
        )
        showPermission()
        if (checkedId == R.id.mode_auto && askForLocation && !granted(Manifest.permission.ACCESS_COARSE_LOCATION)) {
            requestForeground.launch(Manifest.permission.ACCESS_COARSE_LOCATION)
        }
    }

    private fun showPermission() {
        permissionStatus.setText(
            when {
                granted(Manifest.permission.ACCESS_BACKGROUND_LOCATION) -> R.string.cfg_permission_always
                granted(Manifest.permission.ACCESS_COARSE_LOCATION) -> R.string.cfg_permission_foreground
                else -> R.string.cfg_permission_none
            },
        )
    }

    /**
     * Auto mode needs a current fix during background refreshes, which
     * requires "Allow all the time". Android only offers that choice in the
     * app's system settings, and only after foreground access is granted.
     */
    private fun changePermission() {
        if (!granted(Manifest.permission.ACCESS_COARSE_LOCATION)) {
            requestForeground.launch(Manifest.permission.ACCESS_COARSE_LOCATION)
            return
        }
        Toast.makeText(this, R.string.cfg_background_denied, Toast.LENGTH_LONG).show()
        startActivity(
            Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS)
                .setData(Uri.fromParts("package", packageName, null)),
        )
    }

    private fun granted(perm: String): Boolean =
        ContextCompat.checkSelfPermission(this, perm) == PackageManager.PERMISSION_GRANTED

    /** Draws the widget from this widget's stored forecast, or the newest one for a new widget. */
    private fun showPreview() {
        val id = if (WidgetStore.loadGlance(this, appWidgetId) != null) appWidgetId else WidgetStore.newestWidgetId(this)
        val content = id?.let { WidgetUpdater.storedContent(this, it, pill = null) } ?: return
        val host = findViewById<FrameLayout>(R.id.preview_host)
        val views = WidgetRenderer.build(this, content, listOf(SizeF(PREVIEW_W, PREVIEW_H)))
        val density = resources.displayMetrics.density
        // The application context inflates plain framework views; this
        // activity's AppCompat inflater would swap in AppCompatImageView,
        // which RemoteViews refuses.
        val widget = runCatching { views.apply(applicationContext, host) }
            .onFailure { Log.w("RainWidget", "Settings preview failed", it) }
            .getOrNull() ?: return
        host.addView(widget, FrameLayout.LayoutParams((PREVIEW_W * density).toInt(), (PREVIEW_H * density).toInt()))
        findViewById<View>(R.id.preview_card).visibility = View.VISIBLE
        // Scale the 4×2 widget to the card's width, keeping its proportions.
        host.post {
            val scale = (host.width / (PREVIEW_W * density)).coerceAtMost(1f)
            widget.pivotX = 0f
            widget.pivotY = 0f
            widget.scaleX = scale
            widget.scaleY = scale
            host.updateHeight((PREVIEW_H * density * scale).toInt())
        }
    }

    private fun ViewGroup.updateHeight(px: Int) {
        layoutParams = layoutParams.apply { height = px }
    }

    private fun onSave() {
        urlLayout.error = null
        nameLayout.error = null
        latLayout.error = null
        lonLayout.error = null
        val url = urlField.text.toString().trim()
        if (!url.startsWith("http://") && !url.startsWith("https://")) {
            urlLayout.error = getString(R.string.cfg_invalid_url)
            return
        }
        val mode = when (modeGroup.checkedButtonId) {
            R.id.mode_name -> LocationMode.NAME
            R.id.mode_coords -> LocationMode.COORDS
            R.id.mode_ip -> LocationMode.IP
            else -> LocationMode.AUTO
        }
        val name = nameField.text.toString().trim()
        if (mode == LocationMode.NAME && name.isEmpty()) {
            nameLayout.error = getString(R.string.cfg_invalid_name)
            return
        }
        var lat = 0.0
        var lon = 0.0
        if (mode == LocationMode.COORDS) {
            val la = latField.text.toString().toDoubleOrNull()
            val lo = lonField.text.toString().toDoubleOrNull()
            if (la == null) latLayout.error = getString(R.string.cfg_invalid_coords)
            if (lo == null) lonLayout.error = getString(R.string.cfg_invalid_coords)
            if (la == null || lo == null) return
            lat = la
            lon = lo
        }

        WidgetPrefs.save(this, appWidgetId, WidgetConfig(serverUrl = url, mode = mode, name = name, lat = lat, lon = lon))
        RainWidgetScheduler.enqueuePeriodic(this)
        RainWidgetScheduler.enqueueOneShot(this, appWidgetId, expedited = true)

        setResult(RESULT_OK, Intent().putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, appWidgetId))
        finish()
    }

    private companion object {
        const val PREVIEW_W = 384f
        const val PREVIEW_H = 220f
    }
}
