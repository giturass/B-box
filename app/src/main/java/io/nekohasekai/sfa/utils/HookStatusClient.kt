package io.nekohasekai.sfa.utils

import android.content.Context
import android.content.pm.PackageInfo
import io.nekohasekai.sfa.bg.ParceledListSlice
import io.nekohasekai.sfa.xposed.HookModuleVersion
import io.nekohasekai.sfa.xposed.HookStatusKeys
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

object HookStatusClient {
    data class Status(val active: Boolean, val lastPatchedAt: Long, val version: Int, val systemPid: Int) {
        val compatible: Boolean get() = active && version == HookModuleVersion.CURRENT
    }

    private val statusFlow = MutableStateFlow<Status?>(null)
    val status: StateFlow<Status?> = statusFlow
    @Volatile var lastError: String? = null
        private set
    @Volatile private var appContext: Context? = null

    fun register(context: Context) {
        appContext = context.applicationContext
        refresh()
    }

    /** A missing or dead framework must never prevent ordinary app startup. */
    fun refresh(): Status? {
        lastError = null
        val value = try {
            val binder = appContext?.let(ConnectivityBinderUtils::getBinder)
            if (binder == null) null else ConnectivityBinderUtils.withParcel { data, reply ->
                data.writeInterfaceToken(HookStatusKeys.DESCRIPTOR)
                if (!binder.transact(HookStatusKeys.TRANSACTION_STATUS, data, reply, 0)) return@withParcel null
                reply.readException()
                Status(reply.readInt() != 0, reply.readLong(), reply.readInt(), reply.readInt())
            }
        } catch (error: Exception) {
            lastError = error.message ?: error.toString()
            null
        }
        statusFlow.value = value
        return value
    }

    fun getInstalledPackages(context: Context, flags: Long, userId: Int): List<PackageInfo>? {
        val binder = ConnectivityBinderUtils.getBinder(context) ?: return null
        return ConnectivityBinderUtils.withParcel { data, reply ->
            data.writeInterfaceToken(HookStatusKeys.DESCRIPTOR)
            data.writeLong(flags)
            data.writeInt(userId)
            if (!binder.transact(HookStatusKeys.TRANSACTION_GET_INSTALLED_PACKAGES, data, reply, 0)) return@withParcel null
            reply.readException()
            val slice = ParceledListSlice.CREATOR.createFromParcel(reply, PackageInfo::class.java.classLoader)
            slice.list.filterIsInstance<PackageInfo>()
        }
    }
}
