package net.surfly.weather.widget

import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.Data
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.OutOfQuotaPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import java.util.concurrent.TimeUnit

object RainWidgetScheduler {

    private const val PERIODIC_NAME = "rain-widget-periodic"
    private const val ONESHOT_PREFIX = "rain-widget-oneshot-"
    private const val PERIODIC_MINUTES = 15L

    fun enqueuePeriodic(context: Context) {
        val request = PeriodicWorkRequestBuilder<RainWidgetWorker>(PERIODIC_MINUTES, TimeUnit.MINUTES)
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(context)
            .enqueueUniquePeriodicWork(PERIODIC_NAME, ExistingPeriodicWorkPolicy.UPDATE, request)
    }

    fun enqueueAllWidgets(context: Context, expedited: Boolean = false): Int {
        val mgr = AppWidgetManager.getInstance(context)
        val ids = mgr.getAppWidgetIds(ComponentName(context, RainWidgetProvider::class.java))
        ids.forEach { enqueueOneShot(context, it, expedited) }
        return ids.size
    }

    fun enqueueOneShot(context: Context, appWidgetId: Int, expedited: Boolean = false) {
        val builder = OneTimeWorkRequestBuilder<RainWidgetWorker>()
            .setInputData(Data.Builder().putInt(RainWidgetWorker.KEY_WIDGET_ID, appWidgetId).build())
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
        if (expedited) {
            builder.setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
        }
        val request = builder
            .build()
        WorkManager.getInstance(context)
            .enqueueUniqueWork(ONESHOT_PREFIX + appWidgetId, ExistingWorkPolicy.REPLACE, request)
    }

    fun cancelPeriodic(context: Context) {
        WorkManager.getInstance(context).cancelUniqueWork(PERIODIC_NAME)
    }
}
