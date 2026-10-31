package io.nekohasekai.sfa.flutter

import android.content.Context
import android.content.ContextWrapper
import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class CaptureCertificateTest {
    private suspend fun fixture(block: suspend (Context, String, File) -> Unit) {
        Application.application.setupLibbox()
        val base = InstrumentationRegistry.getInstrumentation().targetContext
        val id = UUID.randomUUID().toString()
        val directory = File(base.cacheDir, "capture-certificate-test-$id")
        val alias = "b_box_http_debug_test_$id"
        val context = object : ContextWrapper(base) {
            override fun getNoBackupFilesDir(): File = directory
        }
        try {
            block(context, alias, File(directory, "http-debug/ca.json"))
        } finally {
            KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry(alias)
            directory.deleteRecursively()
        }
    }

    @Test fun reusesTheEncryptedKeyAndExportsOnlyThePublicCertificate() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val ca = CaptureCertificate(context, alias)
            assertEquals(false, ca.snapshot()["exists"])
            val material = ca.material()
            val saved = file.readText()
            assertFalse(saved.contains("PRIVATE KEY"))
            assertFalse(saved.contains(material.second))
            assertTrue(JSONObject(saved).getString("encryptedKey").isNotEmpty())
            assertEquals(material, CaptureCertificate(context, alias).material())
            assertEquals(material.first, ca.publicCertificate())
            assertFalse(ca.publicCertificate().contains("PRIVATE KEY"))
            val certificate = CertificateFactory.getInstance("X.509")
                .generateCertificate(material.first.byteInputStream()) as X509Certificate
            certificate.checkValidity()
            assertTrue(certificate.basicConstraints >= 0)
            assertTrue(certificate.keyUsage[5])
            certificate.verify(certificate.publicKey)
            assertEquals(64, (ca.snapshot()["fingerprint"] as String).length)
        }
    }

    @Test fun rejectsMixingACertificateWithAnotherEncryptedPrivateKey() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val ca = CaptureCertificate(context, alias)
            ca.material()
            val saved = JSONObject(file.readText())
            saved.put("certificate", Libbox.generateUICaptureCA().certificate)
            file.writeText(saved.toString())
            // The new certificate is valid; AES-GCM AAD must still reject the mix.
            assertTrue(runCatching { CaptureCertificate(context, alias).material() }.isFailure)
        }
    }

    @Test fun importVerifiesIdentityAndRotationKeepsTheRemovalPending() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, _ ->
            val commands = mutableListOf<List<String>>()
            var fingerprint = ""
            var publicCertificate = ""
            var present = false
            val ca = CaptureCertificate(context, alias) { arguments ->
                commands += arguments
                when (arguments.first()) {
                    "install_custom" -> {
                        assertEquals(publicCertificate, String(Base64.decode(arguments[1], Base64.NO_WRAP), Charsets.UTF_8))
                        assertFalse(arguments.any { it.contains("PRIVATE KEY") })
                        present = true
                        "ok=1\nfilename=1234abcd.0"
                    }
                    "list_custom" -> if (present) "custom|1234abcd.0|B-box HTTP Debug CA" else ""
                    "cert_info" -> "ok=1\nfingerprint_sha256=$fingerprint"
                    "remove_custom" -> {
                        assertEquals(listOf("remove_custom", "1234abcd.0"), arguments)
                        present = false
                        "ok=1\nreboot_required=1"
                    }
                    else -> error("Unexpected command: ${arguments.first()}")
                }
            }
            val old = ca.material()
            publicCertificate = old.first
            fingerprint = ca.snapshot()["fingerprint"] as String
            val imported = ca.install()
            assertEquals(true, imported["pendingReboot"])
            assertEquals(false, imported["trusted"])
            assertEquals(0L, imported["checkedAt"])
            val rotated = ca.rotate()
            assertNotEquals(old, ca.material())
            assertEquals("", rotated["filename"])
            assertEquals(true, rotated["pendingReboot"])
            assertEquals(1, commands.count { it.first() == "remove_custom" })
            assertTrue(commands.indexOfLast { it.first() == "cert_info" } < commands.indexOfLast { it.first() == "remove_custom" })
        }
    }

    @Test fun missingImportIsRecoverableButADifferentCertificateIsNeverRemoved() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, _ ->
            var fingerprint = ""
            var present = true
            var wrongIdentity = false
            val commands = mutableListOf<String>()
            val ca = CaptureCertificate(context, alias) { arguments ->
                commands += arguments.first()
                when (arguments.first()) {
                    "install_custom" -> "ok=1\nfilename=1234abcd.0"
                    "cert_info" -> "ok=1\nfingerprint_sha256=${if (wrongIdentity) "00".repeat(32) else fingerprint}"
                    "list_custom" -> if (present) "custom|1234abcd.0|Same display name" else ""
                    else -> error("Must not mutate another certificate: ${arguments.first()}")
                }
            }
            val old = ca.material()
            fingerprint = ca.snapshot()["fingerprint"] as String
            ca.install()
            wrongIdentity = true
            assertTrue(runCatching { ca.rotate() }.isFailure)
            assertEquals(old, ca.material())
            assertFalse(commands.contains("remove_custom"))
            present = false // The recorded file was manually removed outside B-box.
            assertEquals("", ca.remove()["filename"])
            val rotated = ca.rotate()
            assertNotEquals(old, ca.material())
            assertEquals(true, rotated["pendingReboot"])
            assertFalse(commands.contains("remove_custom"))
        }
    }
}
