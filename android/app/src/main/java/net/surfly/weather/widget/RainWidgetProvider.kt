package net.surfly.weather.widget

import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.Context
import android.os.Bundle

class RainWidgetProvider : AppWidgetProvider() {

    override fun onUpdate(
        context: Context,
        appWidgetManager: AppWidgetManager,
        appWidgetIds: IntArray,
    ) {
        RainWidgetScheduler.cancelPeriodic(context)
        appWidgetIds.forEach { RainWidgetScheduler.enqueueOneShot(context, it) }
    }

    override fun onEnabled(context: Context) {
        RainWidgetScheduler.cancelPeriodic(context)
    }

    override fun onDisabled(context: Context) {
        RainWidgetScheduler.cancelPeriodic(context)
    }

    override fun onDeleted(context: Context, appWidgetIds: IntArray) {
        appWidgetIds.forEach { WidgetPrefs.clear(context, it) }
    }

    override fun onAppWidgetOptionsChanged(
        context: Context,
        appWidgetManager: AppWidgetManager,
        appWidgetId: Int,
        newOptions: Bundle?,
    ) {
        RainWidgetScheduler.enqueueOneShot(context, appWidgetId)
    }
}
