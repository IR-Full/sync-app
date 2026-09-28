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
    namespace = "com.syncapp.messenger"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.syncapp.messenger"
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
            // No pins in development: the gateway there is plain ws:// on an emulator
            // loopback, so there is no certificate to pin and nothing a pin would
            // protect. An empty list means "use ordinary CA validation".
            buildConfigField("String", "TLS_PINS", "\"\"")
            buildConfigField("long", "TLS_PINS_EXPIRE_AT", "0L")
            resValue("string", "app_name", "syncapp Dev")
        }
        create("staging") {
            dimension = "environment"
            applicationIdSuffix = ".staging"
            versionNameSuffix = "-staging"
            buildConfigField("String", "GATEWAY_URL", "\"wss://staging.syncapp.example/ws\"")
            buildConfigField("String", "MEDIA_BASE_URL", "\"https://staging.syncapp.example\"")
            buildConfigField("String", "ENVIRONMENT_NAME", "\"staging\"")
            buildConfigField("boolean", "ALLOW_ENDPOINT_OVERRIDE", "true")
            // Staging deliberately unpinned: its whole purpose is to be repointed, and
            // a pin there would make every certificate change an app release.
            buildConfigField("String", "TLS_PINS", "\"\"")
            buildConfigField("long", "TLS_PINS_EXPIRE_AT", "0L")
            resValue("string", "app_name", "syncapp Staging")
        }
        create("production") {
            dimension = "environment"
            buildConfigField("String", "GATEWAY_URL", "\"wss://syncapp.example/ws\"")
            buildConfigField("String", "MEDIA_BASE_URL", "\"https://syncapp.example\"")
            buildConfigField("String", "ENVIRONMENT_NAME", "\"production\"")
            buildConfigField("boolean", "ALLOW_ENDPOINT_OVERRIDE", "false")
            /*
             * TLS pins for the production gateway: comma-separated base64 SHA-256 of the
             * subject public key info.
             *
             * SPKI rather than certificate hashes, because a certificate pin breaks on
             * every renewal — ninety days with an automated issuer — and therefore gets
             * removed after the first outage it causes.
             *
             * TWO are required and the code refuses a single one. The second is an
             * offline BACKUP key that is never served; it exists so a key rotation is a
             * config change rather than a forced update of every installed copy. A lone
             * pin is worse than none: it looks like protection and its failure mode is
             * an app that cannot connect at all.
             *
             * Empty here because this repository does not hold the real deployment's
             * keys — filled by the release pipeline from
             *   openssl s_client -connect host:443 | openssl x509 -pubkey -noout \
             *     | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary \
             *     | openssl enc -base64
             * With it empty the app falls back to ordinary CA validation, which is what
             * a self-hosted deployment needs anyway: an operator running their own
             * gateway cannot know our pins.
             */
            buildConfigField("String", "TLS_PINS", "\"\"")
            /*
             * When the pins stop being enforced (unix millis; 0 = never).
             *
             * Past it the app falls back to CA validation rather than failing closed. A
             * pin with no end date is one nobody revisits, and the deliberately weaker
             * choice here is the right one: a stale pin that blocks every connection
             * turns a forgotten config entry into a dead app, and for a build nobody is
             * maintaining, CA validation is far better than nothing.
             */
            buildConfigField("long", "TLS_PINS_EXPIRE_AT", "0L")
            resValue("string", "app_name", "syncapp")
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

    /*
     * The app has an in-app language switcher, so the bundle must carry every
     * locale rather than only the device's.
     *
     * Play splits an App Bundle by language by default and delivers just the
     * matching resources; a runtime switch to a language that was never
     * installed then falls back silently, which reads as the setting not
     * working. The alternative is the Play Core language-download API — more
     * moving parts than a messenger with two locales needs.
     */
    bundle {
        language {
            enableSplit = false
        }
    }

    /*
     * Android Lint, run as a CI gate.
     *
     * Chosen over detekt/ktlint because it ships with AGP — no plugin to add, no
     * version to keep in step with Kotlin — and because it catches the things
     * that are specific to this platform and invisible to a generic Kotlin
     * linter: a missing permission, a leaked Context, an API used above minSdk,
     * a Composable that breaks the conventions the compiler relies on.
     *
     * warningsAsErrors, because a warning nobody has to act on is a warning
     * everybody scrolls past. The two disabled checks below are the ones that
     * would make that setting untenable, and each is disabled for a reason
     * rather than to get to zero.
     */
    lint {
        warningsAsErrors = true
        abortOnError = true

        disable += setOf(
            // Dependabot now opens the upgrade PRs (.github/dependabot.yml), so
            // this only duplicates them — and it fails the build for a version
            // released after the commit was written, which makes a green build
            // go red without anyone touching the code.
            "GradleDependency",
            "NewerVersionAvailable",
            // local.properties is developer-local and gitignored, so this never
            // fires in CI and always fires on Windows. Failing a local lint run
            // over a path separator in a file the build itself generated is
            // noise, not a finding.
            "PropertyEscape",
            // Same reasoning as GradleDependency: Dependabot owns the upgrade,
            // and a build that goes red because a new AGP shipped overnight
            // teaches people to ignore the linter.
            "AndroidGradlePluginVersion",
        )

        // A machine-readable report for the CI summary; the HTML one is
        // unreadable in a log.
        textReport = true
        xmlReport = true
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
    implementation(libs.sqlcipher.android)
    ksp(libs.androidx.room.compiler)

    implementation(libs.hilt.android)
    implementation(libs.hilt.navigation.compose)
    ksp(libs.hilt.compiler)

    implementation(libs.androidx.work.runtime)
    implementation(libs.androidx.hilt.work)
    ksp(libs.androidx.hilt.compiler)

    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.protobuf)
    implementation(libs.kotlinx.serialization.json)

    implementation(libs.okhttp)
    implementation(libs.okhttp.logging)
    implementation(libs.coil.compose)

    // Secret chats. See the note in gradle/libs.versions.toml for why this is
    // needed at all and why the lightweight API is used.
    implementation(libs.bouncycastle)

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
