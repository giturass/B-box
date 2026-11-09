package io.nekohasekai.sfa.vendor

sealed class PackageQueryStrategy {
    data class UserSelected(val mode: String) : PackageQueryStrategy()
    data object Direct : PackageQueryStrategy()
}
