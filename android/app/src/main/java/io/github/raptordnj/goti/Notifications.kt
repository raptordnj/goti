package io.github.raptordnj.goti

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent

/** Notification channel, ids and builders for the download service. */
object Notifications {
    const val CHANNEL = "downloads"
    const val ID_ONGOING = 1
    const val ID_COMPLETE = 2
    const val ID_TIMEOUT = 3

    /** Action sent by the toggle button: resume when paused, else pause. */
    fun actionFor(paused: Boolean): String = if (paused) "resume_all" else "pause_all"

    /** Label of the toggle button. */
    fun labelFor(paused: Boolean): String = if (paused) "Resume all" else "Pause all"

    fun ensureChannel(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, ctx.getString(R.string.channel_downloads), NotificationManager.IMPORTANCE_LOW)
            )
        }
    }

    private fun openApp(ctx: Context): PendingIntent =
        PendingIntent.getActivity(
            ctx, 0,
            Intent(ctx, GotiActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    private fun actionIntent(ctx: Context, name: String): PendingIntent =
        PendingIntent.getService(
            ctx, name.hashCode(),
            Intent(ctx, DownloadService::class.java).setAction(DownloadService.ACTION_NOTIFICATION)
                .putExtra(DownloadService.EXTRA_ACTION, name),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    /** Ongoing service notification (id 1); percent -1 = indeterminate. */
    fun ongoing(ctx: Context, title: String, text: String, percent: Int, paused: Boolean): Notification {
        val b = Notification.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_goti)
            .setContentTitle(title)
            .setContentText(text)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(openApp(ctx))
            .addAction(Notification.Action.Builder(null, labelFor(paused), actionIntent(ctx, actionFor(paused))).build())
            .addAction(Notification.Action.Builder(null, ctx.getString(R.string.open), openApp(ctx)).build())
        if (!paused) {
            if (percent < 0) b.setProgress(0, 0, true) else b.setProgress(100, percent.coerceAtMost(100), false)
        }
        return b.build()
    }

    /** "Downloads complete · N files" (id 2). */
    fun complete(ctx: Context, n: Int): Notification =
        Notification.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_goti)
            .setContentTitle("Downloads complete · $n files")
            .setAutoCancel(true)
            .setContentIntent(openApp(ctx))
            .build()

    /** Posted when Android stops the dataSync service at its time limit (id 3). */
    fun timeout(ctx: Context): Notification =
        Notification.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_goti)
            .setContentTitle(ctx.getString(R.string.timeout_title))
            .setContentText(ctx.getString(R.string.timeout_text))
            .setAutoCancel(true)
            .setContentIntent(openApp(ctx))
            .build()
}
