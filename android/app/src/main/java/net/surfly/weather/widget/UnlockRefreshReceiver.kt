package net.surfly.weather.widget

import android.appwidget.AppWidgetManager
import android.content.BroadcastReceiver
import android.content.ComponentName
import android.content.Context
import android.content.Intent

/**
 * Fires a one-shot widget refresh when the user dismisses the keyguard
 * (unlocks the phone), throttled so it can't run more often than every
 * [WidgetPrefs.UNLOCK_THROTTLE_MS]. Pairs with the periodic WorkManager
 * job in [RainWidgetScheduler] — the periodic job is the floor, this
 * receiver lets the widget catch up faster when you actually look at
 * the phone.
 *
 * Registered in the manifest for `android.intent.action.USER_PRESENT`.
 */
class UnlockRefreshReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_USER_PRESENT) return

        val sinceLast = System.currentTimeMillis() - WidgetPrefs.lastRefreshMs(context)
        if (sinceLast < WidgetPrefs.UNLOCK_THROTTLE_MS) return

        val mgr = AppWidgetManager.getInstance(context)
        val ids = mgr.getAppWidgetIds(ComponentName(context, RainWidgetProvider::class.java))
        for (id in ids) {
            RainWidgetScheduler.enqueueOneShot(context, id)
        }
    }
}
