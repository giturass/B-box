package io.nekohasekai.sfa.flutter.ipc

import android.content.Context
import android.content.ClipData
import android.content.ClipDescription
import android.content.Intent
import android.net.Uri
import android.os.Parcel
import android.os.ParcelFileDescriptor
import android.security.KeyChain
import android.util.Log
import androidx.core.content.IntentCompat
import io.flutter.plugin.common.FlutterException
import io.flutter.plugin.common.StandardMethodCodec
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.launch
import java.io.Closeable
import java.io.File
import java.io.FileOutputStream
import java.nio.ByteBuffer
import java.util.concurrent.atomic.AtomicBoolean

internal const val BOX_CHANNEL = "io.ericlee.bfa/box"
internal const val EVENTS_CHANNEL = "io.ericlee.bfa/events"
internal const val HOST_CHANNEL = "io.ericlee.bfa/host"
internal val FLUTTER_CHANNELS = setOf(BOX_CHANNEL, EVENTS_CHANNEL)

/** The embedding uses written buffers when sending and readable buffers when receiving. */
internal object IpcPayload {
    fun writtenBytes(buffer: ByteBuffer?): ByteArray? = buffer?.duplicate()?.let { copy ->
        copy.limit(copy.position())
        copy.position(0)
        ByteArray(copy.remaining()).also(copy::get)
    }

    fun readableBytes(buffer: ByteBuffer?): ByteArray? = buffer?.duplicate()?.let { copy ->
        ByteArray(copy.remaining()).also(copy::get)
    }

    fun readableBuffer(bytes: ByteArray?): ByteBuffer? = bytes?.let(ByteBuffer::wrap)

    fun writtenBuffer(bytes: ByteArray?): ByteBuffer? = bytes?.let {
        ByteBuffer.allocateDirect(it.size).apply { put(it) }
    }

    fun errorBytes(error: Throwable, code: String = "ui_bridge"): ByteArray = writtenBytes(
        if (error is FlutterException) {
            StandardMethodCodec.INSTANCE.encodeErrorEnvelope(error.code, error.message, error.details)
        } else {
            StandardMethodCodec.INSTANCE.encodeErrorEnvelope(code, error.message, null)
        },
    )!!

    fun read(descriptor: ParcelFileDescriptor?): ByteArray? = descriptor?.let {
        ParcelFileDescriptor.AutoCloseInputStream(it).use { stream -> stream.readBytes() }
    }

    /** Also works with a local Binder in instrumentation tests: retain before the sender closes. */
    fun retain(descriptor: ParcelFileDescriptor?): ParcelFileDescriptor? = descriptor?.let {
        try {
            it.dup()
        } finally {
            runCatching { it.close() }
        }
    }

    /** Only the FD crosses Binder. Unlinking leaves no named payload file after this returns. */
    fun write(context: Context, bytes: ByteArray?): ParcelFileDescriptor? {
        if (bytes == null) return null
        val directory = File(context.cacheDir, "ui-ipc")
        check(directory.isDirectory || directory.mkdirs() || directory.isDirectory) { "Unable to create UI IPC directory" }
        val file = File.createTempFile("message-", ".ipc", directory)
        var descriptor: ParcelFileDescriptor? = null
        try {
            FileOutputStream(file).use { it.write(bytes) }
            descriptor = ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
            check(file.delete()) { "Unable to unlink UI IPC payload" }
            return descriptor
        } catch (error: Throwable) {
            runCatching { descriptor?.close() }
            throw error
        } finally {
            file.delete()
        }
    }

    fun marshalIntent(intent: Intent): ByteArray = Parcel.obtain().let { parcel ->
        try {
            sanitizeIntent(intent).writeToParcel(parcel, 0)
            parcel.marshall()
        } finally {
            parcel.recycle()
        }
    }

    /** Intent.marshall cannot carry Binder/FD extras. Copy only B-box import/share/UI fields. */
    fun sanitizeIntent(source: Intent): Intent = sanitizeIntent(source, allowChooserTarget = true)

