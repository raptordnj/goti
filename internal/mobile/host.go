// Package mobile holds the pure-Go Android service logic: notification model,
// service controller and the Downloads publisher. It must not import gioui.org
// or JNI so it stays desktop-testable.
package mobile

// Host is the platform side (Kotlin via JNI on Android, a fake in tests).
type Host interface {
	StartService()
	UpdateNotification(n Notification)
	StopService(completed int) // completed>0 shows "Downloads complete · N files"
	PublishToDownloads(path, name, mime string) (uri string, err error)
	RequestNotificationPermission()
}
