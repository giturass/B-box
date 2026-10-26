package io.nekohasekai.sfa.flutter

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.hardware.usb.UsbDevice
import android.hardware.usb.UsbManager
import android.os.Build
import androidx.core.content.ContextCompat
import androidx.core.content.IntentCompat
import io.nekohasekai.sfa.usbip.USBIPManager
import java.io.Closeable

internal class BoxUsb(private val context: Context) : Closeable {
    private val manager = context.getSystemService(Context.USB_SERVICE) as UsbManager
    private val action = context.packageName + ".action.FLUTTER_USB_PERMISSION"
    private val pending = mutableMapOf<String, String>()
    private var requestID = 0
    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(received: Context, intent: Intent) {
            if (intent.action != action) return
            val device = IntentCompat.getParcelableExtra(intent, UsbManager.EXTRA_DEVICE, UsbDevice::class.java) ?: return
            val server = pending.remove(device.deviceName) ?: return
            if (intent.getBooleanExtra(UsbManager.EXTRA_PERMISSION_GRANTED, false) && manager.hasPermission(device)) {
                USBIPManager.attach(received, server, device)
            }
        }
    }

    init {
        ContextCompat.registerReceiver(context, receiver, IntentFilter(action), ContextCompat.RECEIVER_NOT_EXPORTED)
    }

    fun devices(): List<Map<String, Any?>> = manager.deviceList.values.map {
        mapOf("id" to it.deviceName, "name" to (it.productName ?: it.deviceName), "vendor" to it.vendorId, "product" to it.productId)
    }

    fun attach(server: String, name: String) {
        val device = manager.deviceList[name] ?: error("USB device disconnected")
        if (manager.hasPermission(device)) {
            USBIPManager.attach(context, server, device)
        } else {
            require(!pending.containsKey(name)) { "USB permission is already pending" }
            pending[name] = server
            val flags = PendingIntent.FLAG_ONE_SHOT or if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) PendingIntent.FLAG_MUTABLE else 0
            manager.requestPermission(device, PendingIntent.getBroadcast(context, ++requestID, Intent(action).setPackage(context.packageName), flags))
        }
    }

    override fun close() {
        context.unregisterReceiver(receiver)
        pending.clear()
    }
}
