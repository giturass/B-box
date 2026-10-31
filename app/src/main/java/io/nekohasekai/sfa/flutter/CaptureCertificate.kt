package io.nekohasekai.sfa.flutter

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import android.util.Base64
import com.topjohnwu.superuser.Shell
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.RootClient
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CancellationException
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import java.security.MessageDigest
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Only the public certificate is passed to CertBridge. Private keys stay encrypted at rest. */
internal class CaptureCertificate(
    context: Context,
    private val keyAlias: String = "b_box_http_debug_ca_v1",
    private val runCertBridge: suspend (List<String>) -> String = ::executeCertBridge,
) {
    private val storage = AtomicFile(File(context.noBackupFilesDir, "http-debug/ca.json"))
    private fun read(): JSONObject? = if (storage.baseFile.exists()) JSONObject(String(storage.readFully(), Charsets.UTF_8)) else null
    private fun write(value: JSONObject) = writeCaptureFile(storage, value.toString())
    private fun encode(bytes: ByteArray) = Base64.encodeToString(bytes, Base64.NO_WRAP)
    private fun decode(text: String) = Base64.decode(text, Base64.NO_WRAP)
    private fun certificate(value: JSONObject): X509Certificate = CertificateFactory.getInstance("X.509")
        .generateCertificate(value.getString("certificate").byteInputStream()) as X509Certificate
    private fun fingerprint(value: JSONObject): String = MessageDigest.getInstance("SHA-256")
        .digest(certificate(value).encoded).joinToString("") { "%02x".format(it.toInt() and 0xff) }

    private fun key(create: Boolean): SecretKey {
        val keystore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keystore.getKey(keyAlias, null) as? SecretKey)?.let { return it }
        check(create) { "The CA encryption key is unavailable; rotate the debugging CA" }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(keyAlias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256).build())
        }.generateKey()
    }

    private fun generate(previous: JSONObject? = null): JSONObject {
        val generated = Libbox.generateUICaptureCA()
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key(create = true))
        cipher.updateAAD(generated.certificate.toByteArray(Charsets.UTF_8))
        return JSONObject().put("certificate", generated.certificate)
            .put("iv", encode(cipher.iv)).put("encryptedKey", encode(cipher.doFinal(generated.privateKey.toByteArray(Charsets.UTF_8))))
            .put("pendingReboot", previous?.optBoolean("pendingReboot") == true)
            .put("backendOutput", previous?.optString("backendOutput").orEmpty())
            .also(::write)
    }

    fun material(): Pair<String, String> {
        val value = read() ?: generate()
        certificate(value).checkValidity()
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key(create = false), GCMParameterSpec(128, decode(value.getString("iv"))))
        cipher.updateAAD(value.getString("certificate").toByteArray(Charsets.UTF_8))
        val plaintext = cipher.doFinal(decode(value.getString("encryptedKey")))
        return try { value.getString("certificate") to String(plaintext, Charsets.UTF_8) } finally { plaintext.fill(0) }
    }

    fun publicCertificate(): String = (read() ?: generate()).getString("certificate")

    fun snapshot(): Map<String, Any?> {
        val value = read() ?: return mapOf("exists" to false)
        return mapOf("exists" to true, "fingerprint" to fingerprint(value), "expiresAt" to certificate(value).notAfter.time,
            "filename" to value.optString("filename"), "trusted" to value.optBoolean("trusted"),
            "checkedAt" to value.optLong("checkedAt"), "pendingReboot" to value.optBoolean("pendingReboot"),
            "backendOutput" to value.optString("backendOutput"))
    }

    private suspend fun command(vararg arguments: String) = runCertBridge(arguments.toList())

    private suspend fun hasRecordedImport(value: JSONObject): Boolean {
        val filename = value.optString("filename")
        if (filename.isEmpty()) return false
        if (certBridgeFilename(filename) in certBridgeCustomFiles(command("list_custom"))) {
            checkCertBridgeIdentity(command("cert_info", "custom:$filename"), fingerprint(value))
            return true
        }
        // An externally removed file is recoverable, but it may remain trusted
        // in existing processes until reboot. Never delete a different certificate.
        value.remove("filename")
        value.put("pendingReboot", true).put("trusted", false).put("checkedAt", 0)
        write(value)
        return false
    }

    suspend fun install(): Map<String, Any?> {
        val value = read() ?: generate()
        certificate(value).checkValidity()
        val output = command("install_custom", encode(value.getString("certificate").toByteArray(Charsets.UTF_8)))
        val fields = certBridgeFields(output)
        check(fields["ok"] == "1") { "CertBridge did not confirm certificate import" }
        val filename = certBridgeFilename(fields["filename"].orEmpty())
        // Keep ownership metadata even if a later verification fails.
        value.put("filename", filename).put("pendingReboot", true).put("trusted", false)
            .put("checkedAt", 0).put("backendOutput", output)
        write(value)
        checkCertBridgeIdentity(command("cert_info", "custom:$filename"), fingerprint(value))
        return snapshot()
    }

    suspend fun check(): Map<String, Any?> {
        val value = read() ?: error("Generate a debugging CA first")
        var imported: Boolean? = null
        var output = ""
        var backendError: Exception? = null
        try {
            output = command("status", "--live")
            imported = hasRecordedImport(value)
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            backendError = error
        }
        // Check the current app process's system store, not a CLI's cached claim.
        // A Root/CertBridge failure must not prevent this independent check.
        val systemStore = KeyStore.getInstance("AndroidCAStore").apply { load(null) }
        val expected = certificate(value).encoded
        val trusted = systemStore.aliases().toList().any { alias ->
            alias.startsWith("system:") && systemStore.getCertificate(alias)?.encoded?.contentEquals(expected) == true
        }
        val pendingReboot = certBridgeFields(output)["reboot_required"] == "1" ||
            if (backendError == null) imported != trusted else value.optBoolean("pendingReboot")
        value.put("trusted", trusted).put("checkedAt", System.currentTimeMillis())
            .put("pendingReboot", pendingReboot)
            .put("backendOutput", output)
        write(value)
        // Keep reporting the CLI failure after saving the actual trust result.
        backendError?.let { throw it }
        return snapshot()
    }

    suspend fun remove(): Map<String, Any?> {
        val value = read() ?: error("Generate a debugging CA first")
        if (!hasRecordedImport(value)) return snapshot()
        val filename = certBridgeFilename(value.getString("filename"))
        val output = command("remove_custom", filename)
        check(certBridgeFields(output)["ok"] == "1") { "CertBridge did not confirm certificate removal" }
        value.remove("filename")
        value.put("pendingReboot", true).put("trusted", false).put("checkedAt", 0).put("backendOutput", output)
        write(value)
        return snapshot()
    }

    suspend fun rotate(): Map<String, Any?> {
        val old = read()
        if (!old?.optString("filename").isNullOrEmpty()) remove()
        generate(read())
        return snapshot()
    }
}

