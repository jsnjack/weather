# kotlinx.serialization keep rules
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.AnnotationsKt

-keep,includedescriptorclasses class net.surfly.weather.widget.**$$serializer { *; }
-keepclassmembers class net.surfly.weather.widget.** {
    *** Companion;
}
-keepclasseswithmembers class net.surfly.weather.widget.** {
    kotlinx.serialization.KSerializer serializer(...);
}
