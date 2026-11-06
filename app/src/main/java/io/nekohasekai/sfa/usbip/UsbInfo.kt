package io.nekohasekai.sfa.usbip

fun formatVidPid(vendorId: Int, productId: Int): String {
    fun hex4(value: Int) = (value and 0xffff).toString(16).padStart(4, '0')
    return "${hex4(vendorId)}:${hex4(productId)}"
}
