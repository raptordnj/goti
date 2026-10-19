package icons

import (
	"image"
	"image/color"
	"reflect"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func all() map[string]Icon {
	return map[string]Icon{
		"Plus": Plus, "Pause": Pause, "Play": Play, "Close": Close, "Retry": Retry,
		"Restart": Restart, "Folder": Folder, "File": File, "Archive": Archive,
		"Video": Video, "Audio": Audio, "Image": Image, "Doc": Doc, "Disk": Disk,
		"More": More, "Settings": Settings, "Check": Check, "Alert": Alert,
		"WifiOff": WifiOff, "ArrowDown": ArrowDown, "ChevronRight": ChevronRight,
		"ChevronDown": ChevronDown, "Copy": Copy, "Trash": Trash, "Share": Share, "ArrowLeft": ArrowLeft,
	}
}

func TestIconsBounds(t *testing.T) {
	for name, ic := range all() {
		if ic == nil {
			t.Fatalf("%s is nil", name)
		}
		for _, sz := range []unit.Dp{16, 24} {
			ops := new(op.Ops)
			gtx := layout.Context{Ops: ops, Metric: unit.Metric{PxPerDp: 2, PxPerSp: 2},
				Constraints: layout.Exact(image.Pt(500, 500))}
			gtx.Constraints.Min = image.Point{}
			d := ic(gtx, sz, color.NRGBA{A: 255})
			want := gtx.Dp(sz)
			if d.Size != image.Pt(want, want) {
				t.Errorf("%s@%v: size %v want %d", name, sz, d.Size, want)
			}
			if reflect.ValueOf(&ops.Internal).Elem().FieldByName("data").Len() == 0 {
				t.Errorf("%s@%v: no ops recorded", name, sz)
			}
		}
	}
}

func TestForFile(t *testing.T) {
	cases := map[string]string{
		"a.zip": "archive", "b.TAR.GZ": "archive", "c.7z": "archive", "m.mp4": "video", "m.mkv": "video",
		"s.mp3": "audio", "s.flac": "audio", "p.PNG": "image", "p.jpg": "image", "d.pdf": "doc",
		"d.docx": "doc", "x.iso": "disk", "x.dmg": "disk", "noext": "file", "x.bin": "file",
	}
	for n, want := range cases {
		if got := KindFor(n); got != want {
			t.Errorf("KindFor(%q)=%q want %q", n, got, want)
		}
		if ForFile(n) == nil {
			t.Errorf("ForFile(%q) nil", n)
		}
	}
}
