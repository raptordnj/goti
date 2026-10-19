package mobile

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/state"
)

func TestNotificationRunning(t *testing.T) {
	s := snap(2, 0, 0, 0)
	const mb = 100 << 20
	for i := 0; i < 2; i++ {
		s.Tasks = append(s.Tasks, state.TaskView{Section: state.SectionActive, Downloaded: mb / 2, Total: mb, TotalKnown: true})
	}
	n := NotificationFor(s)
	if n.Title != "Goti · 2 downloading" || n.Percent != 50 || n.Paused {
		t.Fatalf("%+v", n)
	}
	if !strings.HasPrefix(n.Text, platform.FormatSpeed(1<<20)+" · ") || !strings.HasSuffix(n.Text, " left") {
		t.Fatalf("text %q", n.Text)
	}
}

func TestNotificationUnknownTotal(t *testing.T) {
	s := snap(1, 0, 0, 0)
	s.Tasks = []state.TaskView{{Section: state.SectionActive, Downloaded: 5}}
	n := NotificationFor(s)
	if n.Percent != -1 || n.Text != platform.FormatSpeed(1<<20) {
		t.Fatalf("%+v", n)
	}
}

func TestNotificationPaused(t *testing.T) {
	n := NotificationFor(snap(0, 0, 3, 0))
	want := Notification{Title: "Goti", Text: "Paused · 3 downloads", Percent: -1, Paused: true}
	if n != want {
		t.Fatalf("%+v", n)
	}
}

func TestNotificationQueuedOnly(t *testing.T) {
	n := NotificationFor(snap(0, 4, 0, 0))
	if n.Title != "Goti · 4 queued" || n.Percent != -1 {
		t.Fatalf("%+v", n)
	}
}

func TestMobileImportsPure(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range strings.Fields(string(out)) {
		if strings.HasPrefix(d, "gioui.org") || strings.Contains(d, "jni") {
			t.Errorf("forbidden import %s", d)
		}
	}
}
