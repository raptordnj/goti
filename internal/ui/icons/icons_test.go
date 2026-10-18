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
		"ChevronDown": ChevronDown,
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
	cases := map[string]Icon{
		"a.zip": Archive, "b.TAR.GZ": Archive, "c.7z": Archive, "m.mp4": Video, "m.mkv": Video,
		"s.mp3": Audio, "s.flac": Audio, "p.PNG": Image, "p.jpg": Image, "d.pdf": Doc,
		"d.docx": Doc, "x.iso": Disk, "x.dmg": Disk, "noext": File, "x.bin": File,
	}
	for n, want := range cases {
		if got := ForFile(n); reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
			t.Errorf("ForFile(%q) wrong icon", n)
		}
	}
}
