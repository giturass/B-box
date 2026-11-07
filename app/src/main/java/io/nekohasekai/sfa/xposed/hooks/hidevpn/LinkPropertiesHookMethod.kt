package io.nekohasekai.sfa.xposed.hooks.hidevpn

import android.net.LinkProperties
import java.lang.reflect.Method

internal object LinkPropertiesHookMethod {
    fun find(serviceClass: Class<*>): Method {
        val lpType = LinkProperties::class.java
        val intType = Int::class.javaPrimitiveType!!
        // Connectivity APEX updates can change these methods without changing SDK_INT.
        // The renamed three-argument wrapper delegates to the four-argument method;
        // callbacks also call the latter directly, so prefer that shared implementation.
        val candidates = listOf(
            "sanitizeLinkPropertiesForCallerAndUid" to arrayOf(lpType, intType, intType, intType),
            "sanitizeLinkPropertiesForCallerAndUid" to arrayOf(lpType, intType, intType),
            "linkPropertiesRestrictedForCallerPermissions" to arrayOf(lpType, intType, intType),
        )
        for ((name, parameterTypes) in candidates) {
            val method = try {
                serviceClass.getDeclaredMethod(name, *parameterTypes)
            } catch (_: NoSuchMethodException) {
                continue
            }
            if (method.returnType == lpType) {
                return method.apply { isAccessible = true }
            }
        }
        throw NoSuchMethodException("${serviceClass.name}: no supported LinkProperties sanitization method")
    }
}
