package net.surfly.weather.widget

import android.app.Application
import android.content.Intent
import android.content.IntentFilter
import androidx.core.content.ContextCompat

/**
 * Registers the unlock receiver while the process is alive. Dark mode needs
 * no refresh here: every widget update carries light and dark images, and
 * the launcher switches between them by itself.
 */
class RainWidgetApp : Application() {
    private val unlockReceiver = UnlockRefreshReceiver()

    override fun onCreate() {
        super.onCreate()
        ContextCompat.registerReceiver(
            this,
            unlockReceiver,
            IntentFilter(Intent.ACTION_USER_PRESENT),
            ContextCompat.RECEIVER_NOT_EXPORTED,
        )
    }
}
