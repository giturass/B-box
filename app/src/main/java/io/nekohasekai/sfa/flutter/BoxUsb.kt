package io.nekohasekai.sfa.flutter

import android.hardware.usb.UsbManager
import io.nekohasekai.sfa.usbip.USBIPManager
import java.io.Closeable

internal class BoxUsb(private val context: BoxHost) : Closeable {
    private val manager = context.getSystemService(android.content.Context.USB_SERVICE) as UsbManager

    fun devices(): List<Map<String, Any?>> = manager.deviceList.values.map {
        mapOf("id" to it.deviceName, "name" to (it.productName ?: it.deviceName), "vendor" to it.vendorId, "product" to it.productId)
    }

    suspend fun attach(server: String, name: String) {
        val device = manager.deviceList[name] ?: error("USB device disconnected")
        if (!manager.hasPermission(device) && !context.requestUsbPermission(name)) return
        val current = manager.deviceList[name] ?: error("USB device disconnected")
        require(manager.hasPermission(current)) { "USB permission was not granted" }
        USBIPManager.attach(context, server, current)
    }

    override fun close() = Unit
}
