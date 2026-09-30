package io.nekohasekai.sfa.compose.component

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.outlined.Cable
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.compose.theme.Theme
import io.nekohasekai.sfa.constant.Status
import kotlinx.coroutines.delay

@Composable
fun ServiceStatusBar(
    visible: Boolean,
    serviceStatus: Status,
    startTime: Long?,
    hasGroups: Boolean,
    onGroupsClick: () -> Unit,
    onConnectionsClick: () -> Unit,
    onServiceClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    AnimatedVisibility(
        visible = visible,
        enter = slideInVertically { it } + fadeIn(),
        exit = slideOutVertically { it } + fadeOut(),
        modifier = modifier,
    ) {
        BoxWithConstraints(modifier = Modifier.fillMaxWidth().padding(16.dp)) {
            val showShortcuts = serviceStatus == Status.Started
            val shortcutCount = if (showShortcuts) { if (hasGroups) 2 else 1 } else 0
            val maxStartWidth = (maxWidth - 68.dp * shortcutCount).coerceIn(56.dp, 220.dp)
            val connectionsLabel = stringResource(R.string.dashboard_active_connections)
            val groupsLabel = stringResource(R.string.dashboard_proxy_groups)
            val textMeasurer = rememberTextMeasurer()
            val labelStyle = MaterialTheme.typography.labelLarge
            val widestLabel = listOfNotNull(connectionsLabel, groupsLabel.takeIf { hasGroups })
                .maxOf { textMeasurer.measure(it, labelStyle).size.width }
            val inlineShortcutWidth = with(LocalDensity.current) { widestLabel.toDp() } + 52.dp
            // Decide for both shortcuts before the start button animates, so their
            // labels do not alternate between horizontal and stacked layouts.
            val compactShortcuts = maxWidth < maxStartWidth + (inlineShortcutWidth + 12.dp) * shortcutCount
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (showShortcuts) {
                    DashboardShortcut(
                        icon = Icons.Outlined.Cable,
                        label = connectionsLabel,
                        compact = compactShortcuts,
                        onClick = onConnectionsClick,
                        modifier = Modifier.weight(1f),
                    )
                    if (hasGroups) {
                        DashboardShortcut(
                            icon = Icons.Default.Folder,
                            label = groupsLabel,
                            compact = compactShortcuts,
                            onClick = onGroupsClick,
                            modifier = Modifier.weight(1f),
                        )
                    }
                } else {
                    Spacer(modifier = Modifier.weight(1f))
                }
                ServiceStartButton(
                    serviceStatus = serviceStatus,
                    startTime = startTime,
                    onClick = onServiceClick,
                    maxWidth = maxStartWidth,
                )
            }
        }
    }
}

@Composable
private fun DashboardShortcut(icon: ImageVector, label: String, compact: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Surface(
        onClick = onClick,
        modifier = modifier.height(56.dp),
        shape = RoundedCornerShape(16.dp),
        color = MaterialTheme.colorScheme.secondaryContainer,
        contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
        shadowElevation = 1.dp,
    ) {
        if (!compact) {
            Row(
                modifier = Modifier.padding(horizontal = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.Center,
            ) {
                Icon(icon, contentDescription = null, modifier = Modifier.size(20.dp))
                Spacer(modifier = Modifier.width(8.dp))
                Text(label, style = MaterialTheme.typography.labelLarge, maxLines = 1)
            }
        } else {
            Column(
                modifier = Modifier.padding(horizontal = 4.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(2.dp, Alignment.CenterVertically),
            ) {
                Icon(icon, contentDescription = null, modifier = Modifier.size(20.dp))
                Text(
                    text = label,
                    style = MaterialTheme.typography.labelSmall,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
fun UptimeText(startTime: Long, modifier: Modifier = Modifier) {
    var currentTime by remember { mutableLongStateOf(System.currentTimeMillis()) }

    LaunchedEffect(startTime) {
        while (true) {
            delay(1000)
            currentTime = System.currentTimeMillis()
        }
    }

    val elapsedSeconds = ((currentTime - startTime) / 1000).coerceAtLeast(0)
    val hours = elapsedSeconds / 3600
    val minutes = (elapsedSeconds % 3600) / 60
    val seconds = elapsedSeconds % 60

    val formattedTime =
        if (hours > 0) {
            String.format("%d:%02d:%02d", hours, minutes, seconds)
        } else {
            String.format("%d:%02d", minutes, seconds)
        }

    Text(
        text = formattedTime,
        maxLines = 1,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.Medium,
        color = MaterialTheme.colorScheme.onPrimaryContainer,
        modifier = modifier,
    )
}

@Preview(name = "Running · light", widthDp = 360, showBackground = true, locale = "zh")
@Preview(name = "Running · large type", widthDp = 320, fontScale = 1.5f, showBackground = true, locale = "zh")
@Composable
private fun RunningControlsPreview() {
    Theme(dynamicColor = false) {
        ServiceStatusBar(true, Status.Started, System.currentTimeMillis() - 3661000, true, {}, {}, {})
    }
}

@Preview(name = "Running · dark", widthDp = 360, showBackground = true, backgroundColor = 0xFF151515, locale = "zh")
@Composable
private fun DarkControlsPreview() {
    Theme(darkTheme = true, dynamicColor = false) {
        ServiceStatusBar(true, Status.Started, System.currentTimeMillis() - 3661000, true, {}, {}, {})
    }
}

@Preview(name = "Stopped", widthDp = 360, showBackground = true)
@Composable
private fun StoppedControlsPreview() {
    Theme(dynamicColor = false) {
        ServiceStatusBar(true, Status.Stopped, null, false, {}, {}, {})
    }
}
