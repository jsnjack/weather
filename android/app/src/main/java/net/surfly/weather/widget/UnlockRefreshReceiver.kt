package net.surfly.weather.widget

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log

/**
 * Fires an expedited one-shot widget refresh when the user dismisses the
 * keyguard (unlocks the phone). [RainWidgetApp] registers this receiver
 * dynamically, which is the Android 8+ compliant path while the app process is
 * alive.
 */
class UnlockRefreshReceiver : BroadcastReceiver() {
    companion object {
        private const val TAG = "RainWidget"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_USER_PRESENT) return

        val count = RainWidgetScheduler.enqueueAllWidgets(context, expedited = true)
        Log.i(TAG, "USER_PRESENT enqueued expedited refresh for $count widget(s)")
    }
}
