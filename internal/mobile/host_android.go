//go:build android

package mobile

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	gioapp "gioui.org/app"
	"git.wow.st/gmp/jni"

	"github.com/raptordnj/goti/internal/platform"
)

const bridgeClass = "io.github.raptordnj.goti.Bridge"

// jniHost calls the Kotlin Bridge's static methods.
type jniHost struct {
	log *slog.Logger

	once sync.Once
	cls  jni.Class // global ref
	err  error
}

func platformHost(log *slog.Logger) uriHost {
	if log == nil {
		log = slog.Default()
	}
	return &jniHost{log: log}
}

func installOpeners(open, share func(string) error) {
	platform.AndroidOpen, platform.AndroidShare = open, share
}

// do runs f with the Bridge class (loaded through the app context's class
// loader: FindClass fails on Go-created threads) and the app context.
func (h *jniHost) do(name string, f func(env jni.Env, cls jni.Class, ctx jni.Object) error) error {
	vm := jni.JVMFor(gioapp.JavaVM())
	ctx := jni.Object(gioapp.AppContext())
	if ctx == 0 {
		return errors.New("no android app context")
	}
	err := jni.Do(vm, func(env jni.Env) error {
		h.once.Do(func() {
			loader := jni.ClassLoaderFor(env, ctx)
			c, err := jni.LoadClass(env, loader, bridgeClass)
			if err != nil {
				h.err = fmt.Errorf("load %s: %w", bridgeClass, err)
				return
			}
			h.cls = jni.Class(jni.NewGlobalRef(env, jni.Object(c)))
		})
		if h.err != nil {
			return h.err
		}
		return f(env, h.cls, ctx)
	})
	if err != nil {
		h.log.Warn("bridge call failed", "method", name, "err", err)
	}
	return err
}

func (h *jniHost) void(name, sig string, args func(env jni.Env) []jni.Value) {
	_ = h.do(name, func(env jni.Env, cls jni.Class, ctx jni.Object) error {
		m := jni.GetStaticMethodID(env, cls, name, sig)
		return jni.CallStaticVoidMethod(env, cls, m, append([]jni.Value{jni.Value(ctx)}, args(env)...)...)
	})
}

func noArgs(jni.Env) []jni.Value { return nil }

func jbool(b bool) jni.Value {
	if b {
		return jni.Value(jni.TRUE)
	}
	return jni.Value(jni.FALSE)
}

func (h *jniHost) StartService() {
	h.void("startService", "(Landroid/content/Context;)V", noArgs)
}

func (h *jniHost) UpdateNotification(n Notification) {
	h.void("updateNotification", "(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;IZ)V", func(env jni.Env) []jni.Value {
		return []jni.Value{jni.Value(jni.JavaString(env, n.Title)), jni.Value(jni.JavaString(env, n.Text)),
			jni.Value(uint64(uint32(int32(n.Percent)))), jbool(n.Paused)}
	})
}

func (h *jniHost) StopService(completed int) {
	h.void("stopService", "(Landroid/content/Context;I)V", func(jni.Env) []jni.Value {
		return []jni.Value{jni.Value(uint64(uint32(int32(completed))))}
	})
}

func (h *jniHost) RequestNotificationPermission() {
	h.void("requestNotificationPermission", "(Landroid/content/Context;)V", noArgs)
}

func (h *jniHost) PublishToDownloads(path, name, mime string) (string, error) {
	var uri string
	err := h.do("publishToDownloads", func(env jni.Env, cls jni.Class, ctx jni.Object) error {
		m := jni.GetStaticMethodID(env, cls, "publishToDownloads",
			"(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;")
		o, err := jni.CallStaticObjectMethod(env, cls, m, jni.Value(ctx), jni.Value(jni.JavaString(env, path)),
			jni.Value(jni.JavaString(env, name)), jni.Value(jni.JavaString(env, mime)))
		if err != nil {
			return err
		}
		uri = jni.GoString(env, jni.String(o))
		return nil
	})
	if err != nil {
		return "", err
	}
	if uri == "" {
		return "", errors.New("publish to Downloads failed")
	}
	return uri, nil
}

func (h *jniHost) uriCall(method, uri, mime string) bool {
	var ok bool
	_ = h.do(method, func(env jni.Env, cls jni.Class, ctx jni.Object) error {
		m := jni.GetStaticMethodID(env, cls, method, "(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;)Z")
		var err error
		ok, err = jni.CallStaticBooleanMethod(env, cls, m, jni.Value(ctx), jni.Value(jni.JavaString(env, uri)), jni.Value(jni.JavaString(env, mime)))
		return err
	})
	return ok
}

func (h *jniHost) OpenURI(uri, mime string) bool  { return h.uriCall("openUri", uri, mime) }
func (h *jniHost) ShareURI(uri, mime string) bool { return h.uriCall("shareUri", uri, mime) }
