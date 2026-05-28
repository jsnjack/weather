package net.surfly.weather.widget

import android.content.Context
import androidx.work.Constraints
import androidx.work.Data
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import java.util.concurrent.TimeUnit

object RainWidgetScheduler {

    private const val PERIODIC_NAME = "rain-widget-periodic"
    private const val ONESHOT_PREFIX = "rain-widget-oneshot-"

    fun enqueuePeriodic(context: Context) {
        val minutes = WidgetPrefs.refreshMinutes(context).coerceAtLeast(15).toLong()
        val request = PeriodicWorkRequestBuilder<RainWidgetWorker>(minutes, TimeUnit.MINUTES)
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .setBackoffCriteria(androidx.work.BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(context)
            .enqueueUniquePeriodicWork(PERIODIC_NAME, ExistingPeriodicWorkPolicy.UPDATE, request)
    }

    fun enqueueOneShot(context: Context, appWidgetId: Int) {
        val request = OneTimeWorkRequestBuilder<RainWidgetWorker>()
            .setInputData(Data.Builder().putInt(RainWidgetWorker.KEY_WIDGET_ID, appWidgetId).build())
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .build()
        WorkManager.getInstance(context)
            .enqueueUniqueWork(ONESHOT_PREFIX + appWidgetId, ExistingWorkPolicy.REPLACE, request)
    }

    fun cancelAll(context: Context) {
        WorkManager.getInstance(context).cancelUniqueWork(PERIODIC_NAME)
    }
}
