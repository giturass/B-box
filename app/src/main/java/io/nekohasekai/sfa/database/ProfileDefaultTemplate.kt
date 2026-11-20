package io.nekohasekai.sfa.database

import io.nekohasekai.libbox.Libbox
import org.json.JSONArray
import org.json.JSONObject

/** The default profile identity and use of its generated template are separate. */
internal object ProfileDefaultTemplate {
    private val nodeSections = setOf("outbounds", "endpoints")
    private val groupSelections = setOf("outbounds", "default")
    // Read-only after initialization; compare the template of the actual bundled core.
    private val template by lazy {
        JSONObject(Libbox.formatConfig(Libbox.getUIDefaultConfig().value).value)
    }

    fun usesAutoGroup(profile: Profile, content: String): Boolean {
        if (profile.typed.isDefault) return true
        if (profile.typed.type != TypedProfile.Type.Local) return false
        return matchesTemplate(JSONObject(Libbox.formatConfig(content).value))
    }

    /** Caller has already restored local node ownership after the complete JSON edit. */
    fun normalizeAutoGroup(content: String, type: TypedProfile.Type, isDefault: Boolean): String {
        if (!isDefault && type != TypedProfile.Type.Local) return content
        val current = JSONObject(Libbox.formatConfig(content).value)
        if (!isDefault && !matchesTemplate(current)) return content
        val auto = group(current, "auto") ?: return content
        if (auto.optString("type") != "urltest") return content
        val update = Libbox.addUIConfigNodes(content, "[]", true)
        val nextAuto = group(JSONObject(Libbox.formatConfig(update.config).value), "auto") ?: return content
        // Empty imports also emit a marker and reformat JSON. Preserve the original
        // bytes when the core did not change the managed automatic group's members.
        return if (sameValue(auto.opt("outbounds"), nextAuto.opt("outbounds"))) content else update.config
    }

    private fun matchesTemplate(current: JSONObject): Boolean {
        // Ordinary local files qualify only while their template skeleton is unchanged.
        // Proxy additions and group selections can vary without granting default identity.
        if (!sameObject(template, current, nodeSections)) return false
        val templateAuto = group(template, "auto") ?: return false
        val templateProxy = group(template, "proxy") ?: return false
        val auto = group(current, templateAuto.getString("tag")) ?: return false
        val proxy = group(current, templateProxy.getString("tag")) ?: return false
        if (!sameObject(templateAuto, auto, groupSelections) ||
            !sameObject(templateProxy, proxy, groupSelections)) return false
        val members = proxy.optJSONArray("outbounds") ?: return false
        return (0 until members.length()).any { members.optString(it) == templateAuto.getString("tag") }
    }

    private fun group(root: JSONObject, tag: String): JSONObject? {
        val outbounds = root.optJSONArray("outbounds") ?: return null
        return (0 until outbounds.length()).asSequence().mapNotNull { outbounds.optJSONObject(it) }
            .filter { it.optString("tag") == tag }.singleOrNull()
    }

    private fun sameObject(left: JSONObject, right: JSONObject, ignored: Set<String> = emptySet()): Boolean {
        val keys = left.keys().asSequence().filterNot { it in ignored }.toSet()
        val otherKeys = right.keys().asSequence().filterNot { it in ignored }.toSet()
        return keys == otherKeys && keys.all { sameValue(left.get(it), right.get(it)) }
    }

    private fun sameValue(left: Any?, right: Any?): Boolean = when {
        left is JSONObject && right is JSONObject -> sameObject(left, right)
        left is JSONArray && right is JSONArray -> left.length() == right.length() &&
            (0 until left.length()).all { sameValue(left.get(it), right.get(it)) }
        else -> left == right
    }
}
