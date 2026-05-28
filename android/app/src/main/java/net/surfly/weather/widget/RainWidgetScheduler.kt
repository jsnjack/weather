package net.surfly.weather.widget

import android.content.Context
import androidx.work.Constraints
import androidx.work.Data
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager

object RainWidgetScheduler {

    private const val PERIODIC_NAME = "rain-widget-periodic"
    private const val ONESHOT_PREFIX = "rain-widget-oneshot-"

    fun enqueueOneShot(context: Context, appWidgetId: Int) {
        val request = OneTimeWorkRequestBuilder<RainWidgetWorker>()
            .setInputData(Data.Builder().putInt(RainWidgetWorker.KEY_WIDGET_ID, appWidgetId).build())
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .build()
        WorkManager.getInstance(context)
            .enqueueUniqueWork(ONESHOT_PREFIX + appWidgetId, ExistingWorkPolicy.REPLACE, request)
    }

    fun cancelPeriodic(context: Context) {
        WorkManager.getInstance(context).cancelUniqueWork(PERIODIC_NAME)
    }
}
