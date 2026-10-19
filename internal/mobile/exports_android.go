//go:build android

package mobile

/*
#include <jni.h>
*/
import "C"

import (
	"unsafe"

	"git.wow.st/gmp/jni"
)

//export Java_io_github_raptordnj_goti_Bridge_nativeAction
func Java_io_github_raptordnj_goti_Bridge_nativeAction(env *C.JNIEnv, cls C.jclass, name C.jstring) {
	DefaultInbox.Dispatch(goString(env, name))
}

//export Java_io_github_raptordnj_goti_Bridge_nativeSharedUrl
func Java_io_github_raptordnj_goti_Bridge_nativeSharedUrl(env *C.JNIEnv, cls C.jclass, url C.jstring) {
	if u := goString(env, url); u != "" {
		DefaultInbox.SetSharedURL(u)
	}
}

//export Java_io_github_raptordnj_goti_Bridge_nativeServiceTimeout
func Java_io_github_raptordnj_goti_Bridge_nativeServiceTimeout(env *C.JNIEnv, cls C.jclass) {
	DefaultInbox.logger().Warn("foreground service time limit reached; pausing all downloads")
	DefaultInbox.Dispatch("pause_all")
}

func goString(env *C.JNIEnv, s C.jstring) string {
	return jni.GoString(jni.EnvFor(uintptr(unsafe.Pointer(env))), jni.String(s))
}
