package io.nekohasekai.sfa.compose.component

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandHorizontally
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkHorizontally
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.constant.Status
import kotlinx.coroutines.delay
import java.util.Locale

// Compose adaptation of FlClash's StartButton layout and timing.
// Reference: chen08209/FlClash@c7be7023, lib/views/dashboard/widgets/start_button.dart.
// See MODIFICATIONS.md for provenance and the intentional platform differences.
@Composable
fun ServiceStartButton(
    serviceStatus: Status,
    startTime: Long?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    maxWidth: Dp = 220.dp,
) {
    val expanded = serviceStatus == Status.Started || serviceStatus == Status.Stopping
    var elapsedSeconds by remember { mutableLongStateOf(0L) }
    LaunchedEffect(serviceStatus, startTime) {
        when (serviceStatus) {
            Status.Started -> {
                while (true) {
                    elapsedSeconds = startTime?.let { ((System.currentTimeMillis() - it) / 1000).coerceAtLeast(0) } ?: 0
                    delay(1000)
                }
            }
            Status.Stopped -> {
                // Keep the last time visible until the closing animation finishes.
                delay(200)
                elapsedSeconds = 0
            }
            else -> Unit
        }
    }

    val hours = elapsedSeconds / 3600
    val time = String.format(Locale.ROOT, "%02d:%02d:%02d", hours, elapsedSeconds / 60 % 60, elapsedSeconds % 60)
    val timeStyle = MaterialTheme.typography.titleMedium.copy(
        fontWeight = FontWeight.Medium,
        fontFeatureSettings = "tnum",
        textDirection = TextDirection.Ltr,
    )
    val textMeasurer = rememberTextMeasurer()
    val density = LocalDensity.current
    val sample = "8".repeat(hours.toString().length.coerceAtLeast(2)) + ":88:88"
    val measuredWidth = textMeasurer.measure(sample, timeStyle).size.width
    val timeWidth by animateDpAsState(
        targetValue = (with(density) { measuredWidth.toDp() } + 16.dp)
            .coerceAtMost((maxWidth - 48.dp).coerceAtLeast(8.dp)),
        animationSpec = tween(200, easing = FastOutSlowInEasing),
        label = "Runtime width",
    )
    val iconWidth by animateDpAsState(
        targetValue = if (expanded) 48.dp else 56.dp,
        animationSpec = tween(200, easing = FastOutSlowInEasing),
        label = "Service icon spacing",
    )
    val iconProgress by animateFloatAsState(
        targetValue = if (expanded) 1f else 0f,
        animationSpec = tween(200, easing = FastOutSlowInEasing),
        label = "Play pause morph",
    )
    val stateLabel = stringResource(
        when (serviceStatus) {
            Status.Stopped -> R.string.status_default
            Status.Starting -> R.string.status_starting
            Status.Started -> R.string.status_started
            Status.Stopping -> R.string.status_stopping
        },
    )
    val actionLabel = stringResource(if (serviceStatus == Status.Stopped) R.string.action_start else R.string.stop)
    Surface(
        onClick = onClick,
        enabled = serviceStatus != Status.Stopping,
        modifier = modifier.height(56.dp).semantics {
            contentDescription = actionLabel
            stateDescription = stateLabel
        },
        shape = RoundedCornerShape(16.dp),
        color = MaterialTheme.colorScheme.primaryContainer,
        contentColor = MaterialTheme.colorScheme.onPrimaryContainer,
        shadowElevation = 4.dp,
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                modifier = Modifier.width(iconWidth).height(56.dp).padding(start = 16.dp),
                contentAlignment = Alignment.CenterStart,
            ) {
                if (serviceStatus == Status.Starting || serviceStatus == Status.Stopping) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(24.dp),
                        color = MaterialTheme.colorScheme.onPrimaryContainer,
                        strokeWidth = 2.dp,
                    )
                } else {
                    PlayPauseIcon(progress = iconProgress)
                }
            }
            AnimatedVisibility(
                visible = expanded,
                enter = expandHorizontally(tween(200), expandFrom = Alignment.Start) + fadeIn(tween(150)),
                exit = shrinkHorizontally(tween(200), shrinkTowards = Alignment.Start) + fadeOut(tween(150)),
            ) {
                val timeText = buildAnnotatedString {
                    val extraHourDigits = (time.indexOf(':') - 2).coerceAtLeast(0)
                    withStyle(SpanStyle(color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.SemiBold)) {
                        append(time.take(extraHourDigits))
                    }
                    append(time.drop(extraHourDigits))
                }
                Text(
                    text = timeText,
                    modifier = Modifier.width(timeWidth).padding(end = 16.dp),
                    style = timeStyle,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun PlayPauseIcon(progress: Float) {
    val color = MaterialTheme.colorScheme.onPrimaryContainer
    Canvas(modifier = Modifier.size(24.dp)) {
        fun point(from: Float, to: Float) = from + (to - from) * progress
        val scaleX = size.width / 24f
        val scaleY = size.height / 24f
        // Two polygons form a triangle at rest and separate into pause bars.
        val left = Path().apply {
            moveTo(6f * scaleX, 4f * scaleY)
            lineTo(point(12f, 10f) * scaleX, point(8f, 4f) * scaleY)
            lineTo(point(12f, 10f) * scaleX, point(16f, 20f) * scaleY)
            lineTo(6f * scaleX, 20f * scaleY)
            close()
        }
        val right = Path().apply {
            moveTo(point(12f, 14f) * scaleX, point(8f, 4f) * scaleY)
            lineTo(point(20f, 18f) * scaleX, point(12f, 4f) * scaleY)
            lineTo(point(20f, 18f) * scaleX, point(12f, 20f) * scaleY)
            lineTo(point(12f, 14f) * scaleX, point(16f, 20f) * scaleY)
            close()
        }
        drawPath(left, color)
        drawPath(right, color)
    }
}