// CLI contract verified against CertBridge bda7cb26e6b831768e8faf2cac6bcde7c89d7fc7.
private suspend fun executeCertBridge(arguments: List<String>): String {
    check(!CommandTarget.isRemote) { "HTTP debugging is only available for the local service" }
    if (RootClient.rootAvailable.value != true) check(RootClient.checkRootAvailable()) { "Root permission is required for CertBridge" }
    check(!CommandTarget.isRemote) { "HTTP debugging is only available for the local service" }
    fun quote(value: String) = "'" + value.replace("'", "'\"'\"'") + "'"
    val args = arguments.joinToString(" ", transform = ::quote)
    val primary = "/data/adb/certbridge/cb"
    val fallback = "/data/adb/modules/CertBridge/bin/cb"
    val result = Shell.cmd("if [ -x $primary ]; then $primary $args; elif [ -x $fallback ]; then $fallback $args; else echo error=CertBridge_not_installed; exit 1; fi").exec()
    val output = (result.out + result.err).joinToString("\n")
    // A truncated list could incorrectly report a recorded certificate as absent.
    check(output.length <= 65536) { "CertBridge returned too much output" }
    val fields = certBridgeFields(output)
    check(result.isSuccess && fields["error"] == null) { "CertBridge: " + (fields["error"] ?: output.ifBlank { "command failed" }) }
    return output
}
