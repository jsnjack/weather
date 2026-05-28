package net.surfly.weather.widget

import android.appwidget.AppWidgetManager
import android.content.BroadcastReceiver
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.util.Log

/**
 * Fires a one-shot widget refresh when the user dismisses the keyguard
 * (unlocks the phone). This is the only automatic refresh path; otherwise the
 * widget updates on explicit events like manual refresh, configure/save,
 * resize, install/update, or launcher APPWIDGET_UPDATE.
 *
 * Registered in the manifest for `android.intent.action.USER_PRESENT`.
 */
class UnlockRefreshReceiver : BroadcastReceiver() {
    companion object {
        private const val TAG = "RainWidget"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_USER_PRESENT) return

        val mgr = AppWidgetManager.getInstance(context)
        val ids = mgr.getAppWidgetIds(ComponentName(context, RainWidgetProvider::class.java))
        for (id in ids) {
            RainWidgetScheduler.enqueueOneShot(context, id)
        }
        Log.i(TAG, "USER_PRESENT enqueued refresh for ${ids.size} widget(s)")
    }
}
