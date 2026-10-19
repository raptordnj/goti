package io.github.raptordnj.goti

import org.junit.Assert.assertEquals
import org.junit.Test

class NotificationsTest {
    @Test fun actions() {
        assertEquals("pause_all", Notifications.actionFor(false))
        assertEquals("resume_all", Notifications.actionFor(true))
    }

    @Test fun labels() {
        assertEquals("Pause all", Notifications.labelFor(false))
        assertEquals("Resume all", Notifications.labelFor(true))
    }
}
