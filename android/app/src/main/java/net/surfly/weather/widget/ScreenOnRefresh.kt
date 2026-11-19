package net.surfly.weather.widget

import android.app.AlarmManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.SystemClock

/**
 * Refreshes the widget when the screen turns on.
 *
 * Android 14+ holds context-registered broadcasts such as `USER_PRESENT` for a
 * cached app until it becomes active again (`bcast_core_defer_until_active`),
 * and the widget's process is frozen seconds after every refresh. An unlock
 * receiver therefore ran only when the next periodic job woke the app, up to
 * 15 minutes after the unlock. A non-wakeup alarm is held while the screen is
 * off and delivered as soon as it turns on, to [ScreenOnRefreshReceiver], a
 * manifest receiver the system starts even when the process is frozen.
 */
object ScreenOnRefresh {
    const val ACTION = "net.surfly.weather.widget.ACTION_SCREEN_ON_REFRESH"
    private const val INTERVAL_MS = 5 * 60_000L

    /** Arms the alarm, replacing an earlier one. */
    fun arm(ctx: Context) {
        val intent = pendingIntent(ctx, PendingIntent.FLAG_UPDATE_CURRENT) ?: return
        alarms(ctx).setInexactRepeating(
            // Non-wakeup: never wakes a sleeping phone, delivered when the screen turns on.
            AlarmManager.ELAPSED_REALTIME,
            SystemClock.elapsedRealtime() + INTERVAL_MS,
            INTERVAL_MS,
            intent,
        )
    }

    /** Arms the alarm unless it already is: a reboot or force-stop clears it. */
    fun ensureArmed(ctx: Context) {
        if (pendingIntent(ctx, PendingIntent.FLAG_NO_CREATE) == null) arm(ctx)
    }

    fun cancel(ctx: Context) {
        val intent = pendingIntent(ctx, PendingIntent.FLAG_NO_CREATE) ?: return
        alarms(ctx).cancel(intent)
        intent.cancel()
    }

    private fun alarms(ctx: Context): AlarmManager = ctx.getSystemService(AlarmManager::class.java)

    private fun pendingIntent(ctx: Context, flag: Int): PendingIntent? = PendingIntent.getBroadcast(
        ctx,
        0,
        Intent(ctx, ScreenOnRefreshReceiver::class.java)
            .setAction(ACTION)
            // Foreground priority, so the broadcast isn't queued behind others for a cached app.
            .addFlags(Intent.FLAG_RECEIVER_FOREGROUND),
        flag or PendingIntent.FLAG_IMMUTABLE,
    )
}
