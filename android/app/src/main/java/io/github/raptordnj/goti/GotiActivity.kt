package io.github.raptordnj.goti

import android.app.Activity
import android.content.Intent
import android.content.res.Configuration
import android.os.Bundle
import android.widget.FrameLayout
import org.gioui.GioView
import java.lang.ref.WeakReference

/** Hosts the Gio window; mirrors org.gioui.GioActivity (which is final). */
class GotiActivity : Activity() {
    private lateinit var view: GioView

    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        val layer = FrameLayout(this)
        view = GioView(this)
        view.layoutParams = FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT)
        view.isFocusable = true
        view.isFocusableInTouchMode = true
        layer.addView(view)
        setContentView(layer)
        Bridge.activity = WeakReference(this)
        onNewIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        if (intent.action == Intent.ACTION_SEND) {
            ShareIntents.extractUrl(intent.getStringExtra(Intent.EXTRA_TEXT))?.let { Bridge.nativeSharedUrl(it) }
        }
    }

    override fun onDestroy() {
        view.destroy()
        if (Bridge.activity?.get() === this) Bridge.activity = null
        super.onDestroy()
    }

    override fun onStart() {
        super.onStart()
        view.start()
    }

    override fun onStop() {
        view.stop()
        super.onStop()
    }

    override fun onPause() {
        super.onPause()
        view.pause()
    }

    override fun onResume() {
        super.onResume()
        Bridge.activity = WeakReference(this)
        view.resume()
    }

    override fun onConfigurationChanged(c: Configuration) {
        super.onConfigurationChanged(c)
        view.configurationChanged()
    }

    @Deprecated("Deprecated in Java")
    override fun onLowMemory() {
        super.onLowMemory()
        GioView.onLowMemory()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (!view.backPressed()) super.onBackPressed()
    }
}
