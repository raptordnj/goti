package components

import (
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/state"
)

func TestToastEnterFades(t *testing.T) {
	toasts := []state.Toast{{Kind: state.ToastInfo, Text: "hi", At: time.Unix(5, 0)}}
	var f ToastFades
	gtx := newGtx()
	ToastStack(lth, &f, toasts, nil)(gtx)
	fd := f.m[toastKey{"hi", time.Unix(5, 0)}]
	if fd == nil {
		t.Fatal("no fade entry")
	}
	if a := fd.Alpha(gtx); a >= 1 {
		t.Fatalf("first-frame alpha = %v, want < 1", a)
	}
	for range 12 { // 192 ms of 16-ms frames
		gtx.Now = gtx.Now.Add(16 * time.Millisecond)
		ToastStack(lth, &f, toasts, nil)(gtx)
	}
	if a := fd.Alpha(gtx); a != 1 {
		t.Fatalf("alpha after 192 ms = %v, want 1", a)
	}
	for range 14 { // the leaving copy fades out over 150 ms
		ToastStack(lth, &f, nil, nil)(gtx)
		gtx.Now = gtx.Now.Add(16 * time.Millisecond)
	}
	if len(f.m) != 0 {
		t.Fatalf("fade entries not freed: %d", len(f.m))
	}

	prev := anim.Reduce
	anim.Reduce = func() bool { return true }
	t.Cleanup(func() { anim.Reduce = prev })
	var g ToastFades
	ToastStack(lth, &g, toasts, nil)(newGtx())
	if a := g.m[toastKey{"hi", time.Unix(5, 0)}].Alpha(newGtx()); a != 1 {
		t.Fatalf("reduced alpha = %v, want 1", a)
	}
}

func TestToastLeaveFades(t *testing.T) {
	toasts := []state.Toast{{Kind: state.ToastInfo, Text: "bye", At: time.Unix(5, 0)}}
	var f ToastFades
	gtx := newGtx()
	ToastStack(lth, &f, toasts, nil)(gtx)
	gtx.Now = gtx.Now.Add(300 * time.Millisecond)
	ToastStack(lth, &f, toasts, nil)(gtx)
	ToastStack(lth, &f, nil, nil)(gtx)
	if f.Leaving() != 1 {
		t.Fatalf("leaving %d, want 1", f.Leaving())
	}
	for range 12 {
		gtx.Now = gtx.Now.Add(16 * time.Millisecond)
		ToastStack(lth, &f, nil, nil)(gtx)
	}
	if f.Leaving() != 0 || len(f.m) != 0 {
		t.Fatalf("leaving copy not pruned: %d %d", f.Leaving(), len(f.m))
	}

	prev := anim.Reduce
	anim.Reduce = func() bool { return true }
	t.Cleanup(func() { anim.Reduce = prev })
	var g ToastFades
	ToastStack(lth, &g, toasts, nil)(newGtx())
	ToastStack(lth, &g, nil, nil)(newGtx())
	if g.Leaving() != 0 {
		t.Fatal("reduced motion kept a leaving toast")
	}
}
