package io.github.raptordnj.goti

import android.app.ForegroundServiceStartNotAllowedException
import android.app.Notification
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import org.gioui.Gio

/** dataSync foreground service that keeps the Go engine running. */
class DownloadService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null

    override fun onCreate() {
        super.onCreate()
        Gio.init(applicationContext) // starts Go if restarted without the activity
        Notifications.ensureChannel(this)
        wakeLock = getSystemService(PowerManager::class.java)
            .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "goti:download")
            .apply { acquire(WAKE_TIMEOUT_MS) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        try {
            val n = current ?: Notifications.ongoing(this, "Goti", "", -1, false)
            if (Build.VERSION.SDK_INT >= 29) {
                startForeground(Notifications.ID_ONGOING, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
            } else {
                startForeground(Notifications.ID_ONGOING, n)
            }
        } catch (e: Exception) {
            if (Build.VERSION.SDK_INT >= 31 && e is ForegroundServiceStartNotAllowedException) {
                Log.w(TAG, "foreground start not allowed", e)
                stopSelf()
                return START_NOT_STICKY
            }
            throw e
        }
        if (intent?.action == ACTION_NOTIFICATION) {
            intent.getStringExtra(EXTRA_ACTION)?.let { Bridge.nativeAction(it) }
        }
        return START_STICKY
    }

    override fun onTimeout(startId: Int, fgsType: Int) {
        Log.w(TAG, "dataSync time limit reached")
        Bridge.nativeServiceTimeout()
        getSystemService(NotificationManager::class.java).notify(Notifications.ID_TIMEOUT, Notifications.timeout(this))
        stopSelf()
    }

    override fun onDestroy() {
        wakeLock?.takeIf { it.isHeld }?.release()
        wakeLock = null
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    companion object {
        const val TAG = "goti"
        const val ACTION_NOTIFICATION = "io.github.raptordnj.goti.NOTIFICATION_ACTION"
        const val EXTRA_ACTION = "action"
        private const val WAKE_TIMEOUT_MS = 6L * 60 * 60 * 1000

        /** Latest ongoing notification, reused when the service (re)starts. */
        @Volatile var current: Notification? = null
    }
}
