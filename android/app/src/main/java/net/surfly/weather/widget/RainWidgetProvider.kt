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
        RainWidgetScheduler.enqueuePeriodic(context)
        appWidgetIds.forEach { RainWidgetScheduler.enqueueOneShot(context, it) }
    }

    override fun onEnabled(context: Context) {
        RainWidgetScheduler.enqueuePeriodic(context)
    }

    override fun onDisabled(context: Context) {
        RainWidgetScheduler.cancelPeriodic(context)
    }

    override fun onDeleted(context: Context, appWidgetIds: IntArray) {
        appWidgetIds.forEach {
            WidgetPrefs.clear(context, it)
            WidgetStore.clear(context, it)
        }
    }

    override fun onAppWidgetOptionsChanged(
        context: Context,
        appWidgetManager: AppWidgetManager,
        appWidgetId: Int,
        newOptions: Bundle?,
    ) {
        // A resize only needs a redraw at the new sizes; the stored forecast
        // and map are enough, so no network round-trip.
        RainWidgetScheduler.enqueueRender(context, appWidgetId)
    }
}
