# kotlinx.serialization keeps generated serializers via companion objects; the
# protobuf codec resolves them reflectively at class level, so the metadata and
# the synthetic serializer members must survive shrinking.
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.**

-keepclassmembers class com.syncapp.messenger.network.protocol.** {
    *** Companion;
}
-keepclasseswithmembers class com.syncapp.messenger.network.protocol.** {
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class com.syncapp.messenger.network.protocol.**$$serializer { *; }

# OkHttp ships optional platform hooks that are absent on Android.
-dontwarn okhttp3.internal.platform.**
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
-dontwarn org.openjsse.**
