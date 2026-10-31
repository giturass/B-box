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
import java.io.ByteArrayOutputStream
import java.io.File
import java.security.KeyFactory
import java.security.KeyStore
import java.security.Signature
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.security.spec.PKCS8EncodedKeySpec
import java.util.UUID
import java.util.zip.ZipInputStream

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

    private fun certificate(pem: String) = CertificateFactory.getInstance("X.509")
        .generateCertificate(pem.byteInputStream()) as X509Certificate

    @Test fun reusesTheEncryptedKeyAndExportsItsMatchingPublicCertificate() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val ca = CaptureCertificate(context, alias)
            assertEquals(false, ca.snapshot()["exists"])
            assertTrue(runCatching { ca.exportCertificate() }.isFailure)
            assertFalse(file.exists())
            val material = ca.material()
            val saved = file.readText()
            assertFalse(saved.contains("PRIVATE KEY"))
            assertFalse(saved.contains(material.second))
            assertTrue(JSONObject(saved).getString("encryptedKey").isNotEmpty())
            assertEquals(material, CaptureCertificate(context, alias).material())
            val exported = ca.exportCertificate()
            val publicCA = certificate(exported.pem)
            assertArrayEquals(certificate(material.first).encoded, publicCA.encoded)
            publicCA.checkValidity()
            assertTrue(publicCA.basicConstraints >= 0)
            assertTrue(publicCA.keyUsage[5])
            publicCA.verify(publicCA.publicKey)
            assertEquals(64, exported.fingerprint.length)
            assertEquals(ca.snapshot()["systemFilename"], exported.filename)
            // Prove the decrypted signing key matches the CA that goes into the ZIP.
            val keyBytes = Base64.decode(material.second.lineSequence().filterNot { it.startsWith("-----") }.joinToString(""), Base64.DEFAULT)
            val key = KeyFactory.getInstance("EC").generatePrivate(PKCS8EncodedKeySpec(keyBytes))
            val message = "B-box export identity".toByteArray()
            val signature = Signature.getInstance("SHA256withECDSA").run { initSign(key); update(message); sign() }
            assertTrue(Signature.getInstance("SHA256withECDSA").run { initVerify(publicCA); update(message); verify(signature) })
            assertEquals(exported, CaptureCertificate(context, alias).exportCertificate())
            assertEquals(saved, file.readText()) // Merely exporting never changes the CA or its trust state.
        }
    }

    @Test fun rejectsMixingACertificateWithAnotherEncryptedPrivateKey() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val ca = CaptureCertificate(context, alias)
            ca.material()
            val mixed = JSONObject(file.readText()).put("certificate", Libbox.generateUICaptureCA().certificate).toString()
            file.writeText(mixed)
            // The replacement certificate is valid; AES-GCM AAD must still reject the mix.
            assertTrue(runCatching { CaptureCertificate(context, alias).material() }.isFailure)
            assertEquals(mixed, file.readText())
        }
    }

    @Test fun installationsHaveDifferentCertificatesAndSigningKeys() = runBlocking(Dispatchers.IO) {
        fixture { firstContext, firstAlias, _ ->
            fixture { secondContext, secondAlias, _ ->
                val first = CaptureCertificate(firstContext, firstAlias)
                val second = CaptureCertificate(secondContext, secondAlias)
                val firstMaterial = first.material()
                val secondMaterial = second.material()
                assertNotEquals(firstMaterial.first, secondMaterial.first)
                assertNotEquals(firstMaterial.second, secondMaterial.second)
                assertNotEquals(first.exportCertificate().moduleFilename, second.exportCertificate().moduleFilename)
            }
        }
    }

    @Test fun moduleContainsOnlyTheCurrentPublicCAAndDoesNotClaimSystemTrust() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, _ ->
            val ca = CaptureCertificate(context, alias)
            val material = ca.material()
            val exported = ca.exportCertificate()
            val output = ByteArrayOutputStream()
            CaptureCertificateModule.write(output, exported) { context.assets.open("${CaptureCertificateModule.assetDirectory}/$it") }
            val entries = mutableMapOf<String, ByteArray>()
            ZipInputStream(output.toByteArray().inputStream()).use { zip ->
                while (true) {
                    val entry = zip.nextEntry ?: break
                    assertNull(entries.put(entry.name, zip.readBytes()))
                }
            }
            assertEquals(listOf("system/etc/security/cacerts/${exported.filename}"), entries.keys.filter { it.startsWith("system/") })
            assertEquals(exported.pem, entries.getValue("system/etc/security/cacerts/${exported.filename}").toString(Charsets.US_ASCII))
            assertTrue(entries.getValue("module.prop").toString(Charsets.UTF_8).contains(exported.fingerprint))
            for (bytes in entries.values) {
                assertFalse(bytes.toString(Charsets.UTF_8).contains("-----BEGIN PRIVATE KEY-----"))
                assertFalse(bytes.toString(Charsets.UTF_8).contains(material.second))
            }
            assertEquals(0L, ca.snapshot()["moduleExportedAt"])
            ca.moduleExported(exported)
            assertTrue((ca.snapshot()["moduleExportedAt"] as Long) > 0)
            assertEquals(0L, ca.snapshot()["checkedAt"])
            assertEquals(false, ca.snapshot()["trusted"])
            val checked = ca.check() // This isolated CA was never installed in AndroidCAStore.
            assertEquals(false, checked["trusted"])
            assertTrue((checked["checkedAt"] as Long) > 0)
            assertEquals(material, ca.material())
        }
    }

    @Test fun rotationInvalidatesOldExportsAndResetsExportAndTrustMetadata() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val ca = CaptureCertificate(context, alias)
            val oldMaterial = ca.material()
            val oldExport = ca.exportCertificate()
            ca.moduleExported(oldExport)
            ca.check()
            val rotated = ca.rotate()
            assertNotEquals(oldMaterial, ca.material())
            assertNotEquals(oldExport.fingerprint, rotated["fingerprint"])
            assertEquals(0L, rotated["moduleExportedAt"])
            assertEquals(0L, rotated["checkedAt"])
            assertEquals(false, rotated["trusted"])
            val saved = file.readText()
            assertTrue(runCatching { ca.requireCurrentExport(oldExport) }.isFailure)
            assertTrue(runCatching { ca.moduleExported(oldExport) }.isFailure)
            assertEquals(saved, file.readText())
            ca.requireCurrentExport(ca.exportCertificate())
        }
    }

    @Test fun missingKeystoreKeyFailsWithoutSilentlyReplacingTheCA() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val ca = CaptureCertificate(context, alias)
            ca.material()
            val original = ca.exportCertificate()
            val saved = file.readText()
            KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry(alias)
            val reopened = CaptureCertificate(context, alias)
            assertTrue(runCatching { reopened.material() }.isFailure)
            assertEquals(saved, file.readText())
            assertEquals(original, reopened.exportCertificate())
            reopened.rotate() // Only explicit rotation repairs a lost encryption key.
            assertNotEquals(original.fingerprint, reopened.exportCertificate().fingerprint)
            reopened.material()
        }
    }

    @Test fun reusesExistingCAStorageWithoutTreatingOldImportMetadataAsSystemTrust() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val original = CaptureCertificate(context, alias).material()
            file.writeText(JSONObject(file.readText()).put("filename", "1234abcd.0")
                .put("pendingReboot", true).put("backendOutput", "old importer output").toString())
            val reopened = CaptureCertificate(context, alias)
            assertEquals(original, reopened.material())
            assertEquals(false, reopened.check()["trusted"])
            assertFalse(reopened.snapshot().containsKey("filename"))
            assertFalse(reopened.snapshot().containsKey("pendingReboot"))
        }
    }

    @Test fun recognizesAnExistingSystemCertificateWithoutRoot() = runBlocking(Dispatchers.IO) {
        fixture { context, alias, file ->
            val systemStore = KeyStore.getInstance("AndroidCAStore").apply { load(null) }
            val systemAlias = systemStore.aliases().toList().first { it.startsWith("system:") }
            val encoded = Base64.encodeToString(systemStore.getCertificate(systemAlias).encoded, Base64.NO_WRAP)
            // Only public bytes enter isolated test storage; the real trust store is untouched.
            val pem = "-----BEGIN CERTIFICATE-----\n${encoded.chunked(64).joinToString("\n")}\n-----END CERTIFICATE-----\n"
            check(file.parentFile!!.mkdirs())
            file.writeText(JSONObject().put("certificate", pem).put("trusted", false).toString())
            val checked = CaptureCertificate(context, alias).check()
            assertEquals(true, checked["trusted"])
            assertTrue((checked["checkedAt"] as Long) > 0)
        }
    }
}
