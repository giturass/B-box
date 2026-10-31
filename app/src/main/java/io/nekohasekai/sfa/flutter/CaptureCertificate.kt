package io.nekohasekai.sfa.flutter

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import android.util.Base64
import io.nekohasekai.libbox.Libbox
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Each installation keeps its own encrypted CA key; module exports contain only its public certificate. */
internal class CaptureCertificate(
    context: Context,
    private val keyAlias: String = "b_box_http_debug_ca_v1",
) {
    private val storage = AtomicFile(File(context.noBackupFilesDir, "http-debug/ca.json"))
    private fun read(): JSONObject? = if (storage.baseFile.exists()) JSONObject(String(storage.readFully(), Charsets.UTF_8)) else null
    private fun write(value: JSONObject) = writeCaptureFile(storage, value.toString())
    private fun encode(bytes: ByteArray) = Base64.encodeToString(bytes, Base64.NO_WRAP)
    private fun decode(text: String) = Base64.decode(text, Base64.NO_WRAP)
    private fun certificate(value: JSONObject): X509Certificate = CertificateFactory.getInstance("X.509")
        .generateCertificate(value.getString("certificate").byteInputStream()) as X509Certificate
    private fun fingerprint(value: JSONObject): String = captureCertificateFingerprint(certificate(value))

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

    private fun generate(): JSONObject {
        val generated = Libbox.generateUICaptureCA()
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key(create = true))
        cipher.updateAAD(generated.certificate.toByteArray(Charsets.UTF_8))
        return JSONObject().put("certificate", generated.certificate)
            .put("iv", encode(cipher.iv)).put("encryptedKey", encode(cipher.doFinal(generated.privateKey.toByteArray(Charsets.UTF_8))))
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

    fun exportCertificate(): CaptureCertificateExport {
        val value = read() ?: error("Generate a debugging CA first")
        val ca = certificate(value)
        ca.checkValidity()
        // Canonicalize from the parsed public certificate so no other PEM
        // material or persisted metadata can enter the export.
        val base64 = encode(ca.encoded).chunked(64).joinToString("\n")
        val pem = "-----BEGIN CERTIFICATE-----\n$base64\n-----END CERTIFICATE-----\n"
        return CaptureCertificateExport(pem, captureCertificateFilename(ca), captureCertificateFingerprint(ca))
    }

    fun requireCurrentExport(expected: CaptureCertificateExport) {
        val value = read() ?: error("Generate a debugging CA first")
        check(fingerprint(value) == expected.fingerprint) { "The CA changed; export its certificate or module again" }
    }

    fun moduleExported(expected: CaptureCertificateExport) {
        requireCurrentExport(expected)
        val value = read() ?: error("Generate a debugging CA first")
        value.put("moduleExportedAt", System.currentTimeMillis())
        write(value)
    }

    fun snapshot(): Map<String, Any?> {
        val value = read() ?: return mapOf("exists" to false)
        val ca = certificate(value)
        return mapOf("exists" to true, "fingerprint" to captureCertificateFingerprint(ca), "expiresAt" to ca.notAfter.time,
            "systemFilename" to captureCertificateFilename(ca), "trusted" to value.optBoolean("trusted"),
            "checkedAt" to value.optLong("checkedAt"), "moduleExportedAt" to value.optLong("moduleExportedAt"))
    }

    fun check(): Map<String, Any?> {
        val value = read() ?: error("Generate a debugging CA first")
        // Saving a module does not prove it was installed. Check the current
        // process's actual system store without Root or an external CLI.
        val systemStore = KeyStore.getInstance("AndroidCAStore").apply { load(null) }
        val expected = certificate(value).encoded
        val trusted = systemStore.aliases().toList().any { alias ->
            alias.startsWith("system:") && systemStore.getCertificate(alias)?.encoded?.contentEquals(expected) == true
        }
        value.put("trusted", trusted).put("checkedAt", System.currentTimeMillis())
        write(value)
        return snapshot()
    }

    fun rotate(): Map<String, Any?> {
        generate()
        return snapshot()
    }
}
