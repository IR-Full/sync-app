package com.synapse.messenger.di

import android.content.Context
import com.synapse.messenger.BuildConfig
import com.synapse.messenger.core.AppScope
import com.synapse.messenger.core.IoDispatcher
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import java.util.concurrent.TimeUnit
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor

/**
 * Why Hilt and not Koin.
 *
 * This is a single-module Android app, and Hilt's graph is verified at compile
 * time: a missing binding is a build error rather than a crash on the screen that
 * happens to need it. It also owns the Android entry points this app actually
 * uses — `@AndroidEntryPoint` activities, `@HiltViewModel`, and injection into a
 * `FirebaseMessagingService`, which is constructed by the framework and would
 * otherwise need a service-locator lookup. Koin's advantages (no annotation
 * processing, Kotlin-Multiplatform reach) would matter if any of this were shared
 * with iOS; none of it is — `ios/` is a separate native target.
 */
@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    /**
     * The application-lifetime scope. The gateway connection, the sync coordinator
     * and every hot StateFlow live here: they outlive screens by design, because a
     * dropped socket must reconnect while the user is looking at a chat list that has
     * not been recomposed.
     */
    @Provides
    @Singleton
    @AppScope
    fun provideAppScope(): CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    @Provides
    @IoDispatcher
    fun provideIoDispatcher(): CoroutineDispatcher = Dispatchers.IO

    @Provides
    @Singleton
    fun provideContext(@ApplicationContext context: Context): Context = context

    /**
     * One OkHttp client for both the WebSocket and media transfers, so they share a
     * connection pool and a DNS cache.
     *
     * `pingInterval` is deliberately NOT set: the protocol has its own heartbeat
     * (the gateway PINGs on the negotiated interval and tears down a connection that
     * stops answering), and a second, invisible WebSocket-level ping would only add
     * radio wakeups without shortening any timeout that matters.
     */
    @Provides
    @Singleton
    fun provideOkHttpClient(): OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.SECONDS) // a WebSocket read blocks for as long as it likes
        .writeTimeout(30, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .apply {
            if (BuildConfig.DEBUG) {
                // BASIC, never BODY: the bodies here are protobuf frames carrying
                // credentials and message text.
                addInterceptor(
                    HttpLoggingInterceptor().apply { level = HttpLoggingInterceptor.Level.BASIC },
                )
            }
        }
        .build()
}
