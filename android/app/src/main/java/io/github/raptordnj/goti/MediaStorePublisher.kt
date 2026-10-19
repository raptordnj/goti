package io.github.raptordnj.goti

import android.content.ContentValues
import android.content.Context
import android.media.MediaScannerConnection
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.util.Log
import java.io.File

/** Copies a completed download into the shared Downloads collection. */
object MediaStorePublisher {
    /** Returns the published uri string, or null on failure. */
    fun publish(ctx: Context, path: String, name: String, mime: String): String? =
        try {
            if (Build.VERSION.SDK_INT >= 29) viaMediaStore(ctx, File(path), name, mime) else legacy(ctx, File(path), name, mime)
        } catch (e: Exception) {
            Log.w(DownloadService.TAG, "publish failed", e)
            null
        }

    private fun viaMediaStore(ctx: Context, src: File, name: String, mime: String): String? {
        val cr = ctx.contentResolver
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            if (mime.isNotEmpty()) put(MediaStore.Downloads.MIME_TYPE, mime)
            put(MediaStore.Downloads.RELATIVE_PATH, "Download/Goti")
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val uri = cr.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: return null
        try {
            val copied = cr.openOutputStream(uri)?.use { out -> src.inputStream().use { it.copyTo(out) } }
            if (copied != src.length()) error("copied $copied of ${src.length()} bytes")
            cr.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
            return uri.toString()
        } catch (e: Exception) {
            cr.delete(uri, null, null)
            throw e
        }
    }

    @Suppress("DEPRECATION")
    private fun legacy(ctx: Context, src: File, name: String, mime: String): String? {
        val dir = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), "Goti")
        if (!dir.isDirectory && !dir.mkdirs()) return null
        var dst = File(dir, name)
        var i = 1
        while (dst.exists()) {
            dst = File(dir, "${name.substringBeforeLast('.', name)} ($i)" +
                (if (name.contains('.')) "." + name.substringAfterLast('.') else ""))
            i++
        }
        val copied = src.inputStream().use { inp -> dst.outputStream().use { inp.copyTo(it) } }
        if (copied != src.length()) {
            dst.delete()
            return null
        }
        MediaScannerConnection.scanFile(ctx, arrayOf(dst.path), arrayOf(mime.ifEmpty { null }), null)
        return Uri.fromFile(dst).toString()
    }
}
