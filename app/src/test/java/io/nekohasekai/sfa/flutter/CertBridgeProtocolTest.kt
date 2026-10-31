package io.nekohasekai.sfa.flutter

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class CertBridgeProtocolTest {
    private val fingerprint = "a1".repeat(32)

    @Test fun verifiesFingerprintBeforeCertificateRemoval() {
        val formatted = fingerprint.chunked(2).joinToString(":").uppercase()
        checkCertBridgeIdentity("linker noise\nok=1\nfingerprint_sha256=$formatted\nfilename=1234abcd.0", fingerprint)
        assertThrows(IllegalArgumentException::class.java) { checkCertBridgeIdentity("ok=1\nfingerprint_sha256=${"b2".repeat(32)}", fingerprint) }
        assertThrows(IllegalArgumentException::class.java) { checkCertBridgeIdentity("ok=1\nerror=not_found\nfingerprint_sha256=$fingerprint", fingerprint) }
    }

    @Test fun acceptsTheOpenSSLLabelLeftInCertBridgeDetails() {
        val formatted = fingerprint.chunked(2).joinToString(":").uppercase()
        for (value in listOf(fingerprint, formatted, "SHA256 Fingerprint=$formatted", "sha256 Fingerprint=$formatted", "SHA-256 Fingerprint = $formatted")) {
            checkCertBridgeIdentity("ok=1\nfingerprint_sha256= $value \r\nfilename=1234abcd.0", fingerprint)
        }
        assertThrows(IllegalArgumentException::class.java) {
            checkCertBridgeIdentity("ok=1\nfingerprint_sha256=SHA256 Fingerprint=${"b2".repeat(32)}", fingerprint)
        }
    }

    @Test fun rejectsMissingTruncatedAndOtherAlgorithmFingerprints() {
        assertThrows(IllegalArgumentException::class.java) { checkCertBridgeIdentity("ok=1\nfingerprint_sha1=$fingerprint", fingerprint) }
        for (value in listOf("", "a1:b2", "SHA256 Fingerprint=a1:b2", "SHA256 Fingerprint=${fingerprint}00",
            "SHA1 Fingerprint=$fingerprint", "MD5 Fingerprint=$fingerprint", "unrecognized=$fingerprint", "$fingerprint trailing text")) {
            assertThrows(IllegalArgumentException::class.java) {
                checkCertBridgeIdentity("ok=1\nfingerprint_sha256=$value", fingerprint)
            }
        }
    }

    @Test fun acceptsOnlyCertificateBasenamesAndCompleteFingerprints() {
        assertEquals("1234abcd.12", certBridgeFilename("1234abcd.12"))
        for (value in listOf("../1234abcd.0", "1234abcd.0;id", "1234abcd.0\n", "whatever")) {
            assertThrows(IllegalArgumentException::class.java) { certBridgeFilename(value) }
        }
        assertThrows(IllegalArgumentException::class.java) { certificateFingerprint("a1:b2") }
        assertEquals("hello=world", certBridgeFields("ignored output\nhint=hello=world")["hint"])
    }

    @Test fun readsTheCustomListWithoutUsingDisplayNamesAsIdentity() {
        assertEquals(setOf("1234abcd.0", "abcd1234.1"), certBridgeCustomFiles(
            "linker noise\ncustom|1234abcd.0|B-box HTTP Debug CA\ncustom|abcd1234.1|Another CA",
        ))
        assertEquals(emptySet<String>(), certBridgeCustomFiles(""))
        assertThrows(IllegalArgumentException::class.java) { certBridgeCustomFiles("custom|../1234abcd.0|Injected") }
    }
}
