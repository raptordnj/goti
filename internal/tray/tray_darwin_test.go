//go:build darwin

package tray

import "testing"

func TestDarwinTrayDisabled(t *testing.T) {
	if ok, stop := Start(Deps{}); ok {
		stop()
		t.Fatal("tray must stay disabled on macOS")
	}
}
