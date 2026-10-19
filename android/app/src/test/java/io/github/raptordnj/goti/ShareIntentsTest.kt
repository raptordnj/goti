package io.github.raptordnj.goti

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ShareIntentsTest {
    @Test fun plainUrl() = assertEquals("https://x.y/a.zip", ShareIntents.extractUrl("https://x.y/a.zip"))
    @Test fun urlInText() = assertEquals("https://x.y/a.zip", ShareIntents.extractUrl("Check this https://x.y/a.zip !"))
    @Test fun trailingPunctuation() = assertEquals("http://h/p", ShareIntents.extractUrl("see (http://h/p)."))
    @Test fun noLink() = assertNull(ShareIntents.extractUrl("no link here"))
    @Test fun nullText() = assertNull(ShareIntents.extractUrl(null))
    @Test fun schemeOnly() = assertNull(ShareIntents.extractUrl("open https:// now"))
    @Test fun schemeOnlyPunct() = assertNull(ShareIntents.extractUrl("https://)."))
}
