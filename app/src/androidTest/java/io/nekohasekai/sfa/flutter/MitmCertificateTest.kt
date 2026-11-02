package io.nekohasekai.sfa.flutter

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.sfa.Application
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.ByteArrayOutputStream
import java.io.File
import java.security.KeyStore
import java.security.MessageDigest
import java.security.Signature
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.UUID
import java.util.zip.ZipInputStream

@RunWith(AndroidJUnit4::class)
class MitmCertificateTest {
    private fun withCertificate(block: (MitmCertificate, File, MutableSet<String>) -> Unit) {
        Application.application.setupLibbox()
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "mitm-certificate-${UUID.randomUUID()}").apply { mkdirs() }
        val aliases = mutableSetOf<String>()
        try { block(MitmCertificate(context, directory), directory, aliases) }
        finally {
            val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            for (alias in aliases) store.deleteEntry(alias)
            directory.deleteRecursively()
        }
    }

    @Test fun keystoreSignsLibboxCAWithoutExportingPrivateKey() = withCertificate { certificate, directory, aliases ->
        val empty = certificate.snapshot()
        assertEquals(false, empty["exists"])
        assertFalse(File(directory, "mitm-authority.json").exists())
        assertTrue(runCatching { certificate.material() }.isFailure)
        val generated = certificate.generate(empty["revision"] as String)
        val record = JSONObject(File(directory, "mitm-authority.json").readText())
        val alias = record.getString("alias").also(aliases::add)
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val key = keyStore.getKey(alias, null)
        assertNotNull(key)
        assertNull("Keystore CA private key must not be exportable", key.encoded)
        val ca = certificate.publicCertificate(generated["revision"] as String)
        ca.verify(keyStore.getCertificate(alias).publicKey)
        ca.checkValidity()
        assertTrue(ca.basicConstraints >= 0)
        assertTrue(ca.keyUsage[5])
        assertEquals(true, generated["valid"])
        assertEquals(true, generated["keyAvailable"])
        assertEquals(false, generated["systemTrusted"])
        assertEquals(false, generated["userTrusted"])
        val material = certificate.material()
        assertFalse(material.first.contains("PRIVATE KEY"))
        val message = "Android Keystore to gomobile MITM signature".toByteArray()
        val signature = material.second.sign(MessageDigest.getInstance("SHA-256").digest(message))
        assertTrue(Signature.getInstance("SHA256withECDSA").run {
            initVerify(ca.publicKey); update(message); verify(signature)
        })
        assertTrue(runCatching { material.second.sign(ByteArray(31)) }.isFailure)
    }

    @Test fun rotationChecksRevisionAndRecoversAnInterruptedWrite() = withCertificate { certificate, directory, aliases ->
        val first = certificate.generate(certificate.snapshot()["revision"] as String)
        val base = File(directory, "mitm-authority.json")
        val oldRecord = base.readText()
        val oldAlias = JSONObject(oldRecord).getString("alias").also(aliases::add)
        val oldCertificate = certificate.publicCertificate(first["revision"] as String)
        val backup = File(directory, "mitm-authority.json.bak")
        assertTrue(base.renameTo(backup))
        base.writeText("interrupted record")
        assertEquals(first, certificate.snapshot())
        assertEquals(oldRecord, base.readText())
        assertFalse(backup.exists())
        assertTrue(runCatching { certificate.generate("stale-revision") }.isFailure)
        assertEquals(oldRecord, base.readText())
        val second = certificate.generate(first["revision"] as String)
        val newAlias = JSONObject(base.readText()).getString("alias").also(aliases::add)
        assertNotEquals(oldAlias, newAlias)
        assertNotEquals(first["fingerprint"], second["fingerprint"])
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        assertFalse(keyStore.containsAlias(oldAlias))
        assertTrue(keyStore.containsAlias(newAlias))
        assertTrue(runCatching { certificate.publicCertificate(first["revision"] as String) }.isFailure)
        assertNotEquals(oldCertificate.subjectX500Principal, certificate.publicCertificate(second["revision"] as String).subjectX500Principal)
        assertEquals(second, MitmCertificate(InstrumentationRegistry.getInstrumentation().targetContext, directory).snapshot())
    }

    @Test fun exportsContainOnlyPublicCertificateAndPackagedModuleScripts() = withCertificate { certificate, directory, aliases ->
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val state = certificate.generate(certificate.snapshot()["revision"] as String)
        val alias = JSONObject(File(directory, "mitm-authority.json").readText()).getString("alias").also(aliases::add)
        val ca = certificate.publicCertificate(state["revision"] as String)
        val pem = MitmCertificate.pem(ca)
        val decoded = CertificateFactory.getInstance("X.509").generateCertificate(pem.byteInputStream()) as X509Certificate
        assertArrayEquals(ca.encoded, decoded.encoded)
        val bytes = ByteArrayOutputStream().also { MitmCertificateModule.write(context, it, ca) }.toByteArray()
        val entries = linkedMapOf<String, ByteArray>()
        ZipInputStream(bytes.inputStream()).use { zip ->
            while (true) {
                val entry = zip.nextEntry ?: break
                assertFalse(entries.containsKey(entry.name))
                assertFalse(entry.name.contains(".."))
                entries[entry.name] = zip.readBytes()
                zip.closeEntry()
            }
        }
        assertEquals(setOf("module.prop", "skip_mount", "inject.sh", "post-fs-data.sh", "service.sh", "NOTICE", "LICENSE",
            "META-INF/com/google/android/update-binary", "META-INF/com/google/android/updater-script",
            "cacert/${MitmCertificate.systemName(ca)}"), entries.keys)
        assertEquals(pem, entries.getValue("cacert/${MitmCertificate.systemName(ca)}").toString(Charsets.US_ASCII))
        assertTrue(MitmCertificate.systemName(ca).matches(Regex("[0-9a-f]{8}\\.0")))
        assertTrue(entries.getValue("module.prop").toString(Charsets.UTF_8).contains(MitmCertificate.fingerprint(ca)))
        for (content in entries.values) {
            val text = content.toString(Charsets.UTF_8)
            assertFalse(text.contains("-----BEGIN PRIVATE KEY-----"))
            assertFalse(text.contains("-----BEGIN EC PRIVATE KEY-----"))
            assertFalse(text.contains(alias))
        }
        assertEquals(false, certificate.snapshot()["systemTrusted"])
        assertEquals(false, certificate.snapshot()["userTrusted"])
    }

    @Test fun missingKeyAndOversizeRecoveredRecordAreReported() = withCertificate { certificate, directory, aliases ->
        val state = certificate.generate(certificate.snapshot()["revision"] as String)
        val base = File(directory, "mitm-authority.json")
        val alias = JSONObject(base.readText()).getString("alias").also(aliases::add)
        KeyStore.getInstance("AndroidKeyStore").apply { load(null); deleteEntry(alias) }
        assertEquals(false, certificate.snapshot()["keyAvailable"])
        assertTrue(runCatching { certificate.material() }.isFailure)
        // Losing the signing key does not invalidate the public export identity.
        assertNotNull(certificate.publicCertificate(state["revision"] as String))
        File(directory, "mitm-authority.json.bak").writeText(" ".repeat(32 * 1024 + 1))
        base.writeText("{}")
        assertTrue(runCatching { certificate.snapshot() }.exceptionOrNull()?.message.orEmpty().contains("32 KiB"))
    }
}
