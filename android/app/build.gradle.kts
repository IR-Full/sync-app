import java.io.FileInputStream
import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
    alias(libs.plugins.hilt)
}

// Firebase is optional: the push path needs a relay in front of FCM (see README),
// and the plugin hard-fails without google-services.json. Applying it only when
// the file exists keeps the project buildable out of the box while a real
// deployment just drops the file in and gets push.
val googleServicesFile = file("google-services.json")
val pushEnabled = googleServicesFile.exists()
if (pushEnabled) {
    apply(plugin = libs.plugins.google.services.get().pluginId)
}

// Signing config for release builds, read from keystore.properties when present.
val keystoreProperties = Properties().apply {
    val f = rootProject.file("keystore.properties")
    if (f.exists()) load(FileInputStream(f))
}

android {
    namespace = "com.synapse.messenger"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.synapse.messenger"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        buildConfigField("boolean", "PUSH_ENABLED", pushEnabled.toString())
    }

    signingConfigs {
        if (keystoreProperties.containsKey("storeFile")) {
            create("release") {
                storeFile = file(keystoreProperties.getProperty("storeFile"))
                storePassword = keystoreProperties.getProperty("storePassword")
                keyAlias = keystoreProperties.getProperty("keyAlias")
                keyPassword = keystoreProperties.getProperty("keyPassword")
            }
        }
    }

    // Environments. The gateway address is never hardcoded in Kotlin: every
    // flavor supplies its own endpoints, and a debug build can still override
    // them at runtime from the settings screen (see EnvironmentStore).
    flavorDimensions += "environment"
    productFlavors {
        create("development") {
            dimension = "environment"
            applicationIdSuffix = ".dev"
            versionNameSuffix = "-dev"
            // 10.0.2.2 is the host machine as seen from the Android emulator.
            buildConfigField("String", "GATEWAY_URL", "\"ws://10.0.2.2:8080/ws\"")
            buildConfigField("String", "MEDIA_BASE_URL", "\"http://10.0.2.2:8080\"")
            buildConfigField("String", "ENVIRONMENT_NAME", "\"development\"")
            buildConfigField("boolean", "ALLOW_ENDPOINT_OVERRIDE", "true")
            resValue("string", "app_name", "Synapse Dev")
        }
        create("staging") {
            dimension = "environment"
            applicationIdSuffix = ".staging"
            versionNameSuffix = "-staging"
            buildConfigField("String", "GATEWAY_URL", "\"wss://staging.synapse.example/ws\"")
            buildConfigField("String", "MEDIA_BASE_URL", "\"https://staging.synapse.example\"")
            buildConfigField("String", "ENVIRONMENT_NAME", "\"staging\"")
            buildConfigField("boolean", "ALLOW_ENDPOINT_OVERRIDE", "true")
            resValue("string", "app_name", "Synapse Staging")
        }
        create("production") {
            dimension = "environment"
            buildConfigField("String", "GATEWAY_URL", "\"wss://synapse.example/ws\"")
            buildConfigField("String", "MEDIA_BASE_URL", "\"https://synapse.example\"")
            buildConfigField("String", "ENVIRONMENT_NAME", "\"production\"")
            buildConfigField("boolean", "ALLOW_ENDPOINT_OVERRIDE", "false")
            resValue("string", "app_name", "Synapse")
        }
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.findByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        resources.excludes += setOf("/META-INF/{AL2.0,LGPL2.1}")
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
        freeCompilerArgs.addAll(
            // @ProtoNumber and the whole protobuf format are still opt-in APIs, and the
            // protocol layer is built on them by necessity — the bodies ARE protobuf.
            "-opt-in=kotlinx.serialization.ExperimentalSerializationApi",
            // flatMapLatest: the repositories key their Room flows on the current user
            // and the currently resolved chat, both of which legitimately change.
            "-opt-in=kotlinx.coroutines.ExperimentalCoroutinesApi",
        )
    }
}

ksp {
    arg("room.schemaLocation", "$projectDir/schemas")
    arg("room.generateKotlin", "true")
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.splashscreen)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.process)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.graphics)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    debugImplementation(libs.compose.ui.tooling)

    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.datastore.preferences)

    implementation(libs.androidx.room.runtime)
    implementation(libs.androidx.room.ktx)
    ksp(libs.androidx.room.compiler)

    implementation(libs.hilt.android)
    implementation(libs.hilt.navigation.compose)
    ksp(libs.hilt.compiler)

    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.protobuf)
    implementation(libs.kotlinx.serialization.json)

    implementation(libs.okhttp)
    implementation(libs.okhttp.logging)
    implementation(libs.coil.compose)

    implementation(platform(libs.firebase.bom))
    implementation(libs.firebase.messaging)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.turbine)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.room.testing)
    testImplementation(libs.androidx.test.junit)

    androidTestImplementation(libs.androidx.test.junit)
    androidTestImplementation(libs.androidx.test.espresso)
}
