package io.github.raptordnj.goti

/** Extracts a download URL from shared text. Pure: JVM-testable. */
object ShareIntents {
    private val url = Regex("""https?://\S+""")
    private const val TRAILING = ".,;:!?)]}'\""

    /** First http(s) URL in [text] with trailing punctuation trimmed, or null. */
    fun extractUrl(text: String?): String? {
        val m = url.find(text ?: return null) ?: return null
        return m.value.trimEnd { it in TRAILING }.takeIf { it.length > "http://".length }
    }
}
