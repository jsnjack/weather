package net.surfly.weather.widget.render

import android.graphics.Bitmap
import android.graphics.BitmapShader
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Shader

internal object RadarRenderer {
    private const val CORNER_DP = 16f

    fun rounded(source: Bitmap, displayedWidthDp: Int): Bitmap {
        val result = Bitmap.createBitmap(source.width, source.height, Bitmap.Config.ARGB_8888)
        val radius = (CORNER_DP * source.width / displayedWidthDp.coerceAtLeast(1))
            .coerceAtMost(source.width / 2f)
        val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            shader = BitmapShader(source, Shader.TileMode.CLAMP, Shader.TileMode.CLAMP)
        }
        Canvas(result).drawRoundRect(0f, 0f, source.width.toFloat(), source.height.toFloat(), radius, radius, paint)
        return result
    }
}
