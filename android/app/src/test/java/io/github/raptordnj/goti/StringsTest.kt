package io.github.raptordnj.goti

import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Test

/** Spec copy lives in strings.xml; unit tests run with the module as working dir. */
class StringsTest {
    private val xml = File("src/main/res/values/strings.xml").readText()

    @Test fun timeoutCopy() {
        assertTrue(xml.contains("<string name=\"timeout_title\">Goti</string>"))
        assertTrue(xml.contains("<string name=\"timeout_text\">Downloads paused by Android — tap to resume</string>"))
    }
}
