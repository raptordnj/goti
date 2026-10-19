package io.github.raptordnj.goti

import android.Manifest
import android.app.NotificationManager
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Log
import java.lang.ref.WeakReference

/** Static methods called from Go (internal/mobile/host_android.go) and Go natives. */
object Bridge {
    private val main = Handler(Looper.getMainLooper())

    /** The visible activity, for permission requests. */
    @Volatile var activity: WeakReference<GotiActivity>? = null

    @JvmStatic external fun nativeAction(name: String)
    @JvmStatic external fun nativeSharedUrl(url: String, autoAdd: Boolean)
    @JvmStatic external fun nativeServiceTimeout()

    @JvmStatic fun startService(ctx: Context) {
        try {
            ctx.startForegroundService(Intent(ctx, DownloadService::class.java))
        } catch (e: Exception) { // ForegroundServiceStartNotAllowedException in background (API 31+)
            Log.w(DownloadService.TAG, "cannot start download service", e)
        }
    }

    @JvmStatic fun updateNotification(ctx: Context, title: String, text: String, percent: Int, paused: Boolean) {
        Notifications.ensureChannel(ctx)
        val n = Notifications.ongoing(ctx, title, text, percent, paused)
        DownloadService.current = n
        ctx.getSystemService(NotificationManager::class.java).notify(Notifications.ID_ONGOING, n)
    }

    @JvmStatic fun stopService(ctx: Context, completed: Int) {
        ctx.stopService(Intent(ctx, DownloadService::class.java))
        DownloadService.current = null
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.cancel(Notifications.ID_ONGOING)
        if (completed > 0) {
            Notifications.ensureChannel(ctx)
            nm.notify(Notifications.ID_COMPLETE, Notifications.complete(ctx, completed))
        }
    }

    @JvmStatic fun publishToDownloads(ctx: Context, path: String, name: String, mime: String): String? =
        MediaStorePublisher.publish(ctx, path, name, mime)

    @JvmStatic fun openUri(ctx: Context, uri: String, mime: String): Boolean =
        launch(ctx, uri) { u ->
            Intent(Intent.ACTION_VIEW).setDataAndType(u, mime.ifEmpty { "*/*" })
        }

    @JvmStatic fun shareUri(ctx: Context, uri: String, mime: String): Boolean =
        launch(ctx, uri) { u ->
            Intent.createChooser(
                Intent(Intent.ACTION_SEND).setType(mime.ifEmpty { "*/*" }).putExtra(Intent.EXTRA_STREAM, u)
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION), null,
            )
        }

    /** Only content:// URIs can be handed to other apps (no FileProvider). */
    private fun launch(ctx: Context, uri: String, build: (Uri) -> Intent): Boolean {
        val u = Uri.parse(uri)
        if (u.scheme != "content") return false
        return try {
            ctx.startActivity(build(u).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_GRANT_READ_URI_PERMISSION))
            true
        } catch (e: ActivityNotFoundException) {
            false
        }
    }

    @JvmStatic fun requestNotificationPermission(ctx: Context) {
        if (Build.VERSION.SDK_INT < 33) return
        if (ctx.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED) return
        main.post {
            activity?.get()?.requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
    }
}
