package com.synapse.messenger.core

import javax.inject.Qualifier

/** The application-lifetime coroutine scope (survives every screen). */
@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class AppScope

@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class IoDispatcher
