package io.nekohasekai.sfa.compose.screen.settings

import android.app.LocaleConfig
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.text.format.Formatter
import android.widget.Toast
import androidx.appcompat.app.AppCompatDelegate
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.outlined.DeleteForever
import androidx.compose.material.icons.outlined.DeleteSweep
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Language
import androidx.compose.material.icons.outlined.Notifications
import androidx.compose.material.icons.outlined.Speed
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.app.NotificationManagerCompat
import androidx.core.os.LocaleListCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.navigation.NavController
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.BuildConfig
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.compose.base.UiEvent
import io.nekohasekai.sfa.compose.base.rememberApplyServiceChangeNotifier
import io.nekohasekai.sfa.compose.topbar.LocalScaffoldPadding
import io.nekohasekai.sfa.compose.topbar.OverrideTopBar
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.ktx.clipboardText
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.xmlpull.v1.XmlPullParser
import java.io.File
import java.util.Locale
import android.provider.Settings as AndroidSettings

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun AppSettingsScreen(
    navController: NavController,
    serviceStatus: Status = Status.Stopped,
) {
    OverrideTopBar {
        TopAppBar(
            title = { Text(stringResource(R.string.title_app_settings)) },
            navigationIcon = {
                IconButton(onClick = { navController.navigateUp() }) {
                    Icon(
                        imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                        contentDescription = stringResource(R.string.content_description_back),
                    )
                }
            },
        )
    }

    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    var showVersionMenu by remember { mutableStateOf(false) }

    var notificationEnabled by remember { mutableStateOf(true) }
    var dynamicNotification by remember { mutableStateOf(Settings.dynamicNotification) }
    var showDisableNotificationDialog by remember { mutableStateOf(false) }
    val notifyApplyChange = rememberApplyServiceChangeNotifier(serviceStatus)

    var showLanguageDialog by remember { mutableStateOf(false) }
    val availableLocales = remember { getSupportedLocales(context) }
    var currentLocaleTag by remember {
        val appLocales = AppCompatDelegate.getApplicationLocales()
        mutableStateOf(if (appLocales.isEmpty) "" else appLocales.toLanguageTags())
    }

    var cacheSize by remember { mutableStateOf(0L) }
    var cacheSizeText by remember { mutableStateOf("") }

    fun refreshCacheSize() {
        scope.launch(Dispatchers.IO) {
            val size = calculateDirSize(context.cacheDir)
            withContext(Dispatchers.Main) {
                cacheSize = size
                cacheSizeText = Formatter.formatFileSize(context, size)
            }
        }
    }

    LaunchedEffect(Unit) {
        refreshCacheSize()
    }

    // Re-check states when returning from background (e.g., after granting permission)
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Application.notification.createNotificationChannel(
                NotificationChannel(
                    "service",
                    "Service Notifications",
                    NotificationManager.IMPORTANCE_LOW,
                ),
            )
            val channel = Application.notification.getNotificationChannel("service")
            notificationEnabled = channel?.importance != NotificationManager.IMPORTANCE_NONE
        } else {
            notificationEnabled = NotificationManagerCompat.from(context).areNotificationsEnabled()
        }
    }

    if (showDisableNotificationDialog) {
        AlertDialog(
            onDismissRequest = { showDisableNotificationDialog = false },
            title = { Text(stringResource(R.string.enable_notification)) },
            text = {
                Text(
                    stringResource(
                        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                            R.string.disable_notification_description
                        } else {
                            R.string.disable_notification_description_legacy
                        },
                    ),
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    showDisableNotificationDialog = false
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                        context.startActivity(
                            Intent(AndroidSettings.ACTION_CHANNEL_NOTIFICATION_SETTINGS).apply {
                                putExtra(AndroidSettings.EXTRA_APP_PACKAGE, context.packageName)
                                putExtra(AndroidSettings.EXTRA_CHANNEL_ID, "service")
                            },
                        )
                    } else {
                        context.startActivity(
                            Intent(
                                AndroidSettings.ACTION_APPLICATION_DETAILS_SETTINGS,
                                Uri.parse("package:${context.packageName}"),
                            ),
                        )
                    }
                }) {
                    Text(stringResource(R.string.ok))
                }
            },
            dismissButton = {
                TextButton(onClick = { showDisableNotificationDialog = false }) {
                    Text(stringResource(android.R.string.cancel))
                }
            },
        )
    }

    if (showLanguageDialog) {
        LanguageDialog(
            currentTag = currentLocaleTag,
            availableLocales = availableLocales,
            onLocaleSelected = { tag ->
                currentLocaleTag = tag
                val localeList = if (tag.isEmpty()) {
                    LocaleListCompat.getEmptyLocaleList()
                } else {
                    LocaleListCompat.forLanguageTags(tag)
                }
                AppCompatDelegate.setApplicationLocales(localeList)
                showLanguageDialog = false
            },
            onDismiss = { showLanguageDialog = false },
        )
    }

    val scaffoldPadding = LocalScaffoldPadding.current

    Column(
        modifier =
        Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.surface)
            .verticalScroll(rememberScrollState())
            .padding(
                top = scaffoldPadding.calculateTopPadding() + 8.dp,
                bottom = scaffoldPadding.calculateBottomPadding() + 8.dp,
            ),
    ) {
        // Info Card
        Card(
            modifier =
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp),
            colors =
            CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.surfaceContainer,
            ),
        ) {
            Column {
                Box {
                    ListItem(
                        headlineContent = {
                            Text(
                                stringResource(R.string.app_version_title),
                                style = MaterialTheme.typography.bodyLarge,
                            )
                        },
                        supportingContent = {
                            Text(
                                BuildConfig.VERSION_NAME,
                                style = MaterialTheme.typography.bodyMedium,
                            )
                        },
                        leadingContent = {
                            Icon(
                                imageVector = Icons.Outlined.Info,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.primary,
                            )
                        },
                        modifier =
                        Modifier
                            .clip(RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp))
                            .combinedClickable(
                                onClick = {},
                                onLongClick = { showVersionMenu = true },
                            ),
                        colors =
                        ListItemDefaults.colors(
                            containerColor = Color.Transparent,
                        ),
                    )
                    Box(modifier = Modifier.align(Alignment.BottomEnd)) {
                        DropdownMenu(
                            expanded = showVersionMenu,
                            onDismissRequest = { showVersionMenu = false },
                        ) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.per_app_proxy_action_copy)) },
                                leadingIcon = {
                                    Icon(
                                        imageVector = Icons.Filled.ContentCopy,
                                        contentDescription = null,
                                    )
                                },
                                onClick = {
                                    clipboardText = BuildConfig.VERSION_NAME
                                    Toast.makeText(
                                        context,
                                        R.string.copied_to_clipboard,
                                        Toast.LENGTH_SHORT,
                                    ).show()
                                    showVersionMenu = false
                                },
                            )
                        }
                    }
                }

                ListItem(
                    headlineContent = {
                        Text(
                            stringResource(R.string.language),
                            style = MaterialTheme.typography.bodyLarge,
                        )
                    },
                    supportingContent = {
                        val displayName = if (currentLocaleTag.isEmpty()) {
                            stringResource(R.string.system_default)
                        } else {
                            val locale = Locale.forLanguageTag(currentLocaleTag)
                            locale.getDisplayName(locale).replaceFirstChar { it.uppercase(locale) }
                        }
                        Text(displayName, style = MaterialTheme.typography.bodyMedium)
                    },
                    leadingContent = {
                        Icon(
                            imageVector = Icons.Outlined.Language,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                        )
                    },
                    modifier =
                    Modifier
                        .clickable { showLanguageDialog = true },
                    colors =
                    ListItemDefaults.colors(
                        containerColor = Color.Transparent,
                    ),
                )

                ListItem(
                    headlineContent = {
                        Text(
                            stringResource(R.string.cache_size),
                            style = MaterialTheme.typography.bodyLarge,
                        )
                    },
                    supportingContent = {
                        if (cacheSizeText.isNotEmpty()) {
                            Text(cacheSizeText, style = MaterialTheme.typography.bodyMedium)
                        }
                    },
                    leadingContent = {
                        Icon(
                            imageVector = Icons.Outlined.DeleteSweep,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                        )
                    },
                    modifier =
                    Modifier
                        .clip(
                            if (cacheSize > 0L) {
                                RoundedCornerShape(0.dp)
                            } else {
                                RoundedCornerShape(bottomStart = 12.dp, bottomEnd = 12.dp)
                            },
                        ),
                    colors =
                    ListItemDefaults.colors(
                        containerColor = Color.Transparent,
                    ),
                )

                if (cacheSize > 0L) {
                    ListItem(
                        headlineContent = {
                            Text(
                                stringResource(R.string.clear_cache),
                                style = MaterialTheme.typography.bodyLarge,
                            )
                        },
                        leadingContent = {
                            Icon(
                                imageVector = Icons.Outlined.DeleteForever,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.primary,
                            )
                        },
                        modifier =
                        Modifier
                            .clip(RoundedCornerShape(bottomStart = 12.dp, bottomEnd = 12.dp))
                            .clickable {
                                scope.launch(Dispatchers.IO) {
                                    context.cacheDir?.listFiles()?.forEach { it.deleteRecursively() }
                                    withContext(Dispatchers.Main) {
                                        cacheSize = 0L
                                        cacheSizeText = Formatter.formatFileSize(context, 0L)
                                    }
                                }
                            },
                        colors =
                        ListItemDefaults.colors(
                            containerColor = Color.Transparent,
                        ),
                    )
                }
            }
        }

        Spacer(modifier = Modifier.height(8.dp))

        Text(
            text = stringResource(R.string.notification_settings),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(horizontal = 32.dp, vertical = 8.dp),
        )

        Card(
            modifier =
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp),
            colors =
            CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.surfaceContainer,
            ),
        ) {
            Column {
                ListItem(
                    headlineContent = {
                        Text(
                            stringResource(R.string.enable_notification),
                            style = MaterialTheme.typography.bodyLarge,
                        )
                    },
                    leadingContent = {
                        Icon(
                            imageVector = Icons.Outlined.Notifications,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                        )
                    },
                    trailingContent = {
                        Switch(
                            checked = notificationEnabled,
                            onCheckedChange = null,
                        )
                    },
                    modifier =
                    Modifier
                        .clip(RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp))
                        .clickable { showDisableNotificationDialog = true },
                    colors =
                    ListItemDefaults.colors(
                        containerColor = Color.Transparent,
                    ),
                )
                ListItem(
                    headlineContent = {
                        Text(
                            stringResource(R.string.dynamic_notification),
                            style = MaterialTheme.typography.bodyLarge,
                        )
                    },
                    leadingContent = {
                        Icon(
                            imageVector = Icons.Outlined.Speed,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                        )
                    },
                    trailingContent = {
                        Switch(
                            checked = dynamicNotification,
                            onCheckedChange = { checked ->
                                dynamicNotification = checked
                                scope.launch(Dispatchers.IO) {
                                    Settings.dynamicNotification = checked
                                    withContext(Dispatchers.Main) {
                                        notifyApplyChange(UiEvent.ApplyServiceChange.Mode.Restart)
                                    }
                                }
                            },
                        )
                    },
                    modifier =
                    Modifier
                        .clip(RoundedCornerShape(bottomStart = 12.dp, bottomEnd = 12.dp)),
                    colors =
                    ListItemDefaults.colors(
                        containerColor = Color.Transparent,
                    ),
                )
            }
        }
    }
}

