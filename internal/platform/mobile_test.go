package platform

import "testing"

func TestMobileFalseOnDesktop(t *testing.T) {
	if Mobile {
		t.Fatal("Mobile must be false off android")
	}
}
