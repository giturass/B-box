package io.nekohasekai.sfa.flutter

import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.zip.ZipInputStream

class CaptureCertificateModuleTest {
    private val pem = javaClass.getResourceAsStream("/capture-ca.pem")!!.bufferedReader().use { it.readText() }
    private val certificate = CertificateFactory.getInstance("X.509")
        .generateCertificate(pem.byteInputStream()) as X509Certificate
    private val fingerprint = "96bcec06264976f37460779acf28c5a7cfe8a3c0aae11a8ffcee05c0bddf08c6"
    private val export = CaptureCertificateExport(pem, "6187b673.0", fingerprint)

    private fun module(value: CaptureCertificateExport = export): ByteArray = ByteArrayOutputStream().also { output ->
        CaptureCertificateModule.write(output, value) { "template: $it".byteInputStream() }
    }.toByteArray()

    @Test fun systemFilenameAndIdentityMatchOpenSSL() {
        // ISRG Root X1; expected values obtained with openssl x509, not this helper.
        assertEquals("6187b673.0", captureCertificateFilename(certificate))
        assertEquals(fingerprint, captureCertificateFingerprint(certificate))
        assertEquals("B-box-CA-96bcec062649.zip", export.moduleFilename)
    }

    @Test fun packagesOnePublicCAAndTheModuleInstallerWithoutExtraFiles() {
        val entries = linkedMapOf<String, ByteArray>()
        ZipInputStream(module().inputStream()).use { zip ->
            while (true) {
                val entry = zip.nextEntry ?: break
                assertNull("Duplicate ZIP entry: ${entry.name}", entries.put(entry.name, zip.readBytes()))
            }
        }
        assertEquals(setOf(
            "META-INF/com/google/android/update-binary", "META-INF/com/google/android/updater-script",
            "customize.sh", "post-fs-data.sh", "service.sh", "ca-store.sh", "skip_mount",
            "LICENSE", "NOTICE", "README.txt", "module.prop", "system/etc/security/cacerts/6187b673.0",
        ), entries.keys)
        assertEquals(pem, entries.getValue("system/etc/security/cacerts/6187b673.0").toString(Charsets.US_ASCII))
        val properties = entries.getValue("module.prop").toString(Charsets.UTF_8)
        assertTrue(properties.lineSequence().contains("id=bbox_http_debug_ca"))
        assertTrue(properties.contains(fingerprint))
        assertFalse(properties.contains("updateJson="))
        assertArrayEquals(module(), module())
    }

    @Test fun rejectsPrivateMaterialPathsAndMismatchedCertificatesBeforeWriting() {
        for (invalid in listOf(
            export.copy(filename = "../6187b673.0"),
            export.copy(filename = "6187b673.0\n"),
            export.copy(filename = "00000000.0"),
            export.copy(fingerprint = "a1".repeat(32)),
            export.copy(fingerprint = "$fingerprint\nid=injected"),
            export.copy(pem = "$pem-----BEGIN PRIVATE KEY-----\nnot-a-key\n-----END PRIVATE KEY-----\n"),
            export.copy(pem = pem + pem),
        )) {
            val output = ByteArrayOutputStream()
            assertThrows(IllegalArgumentException::class.java) {
                CaptureCertificateModule.write(output, invalid) { error("Must reject before reading assets") }
            }
            assertEquals(0, output.size())
        }
    }

    @Test fun finishesTheZipWithoutClosingTheCallersDocumentStream() {
        val output = object : ByteArrayOutputStream() {
            var closed = false
            override fun close() { closed = true; super.close() }
        }
        CaptureCertificateModule.write(output, export) { ByteArray(0).inputStream() }
        assertFalse(output.closed)
        // A missing template and a failed document write must both propagate.
        assertThrows(IOException::class.java) {
            CaptureCertificateModule.write(ByteArrayOutputStream(), export) { throw IOException("Missing module asset") }
        }
        val failing = object : java.io.OutputStream() {
            override fun write(value: Int) { throw IOException("Document provider failed") }
        }
        assertThrows(IOException::class.java) {
            CaptureCertificateModule.write(failing, export) { ByteArray(0).inputStream() }
        }
    }
}
