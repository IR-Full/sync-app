package com.synapse.messenger.presentation.theme

import android.content.Context
import android.content.res.Configuration
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import com.synapse.messenger.datastore.LanguageMode
import com.synapse.messenger.datastore.ThemeMode
import java.util.Locale

/**
 * The palette is defined rather than inherited from dynamic color: a messenger's
 * bubbles carry meaning (mine / theirs, sent / failed) and those relationships have
 * to hold in both schemes, which a wallpaper-derived palette cannot promise.
 */
private val Blue40 = Color(0xFF1F6FEB)
private val Blue80 = Color(0xFF8AB4F8)
private val Teal40 = Color(0xFF00796B)
private val Teal80 = Color(0xFF4DB6AC)
private val Red40 = Color(0xFFBA1A1A)
private val Red80 = Color(0xFFFFB4AB)

private val LightScheme = lightColorScheme(
    primary = Blue40,
    onPrimary = Color.White,
    primaryContainer = Color(0xFFD7E3FF),
    onPrimaryContainer = Color(0xFF001B3D),
    secondary = Teal40,
    onSecondary = Color.White,
    background = Color(0xFFFBFCFF),
    onBackground = Color(0xFF1A1C1E),
    surface = Color(0xFFFBFCFF),
    onSurface = Color(0xFF1A1C1E),
    surfaceVariant = Color(0xFFE1E2EC),
    onSurfaceVariant = Color(0xFF44474F),
    error = Red40,
    onError = Color.White,
)

private val DarkScheme = darkColorScheme(
    primary = Blue80,
    onPrimary = Color(0xFF002F65),
    primaryContainer = Color(0xFF17457F),
    onPrimaryContainer = Color(0xFFD7E3FF),
    secondary = Teal80,
    onSecondary = Color(0xFF00382F),
    background = Color(0xFF111318),
    onBackground = Color(0xFFE2E2E6),
    surface = Color(0xFF111318),
    onSurface = Color(0xFFE2E2E6),
    surfaceVariant = Color(0xFF44474F),
    onSurfaceVariant = Color(0xFFC4C6D0),
    error = Red80,
    onError = Color(0xFF690005),
)

private val AppTypography = Typography(
    titleLarge = Typography().titleLarge.copy(fontWeight = FontWeight.SemiBold),
    titleMedium = Typography().titleMedium.copy(fontWeight = FontWeight.SemiBold),
    bodyLarge = Typography().bodyLarge.copy(fontSize = 16.sp, lineHeight = 22.sp),
    labelSmall = Typography().labelSmall.copy(fontSize = 11.sp),
)

@Composable
fun SynapseTheme(
    themeMode: ThemeMode,
    language: LanguageMode,
    content: @Composable () -> Unit,
) {
    val dark = when (themeMode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
    ProvideAppLocale(language) {
        MaterialTheme(
            colorScheme = if (dark) DarkScheme else LightScheme,
            typography = AppTypography,
            content = content,
        )
    }
}

/**
 * Applies the in-app language.
 *
 * Done by overriding the composition's Context and Configuration rather than
 * through `AppCompatDelegate.setApplicationLocales`: this app has no AppCompat
 * dependency (it is pure Compose), and the per-app system locale API only exists
 * from API 33. Overriding here works on every supported version and takes effect
 * without recreating the Activity.
 */
@Composable
private fun ProvideAppLocale(language: LanguageMode, content: @Composable () -> Unit) {
    val baseContext = LocalContext.current
    val baseConfiguration = LocalConfiguration.current
    val locale = language.toLocale()

    if (locale == null) {
        content()
        return
    }

    val localizedContext = remember(locale, baseConfiguration) {
        localizedContext(baseContext, baseConfiguration, locale)
    }
    CompositionLocalProvider(
        LocalContext provides localizedContext,
        LocalConfiguration provides localizedContext.resources.configuration,
        content = content,
    )
}

private fun LanguageMode.toLocale(): Locale? = when (this) {
    LanguageMode.SYSTEM -> null
    LanguageMode.RUSSIAN -> Locale.forLanguageTag("ru")
    LanguageMode.ENGLISH -> Locale.forLanguageTag("en")
}

private fun localizedContext(
    context: Context,
    configuration: Configuration,
    locale: Locale,
): Context {
    val localized = Configuration(configuration).apply { setLocale(locale) }
    return context.createConfigurationContext(localized)
}
