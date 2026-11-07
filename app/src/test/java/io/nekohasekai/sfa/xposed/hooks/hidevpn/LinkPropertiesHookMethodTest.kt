package io.nekohasekai.sfa.xposed.hooks.hidevpn

import android.net.LinkProperties
import org.junit.Assert.*
import org.junit.Test

@Suppress("UNUSED_PARAMETER")
class LinkPropertiesHookMethodTest {
    @Test fun supportsOriginalPermissionRestrictedMethod() {
        val method = LinkPropertiesHookMethod.find(LegacyService::class.java)
        assertEquals("linkPropertiesRestrictedForCallerPermissions", method.name)
        assertEquals(3, method.parameterTypes.size)
    }

    @Test fun supportsRenamedThreeArgumentMethod() {
        val method = LinkPropertiesHookMethod.find(RenamedService::class.java)
        assertEquals("sanitizeLinkPropertiesForCallerAndUid", method.name)
        assertEquals(3, method.parameterTypes.size)
    }

    @Test fun prefersSharedFourArgumentMethodOverGetterWrapper() {
        // The Android 15 Connectivity APEX sends callbacks directly through this
        // overload; hooking only its three-argument wrapper misses those callbacks.
        val method = LinkPropertiesHookMethod.find(UpdatedService::class.java)
        assertEquals("sanitizeLinkPropertiesForCallerAndUid", method.name)
        assertEquals(4, method.parameterTypes.size)
    }

    @Test fun supportsFourArgumentMethodWithoutWrapper() {
        val method = LinkPropertiesHookMethod.find(FourArgumentService::class.java)
        assertEquals("sanitizeLinkPropertiesForCallerAndUid", method.name)
        assertEquals(4, method.parameterTypes.size)
    }

    @Test fun rejectsUnsupportedSignaturesAndReturnTypes() {
        for (service in listOf(UnknownService::class.java, WrongReturnTypeService::class.java)) {
            val error = assertThrows(NoSuchMethodException::class.java) {
                LinkPropertiesHookMethod.find(service)
            }
            assertTrue(error.message!!.contains(service.name))
        }
    }

    private class LegacyService {
        private fun linkPropertiesRestrictedForCallerPermissions(lp: LinkProperties?, callerPid: Int, callerUid: Int): LinkProperties? = lp
    }

    private class RenamedService {
        private fun sanitizeLinkPropertiesForCallerAndUid(lp: LinkProperties?, callerPid: Int, callerUid: Int): LinkProperties? = lp
    }

    private class UpdatedService {
        private fun sanitizeLinkPropertiesForCallerAndUid(lp: LinkProperties?, callerPid: Int, callerUid: Int): LinkProperties? = sanitizeLinkPropertiesForCallerAndUid(lp, callerPid, callerUid, callerUid)

        private fun sanitizeLinkPropertiesForCallerAndUid(lp: LinkProperties?, callerPid: Int, callerUid: Int, asUid: Int): LinkProperties? = lp
    }

    private class FourArgumentService {
        private fun sanitizeLinkPropertiesForCallerAndUid(lp: LinkProperties?, callerPid: Int, callerUid: Int, asUid: Int): LinkProperties? = lp
    }

    private class UnknownService {
        private fun sanitizeLinkPropertiesForCallerAndUid(lp: LinkProperties?, callerPid: Int, callerUid: Long): LinkProperties? = lp
    }

    private class WrongReturnTypeService {
        private fun sanitizeLinkPropertiesForCallerAndUid(lp: LinkProperties?, callerPid: Int, callerUid: Int): Any? = lp
    }
}
