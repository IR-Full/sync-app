package com.syncapp.messenger

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import androidx.navigation.navDeepLink
import com.syncapp.messenger.presentation.auth.AuthScreen
import com.syncapp.messenger.presentation.chat.ChatScreen
import com.syncapp.messenger.presentation.chats.ChatListScreen
import com.syncapp.messenger.presentation.components.LoadingState
import com.syncapp.messenger.presentation.navigation.Routes
import com.syncapp.messenger.presentation.newchat.NewChatScreen
import com.syncapp.messenger.presentation.premium.PremiumScreen
import com.syncapp.messenger.presentation.privacy.PrivacyScreen
import com.syncapp.messenger.presentation.security.SecurityScreen
import com.syncapp.messenger.presentation.sessions.SessionsScreen
import com.syncapp.messenger.presentation.settings.SettingsScreen
import com.syncapp.messenger.presentation.theme.SyncAppTheme
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.MutableStateFlow

@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    private val requestNotifications = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { /* Declined is fine: the app works, it just stays quiet. */ }

    /**
     * Deep links that arrive while the activity is already alive.
     *
     * The launch intent is handled by NavHost on its own, but this activity is
     * `singleTask`: tapping a notification while the app is open does not create a
     * new activity, it delivers here — and an intent nobody forwards to the
     * NavController is a tap that appears to do nothing. That is the common case
     * for a messenger, not the rare one.
     */
    private val newIntents = MutableStateFlow<Intent?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        // Keep conversations out of screenshots, screen recordings and the thumbnail
        // the system keeps for the recent-apps switcher. Set on the window rather
        // than per screen: every screen in this app can show message content, and a
        // flag that has to be remembered on each new screen eventually is not.
        window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && BuildConfig.PUSH_ENABLED) {
            requestNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }

        setContent { SyncAppApp(newIntents = newIntents) }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // Keep getIntent() current as well, so anything that re-reads it after a
        // configuration change sees the intent that actually brought us forward.
        setIntent(intent)
        newIntents.value = intent
    }
}

@Composable
private fun SyncAppApp(
    newIntents: MutableStateFlow<Intent?>,
    viewModel: RootViewModel = hiltViewModel(),
) {
    val settings by viewModel.settings.collectAsStateWithLifecycle()
    val session by viewModel.session.collectAsStateWithLifecycle()
    val restored by viewModel.restored.collectAsStateWithLifecycle()

    SyncAppTheme(themeMode = settings.theme, language = settings.language) {
        // Until the stored session has been read once, neither destination is right:
        // showing login would flash it for anyone already signed in.
        if (!restored) {
            LoadingState()
            return@SyncAppTheme
        }

        val navController = rememberNavController()

        // Hand a notification tap that arrived while we were already running to the
        // NavController, which resolves it against the same deepLinks declared below.
        // Only while signed in: a deep link into a chat from the login screen would
        // navigate past it.
        val pendingIntent by newIntents.collectAsStateWithLifecycle()
        LaunchedEffect(pendingIntent, session != null) {
            val intent = pendingIntent ?: return@LaunchedEffect
            if (session == null) return@LaunchedEffect
            navController.handleDeepLink(intent)
            newIntents.value = null
        }

        // Session state drives navigation, so a login, a logout and a session revoked
        // by the server all take the same path — the alternative is three call sites
        // that must each remember to navigate.
        LaunchedEffect(session != null) {
            val target = if (session != null) Routes.CHATS else Routes.AUTH
            navController.navigate(target) {
                popUpTo(navController.graph.id) { inclusive = true }
            }
        }

        NavHost(
            navController = navController,
            startDestination = if (session != null) Routes.CHATS else Routes.AUTH,
        ) {
            composable(Routes.AUTH) { AuthScreen() }

            composable(Routes.CHATS) {
                ChatListScreen(
                    onOpenChat = { chatId -> navController.navigate(Routes.chatById(chatId)) },
                    onNewChat = { navController.navigate(Routes.NEW_CHAT) },
                    onOpenSettings = { navController.navigate(Routes.SETTINGS) },
                )
            }

            composable(
                route = Routes.CHAT,
                arguments = listOf(
                    navArgument(Routes.CHAT_ARG_ID) {
                        type = NavType.StringType
                        defaultValue = ""
                    },
                    navArgument(Routes.CHAT_ARG_PEER) {
                        type = NavType.StringType
                        defaultValue = ""
                    },
                ),
                // The deep link a push notification opens; the chat id is the only thing
                // the server's push payload carries that identifies a destination.
                deepLinks = listOf(navDeepLink { uriPattern = Routes.CHAT_DEEP_LINK }),
            ) {
                ChatScreen(onBack = { navController.popBackStack() })
            }

            composable(Routes.NEW_CHAT) {
                NewChatScreen(
                    onBack = { navController.popBackStack() },
                    onOpenPeer = { username ->
                        navController.navigate(Routes.chatByPeer(username)) {
                            popUpTo(Routes.CHATS)
                        }
                    },
                    onOpenChat = { chatId ->
                        navController.navigate(Routes.chatById(chatId)) {
                            popUpTo(Routes.CHATS)
                        }
                    },
                )
            }

            composable(Routes.SETTINGS) {
                SettingsScreen(
                    onBack = { navController.popBackStack() },
                    onOpenSessions = { navController.navigate(Routes.SESSIONS) },
                    onOpenPrivacy = { navController.navigate(Routes.PRIVACY) },
                    onOpenSecurity = { navController.navigate(Routes.SECURITY) },
                    onOpenPremium = { navController.navigate(Routes.PREMIUM) },
                )
            }

            composable(Routes.SESSIONS) {
                SessionsScreen(onBack = { navController.popBackStack() })
            }

            composable(Routes.PRIVACY) {
                PrivacyScreen(onBack = { navController.popBackStack() })
            }

            composable(Routes.SECURITY) {
                SecurityScreen(onBack = { navController.popBackStack() })
            }

            composable(Routes.PREMIUM) {
                PremiumScreen(onBack = { navController.popBackStack() })
            }
        }
    }
}
