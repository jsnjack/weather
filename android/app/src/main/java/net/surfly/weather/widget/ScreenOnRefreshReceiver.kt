package net.surfly.weather.widget

import android.appwidget.AppWidgetManager
import android.content.BroadcastReceiver
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.util.Log

/**
 * Receives the [ScreenOnRefresh] alarm and refreshes every widget whose
 * forecast is stale. While the screen is on the alarm repeats about every
 * five minutes; after the screen was off, it arrives as the screen turns on.
 */
class ScreenOnRefreshReceiver : BroadcastReceiver() {
    companion object {
        private const val TAG = "RainWidget"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ScreenOnRefresh.ACTION) return
        val ids = AppWidgetManager.getInstance(context)
            .getAppWidgetIds(ComponentName(context, RainWidgetProvider::class.java))
        if (ids.isEmpty()) {
            ScreenOnRefresh.cancel(context)
            return
        }
        val now = System.currentTimeMillis()
        val stale = ids.filter { RefreshPolicy.isStale(WidgetStore.glanceSavedAt(context, it), now) }
        stale.forEach { RainWidgetScheduler.enqueueOneShot(context, it, expedited = true) }
        Log.i(TAG, "screen-on alarm: refreshing ${stale.size} of ${ids.size} widget(s)")
    }
}
