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
			defer jni.DeleteLocalRef(env, loader)
			c, err := jni.LoadClass(env, loader, bridgeClass)
			if c != 0 {
				defer jni.DeleteLocalRef(env, jni.Object(c))
			}
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

func (h *jniHost) void(name, sig string, args func(l *locals) []jni.Value) {
	_ = h.do(name, func(env jni.Env, cls jni.Class, ctx jni.Object) error {
		l := &locals{env: env}
		defer l.free()
		m := jni.GetStaticMethodID(env, cls, name, sig)
		return jni.CallStaticVoidMethod(env, cls, m, append([]jni.Value{jni.Value(ctx)}, args(l)...)...)
	})
}

func noArgs(*locals) []jni.Value { return nil }

// locals tracks local refs created during one call so they are deleted
// even when the thread stays attached (e.g. a JVM-owned thread).
type locals struct {
	env  jni.Env
	refs []jni.Object
}

func (l *locals) str(s string) jni.Value {
	o := jni.Object(jni.JavaString(l.env, s))
	l.refs = append(l.refs, o)
	return jni.Value(o)
}

func (l *locals) free() {
	for _, o := range l.refs {
		jni.DeleteLocalRef(l.env, o)
	}
}

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
	h.void("updateNotification", "(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;IZ)V", func(l *locals) []jni.Value {
		return []jni.Value{l.str(n.Title), l.str(n.Text),
			jni.Value(uint64(uint32(int32(n.Percent)))), jbool(n.Paused)}
	})
}

func (h *jniHost) StopService(completed int) {
	h.void("stopService", "(Landroid/content/Context;I)V", func(*locals) []jni.Value {
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
		l := &locals{env: env}
		defer l.free()
		o, err := jni.CallStaticObjectMethod(env, cls, m, jni.Value(ctx), l.str(path), l.str(name), l.str(mime))
		if o != 0 {
			l.refs = append(l.refs, o)
		}
		if err != nil {
			return err
		}
		if o != 0 {
			uri = jni.GoString(env, jni.String(o))
		}
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
		l := &locals{env: env}
		defer l.free()
		var err error
		ok, err = jni.CallStaticBooleanMethod(env, cls, m, jni.Value(ctx), l.str(uri), l.str(mime))
		return err
	})
	return ok
}

func (h *jniHost) OpenURI(uri, mime string) bool  { return h.uriCall("openUri", uri, mime) }
func (h *jniHost) ShareURI(uri, mime string) bool { return h.uriCall("shareUri", uri, mime) }
