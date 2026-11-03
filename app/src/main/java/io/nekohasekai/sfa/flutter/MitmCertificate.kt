package io.nekohasekai.sfa.flutter

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import android.util.Base64
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.UICaptureSigner
import io.nekohasekai.sfa.database.ProfileContent
import org.json.JSONObject
import java.io.File
import java.io.FileNotFoundException
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.Signature
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import java.util.UUID

/** Shared by capture and profile processing. Only the public certificate is persisted. */
internal class MitmCertificate(context: Context, directory: File = File(context.noBackupFilesDir, "http-debug")) {
    private val file = AtomicFile(File(directory, "mitm-authority.json"))
    private val keyStore get() = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

    private fun read(): String {
        val input = try { file.openRead() } catch (error: FileNotFoundException) {
            if (file.baseFile.exists() || File(file.baseFile.path + ".bak").exists()) throw error
            return ""
        }
        return input.use {
            require(it.channel.size() <= 32 * 1024) { "MITM certificate record exceeds 32 KiB" }
            it.readBytes().toString(Charsets.UTF_8)
        }
    }

    private fun record(): JSONObject = JSONObject(read().ifEmpty { error("Generate a MITM CA first") }).also {
        require(it.getInt("version") == 1) { "Unsupported MITM certificate record" }
        require(it.getString("alias").matches(Regex("b_box_mitm_v2_[0-9a-f-]{36}"))) { "Invalid MITM key alias" }
    }

    private fun parse(value: JSONObject): X509Certificate = CertificateFactory.getInstance("X.509")
        .generateCertificate(value.getString("certificate").byteInputStream(Charsets.US_ASCII)) as X509Certificate

    private class PlatformSigner(private val key: PrivateKey) : UICaptureSigner {
        override fun sign(digest: ByteArray): ByteArray {
            require(digest.size == 32) { "MITM CA signs only SHA-256 digests" }
            // x509.CreateCertificate has already hashed the message. Android's
            // NONEwithECDSA supports this operation from API 23 onward.
            return Signature.getInstance("NONEwithECDSA").run {
                initSign(key)
                update(digest)
                sign()
            }
        }
    }

    fun material(): Pair<String, UICaptureSigner> {
        synchronized(accessLock) {
            val value = record()
            val certificate = parse(value)
            certificate.checkValidity()
            val key = keyStore.getKey(value.getString("alias"), null) as? PrivateKey
                ?: error("MITM signing key is unavailable; regenerate the CA")
            return value.getString("certificate") to PlatformSigner(key)
        }
    }

    fun snapshot(): Map<String, Any?> {
        synchronized(accessLock) {
            val raw = read()
            if (raw.isEmpty()) return mapOf("exists" to false, "revision" to ProfileContent.revision(raw))
            val value = record()
            val certificate = parse(value)
            val trust = KeyStore.getInstance("AndroidCAStore").apply { load(null) }
            var system = false
            var user = false
            // Match DER identity in both stores, including hash.N collisions. Store
            // presence is not evidence that an arbitrary app accepts this CA.
            for (alias in trust.aliases().toList()) {
                if (trust.getCertificate(alias)?.encoded?.contentEquals(certificate.encoded) != true) continue
                system = system || alias.startsWith("system:")
                user = user || alias.startsWith("user:")
            }
            return mapOf(
                "exists" to true, "revision" to ProfileContent.revision(raw),
                "subject" to certificate.subjectX500Principal.name, "fingerprint" to fingerprint(certificate),
                "expiresAt" to certificate.notAfter.time, "systemTrusted" to system, "userTrusted" to user,
                "valid" to runCatching { certificate.checkValidity(); true }.getOrDefault(false),
                "keyAvailable" to keyStore.containsAlias(value.getString("alias")),
            )
        }
    }

    fun generate(expectedRevision: String): Map<String, Any?> {
        synchronized(accessLock) {
            val processing = JSONObject(Libbox.readUICaptureStatus())
            require(!processing.optBoolean("active") && !processing.optBoolean("rewriteActive")) {
                "Stop capture and the local service before changing the MITM certificate"
            }
            val old = read()
            require(ProfileContent.revision(old) == expectedRevision) { "MITM certificate changed; refresh before continuing" }
            val oldAlias = old.takeIf(String::isNotEmpty)?.let { record().getString("alias") }
            val alias = "b_box_mitm_v2_${UUID.randomUUID()}"
            try {
                val pair = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore").run {
                    initialize(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
                        .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                        .setDigests(KeyProperties.DIGEST_NONE, KeyProperties.DIGEST_SHA256)
                        .setUserAuthenticationRequired(false).build())
                    generateKeyPair()
                }
                val pem = Libbox.generateUICaptureCA(pair.public.encoded, PlatformSigner(pair.private))
                val value = JSONObject().put("version", 1).put("alias", alias).put("certificate", pem)
                parse(value).verify(pair.public)
                writeCaptureFile(file, value.toString())
            } catch (error: Exception) {
                runCatching { keyStore.deleteEntry(alias) }
                throw error
            }
            if (oldAlias != null) runCatching { keyStore.deleteEntry(oldAlias) }.onFailure {
                Libbox.appendUICaptureLog("warn", "Unable to remove the previous MITM signing key: ${it.message}")
            }
            return snapshot()
        }
    }

    fun publicCertificate(expectedRevision: String): X509Certificate {
        synchronized(accessLock) {
            require(ProfileContent.revision(read()) == expectedRevision) { "MITM certificate changed; refresh before continuing" }
            return parse(record()).also { it.checkValidity() }
        }
    }

    companion object {
        internal val accessLock = Any()
        fun fingerprint(certificate: X509Certificate): String = MessageDigest.getInstance("SHA-256")
            .digest(certificate.encoded).joinToString("") { "%02x".format(it.toInt() and 255) }

        fun pem(certificate: X509Certificate): String = "-----BEGIN CERTIFICATE-----\n" +
            Base64.encodeToString(certificate.encoded, Base64.NO_WRAP).chunked(64).joinToString("\n") +
            "\n-----END CERTIFICATE-----\n"

        fun systemName(certificate: X509Certificate): String {
            val hash = MessageDigest.getInstance("MD5").digest(certificate.subjectX500Principal.encoded)
            return hash.take(4).reversed().joinToString("") { "%02x".format(it.toInt() and 255) } + ".0"
        }
    }
}
