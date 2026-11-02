package io.nekohasekai.sfa.flutter

import android.content.Context
import java.io.OutputStream
import java.security.cert.X509Certificate
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream

/** Creates a public-certificate-only module. Installation belongs to the root manager. */
internal object MitmCertificateModule {
    private val assets = listOf("inject.sh", "post-fs-data.sh", "service.sh", "NOTICE", "LICENSE",
        "META-INF/com/google/android/update-binary", "META-INF/com/google/android/updater-script")

    fun write(context: Context, output: OutputStream, certificate: X509Certificate) {
        certificate.checkValidity()
        require(certificate.basicConstraints >= 0 && certificate.keyUsage?.getOrNull(5) == true) { "Expected a CA certificate" }
        val fingerprint = MitmCertificate.fingerprint(certificate)
        ZipOutputStream(output).use { zip ->
            fun add(name: String, body: () -> Unit) {
                zip.putNextEntry(ZipEntry(name))
                body()
                zip.closeEntry()
            }
            for (name in assets) add(name) { context.assets.open("mitm-ca/$name").use { it.copyTo(zip) } }
            add("module.prop") { zip.write((
                "id=bbox_mitm_ca\nname=B-box MITM CA\nversion=2\nversionCode=2\nauthor=B-box / FlClash contributors\n" +
                "description=Public MITM CA $fingerprint. Reboot after installing, updating or removing.\n"
            ).toByteArray(Charsets.UTF_8)) }
            add("skip_mount") { }
            add("cacert/${MitmCertificate.systemName(certificate)}") {
                zip.write(MitmCertificate.pem(certificate).toByteArray(Charsets.US_ASCII))
            }
        }
    }
}