    private fun sanitizeIntent(source: Intent, allowChooserTarget: Boolean): Intent = Intent().apply {
        action = source.action
        setDataAndType(source.data, source.type)
        flags = source.flags
        component = source.component
        setPackage(source.`package`)
        source.categories?.forEach { addCategory(it) }
        source.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()?.let { putExtra(Intent.EXTRA_TEXT, it) }
        source.getCharSequenceExtra(Intent.EXTRA_TITLE)?.toString()?.let { putExtra(Intent.EXTRA_TITLE, it) }
        source.getByteArrayExtra(KeyChain.EXTRA_CERTIFICATE)?.let { putExtra(KeyChain.EXTRA_CERTIFICATE, it) }
        source.getStringExtra(KeyChain.EXTRA_NAME)?.let { putExtra(KeyChain.EXTRA_NAME, it) }
        if (source.action == Intent.ACTION_SEND_MULTIPLE) {
            IntentCompat.getParcelableArrayListExtra(source, Intent.EXTRA_STREAM, Uri::class.java)?.let {
                putParcelableArrayListExtra(Intent.EXTRA_STREAM, ArrayList(it))
            }
        } else {
            IntentCompat.getParcelableExtra(source, Intent.EXTRA_STREAM, Uri::class.java)?.let {
                putExtra(Intent.EXTRA_STREAM, it)
            }
        }
        if (source.action == Intent.ACTION_CHOOSER && allowChooserTarget) {
            IntentCompat.getParcelableExtra(source, Intent.EXTRA_INTENT, Intent::class.java)?.let {
                putExtra(Intent.EXTRA_INTENT, sanitizeIntent(it, allowChooserTarget = false))
            }
        }
        val uris = source.clipData?.let { clip ->
            (0 until clip.itemCount).mapNotNull { clip.getItemAt(it).uri }
        }.orEmpty()
        if (uris.isNotEmpty()) {
            clipData = ClipData(ClipDescription("B-box", arrayOf("text/uri-list")), ClipData.Item(uris.first())).apply {
                uris.drop(1).forEach { addItem(ClipData.Item(it)) }
            }
        }
    }

    fun unmarshalIntent(bytes: ByteArray): Intent = Parcel.obtain().let { parcel ->
        try {
            parcel.unmarshall(bytes, 0, bytes.size)
            parcel.setDataPosition(0)
            Intent.CREATOR.createFromParcel(parcel)
        } finally {
            parcel.recycle()
        }
    }
}

/** Preserve channel order without performing file IO or waiting for a reader on Binder threads. */
internal class IpcSender(
    private val context: Context,
    scope: CoroutineScope,
    private val onFailure: (Throwable) -> Unit,
) : Closeable {
    private data class Message(
        val bytes: ByteArray?,
        val onFailure: ((Throwable) -> Unit)?,
        val send: (ParcelFileDescriptor?) -> Unit,
    )
    private val closed = AtomicBoolean()
    private val messages = Channel<Message>(Channel.UNLIMITED)

    init {
        scope.launch(Dispatchers.IO) {
            for (message in messages) {
                if (closed.get()) continue
                try {
                    IpcPayload.write(context, message.bytes).use(message.send)
                } catch (error: Exception) {
                    if (!closed.get()) (message.onFailure ?: onFailure)(error)
                }
            }
        }
    }

    fun send(bytes: ByteArray?, onFailure: ((Throwable) -> Unit)? = null, send: (ParcelFileDescriptor?) -> Unit) {
        if (closed.get()) return
        if (!messages.trySend(Message(bytes, onFailure, send)).isSuccess && !closed.get()) {
            (onFailure ?: this.onFailure)(IllegalStateException("UI IPC sender is closed"))
        }
    }

    override fun close() {
        closed.set(true)
        messages.close()
        while (messages.tryReceive().isSuccess) Unit
    }
}

internal fun closeDescriptor(descriptor: ParcelFileDescriptor?) {
    runCatching { descriptor?.close() }.onFailure {
        Log.w("BoxUiIPC", "Unable to close message descriptor", it)
    }
}