@Composable
private fun LanguageDialog(
    currentTag: String,
    availableLocales: List<Locale>,
    onLocaleSelected: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.language)) },
        text = {
            Column {
                Row(
                    modifier =
                    Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(8.dp))
                        .clickable { onLocaleSelected("") }
                        .padding(vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    RadioButton(
                        selected = currentTag.isEmpty(),
                        onClick = { onLocaleSelected("") },
                    )
                    Text(
                        text = stringResource(R.string.system_default),
                        style = MaterialTheme.typography.bodyLarge,
                        modifier = Modifier.padding(start = 8.dp),
                    )
                }
                availableLocales.forEach { locale ->
                    val tag = locale.toLanguageTag()
                    Row(
                        modifier =
                        Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(8.dp))
                            .clickable { onLocaleSelected(tag) }
                            .padding(vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton(
                            selected = currentTag == tag,
                            onClick = { onLocaleSelected(tag) },
                        )
                        Text(
                            text = locale.getDisplayName(locale).replaceFirstChar { it.uppercase(locale) },
                            style = MaterialTheme.typography.bodyLarge,
                            modifier = Modifier.padding(start = 8.dp),
                        )
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) {
                Text(stringResource(android.R.string.cancel))
            }
        },
    )
}

private fun calculateDirSize(dir: File?): Long {
    if (dir == null || !dir.exists()) return 0
    var size = 0L
    dir.listFiles()?.forEach { file ->
        size += if (file.isDirectory) calculateDirSize(file) else file.length()
    }
    return size
}

private fun getSupportedLocales(context: Context): List<Locale> {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        val localeConfig = LocaleConfig(context)
        val localeList = localeConfig.supportedLocales ?: return emptyList()
        return (0 until localeList.size()).map { localeList.get(it) }
    }
    return parseLocalesConfig(context)
}

private fun parseLocalesConfig(context: Context): List<Locale> {
    val locales = mutableListOf<Locale>()
    try {
        val resId = context.resources.getIdentifier(
            "_generated_res_locale_config",
            "xml",
            context.packageName,
        )
        if (resId == 0) return emptyList()
        val parser = context.resources.getXml(resId)
        while (parser.next() != XmlPullParser.END_DOCUMENT) {
            if (parser.eventType == XmlPullParser.START_TAG && parser.name == "locale") {
                val name = parser.getAttributeValue(
                    "http://schemas.android.com/apk/res/android",
                    "name",
                )
                if (name != null) {
                    locales.add(Locale.forLanguageTag(name))
                }
            }
        }
    } catch (_: Exception) {
    }
    return locales
}
