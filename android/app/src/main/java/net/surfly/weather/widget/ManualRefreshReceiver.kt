package net.surfly.weather.widget

import android.appwidget.AppWidgetManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Handles the "tap the timestamp pill to refresh" action. Kept separate from
 * [RainWidgetProvider] and declared `android:exported="false"` so only this
 * app can trigger a refresh: the provider itself must stay exported to receive
 * the system's APPWIDGET_UPDATE broadcast, but the manual-refresh action does
 * not need to be reachable by other apps. The in-app PendingIntent targets
 * this receiver explicitly, which is allowed for a same-UID component even
 * though it is not exported.
 */
class ManualRefreshReceiver : BroadcastReceiver() {

    companion object {
        const val ACTION_MANUAL_REFRESH = "net.surfly.weather.widget.ACTION_MANUAL_REFRESH"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_MANUAL_REFRESH) return
        val id = intent.getIntExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, AppWidgetManager.INVALID_APPWIDGET_ID)
        if (id != AppWidgetManager.INVALID_APPWIDGET_ID) {
            RainWidgetScheduler.enqueueOneShot(context, id)
        }
    }
}
