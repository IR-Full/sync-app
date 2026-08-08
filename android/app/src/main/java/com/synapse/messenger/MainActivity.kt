package com.synapse.messenger

import android.Manifest
import android.os.Build
import android.os.Bundle
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
import com.synapse.messenger.presentation.auth.AuthScreen
import com.synapse.messenger.presentation.chat.ChatScreen
import com.synapse.messenger.presentation.chats.ChatListScreen
import com.synapse.messenger.presentation.components.LoadingState
import com.synapse.messenger.presentation.navigation.Routes
import com.synapse.messenger.presentation.newchat.NewChatScreen
import com.synapse.messenger.presentation.settings.SettingsScreen
import com.synapse.messenger.presentation.theme.SynapseTheme
import dagger.hilt.android.AndroidEntryPoint

@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    private val requestNotifications = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { /* Declined is fine: the app works, it just stays quiet. */ }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && BuildConfig.PUSH_ENABLED) {
            requestNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }

        setContent { SynapseApp() }
    }
}

@Composable
private fun SynapseApp(viewModel: RootViewModel = hiltViewModel()) {
    val settings by viewModel.settings.collectAsStateWithLifecycle()
    val session by viewModel.session.collectAsStateWithLifecycle()
    val restored by viewModel.restored.collectAsStateWithLifecycle()

    SynapseTheme(themeMode = settings.theme, language = settings.language) {
        // Until the stored session has been read once, neither destination is right:
        // showing login would flash it for anyone already signed in.
        if (!restored) {
            LoadingState()
            return@SynapseTheme
        }

        val navController = rememberNavController()

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
                SettingsScreen(onBack = { navController.popBackStack() })
            }
        }
    }
}
