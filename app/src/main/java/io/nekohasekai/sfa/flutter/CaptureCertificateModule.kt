package io.nekohasekai.sfa.flutter

import java.io.FilterOutputStream
import java.io.InputStream
import java.io.OutputStream
import java.security.MessageDigest
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream

internal fun captureCertificateFilename(certificate: X509Certificate): String {
    // OpenSSL subject_hash_old: the first four MD5 bytes of the original
    // subject DER, interpreted little endian. A display name is not identity.
    val digest = MessageDigest.getInstance("MD5").digest(certificate.subjectX500Principal.encoded)
    return digest.take(4).reversed().joinToString("") { "%02x".format(it.toInt() and 0xff) } + ".0"
}

internal fun captureCertificateFingerprint(certificate: X509Certificate): String = MessageDigest.getInstance("SHA-256")
    .digest(certificate.encoded).joinToString("") { "%02x".format(it.toInt() and 0xff) }

internal data class CaptureCertificateExport(val pem: String, val filename: String, val fingerprint: String) {
    val moduleFilename get() = "B-box-CA-${fingerprint.take(12)}.zip"
}

internal object CaptureCertificateModule {
    const val assetDirectory = "capture-module"
    const val moduleId = "bbox_http_debug_ca"
    val files = listOf(
        "META-INF/com/google/android/update-binary", "META-INF/com/google/android/updater-script",
        "customize.sh", "post-fs-data.sh", "service.sh", "ca-store.sh", "skip_mount", "LICENSE", "NOTICE", "README.txt",
    )

    fun write(output: OutputStream, certificate: CaptureCertificateExport, openAsset: (String) -> InputStream) {
        require(certificate.filename.matches(Regex("[0-9a-f]{8}\\.0"))) { "Invalid system certificate filename" }
        require(certificate.fingerprint.matches(Regex("[0-9a-f]{64}"))) { "Invalid certificate fingerprint" }
        require(certificate.pem.matches(Regex("-----BEGIN CERTIFICATE-----\\r?\\n[A-Za-z0-9+/=\\r\\n]+-----END CERTIFICATE-----\\s*"))) {
            "Only the public CA can be exported"
        }
        val parsed = CertificateFactory.getInstance("X.509")
            .generateCertificate(certificate.pem.byteInputStream(Charsets.US_ASCII)) as X509Certificate
        require(captureCertificateFilename(parsed) == certificate.filename &&
            captureCertificateFingerprint(parsed) == certificate.fingerprint) { "Certificate export identity mismatch" }
        // Finish the ZIP and release its compressor; the caller owns the document stream.
        val sink = object : FilterOutputStream(output) {
            override fun write(bytes: ByteArray, offset: Int, length: Int) = out.write(bytes, offset, length)
            override fun close() = flush()
        }
        ZipOutputStream(sink).use { zip ->
            fun entry(name: String, write: () -> Unit) {
                zip.putNextEntry(ZipEntry(name).apply { time = 0 })
                write()
                zip.closeEntry()
            }
            for (name in files) entry(name) { openAsset(name).use { it.copyTo(zip) } }
            entry("module.prop") {
                zip.write(("id=$moduleId\nname=B-box HTTP Debug CA\nversion=1.0\nversionCode=1\n" +
                    "author=B-box, based on ProxyPin Certificate Installer\n" +
                    "description=B-box local debugging CA. SHA-256: ${certificate.fingerprint}. Reboot after installation or removal.\n")
                    .toByteArray(Charsets.UTF_8))
            }
            entry("system/etc/security/cacerts/${certificate.filename}") { zip.write(certificate.pem.toByteArray(Charsets.US_ASCII)) }
        }
    }
}
