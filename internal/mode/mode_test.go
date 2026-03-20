package mode

import "testing"

func TestModeAllows(t *testing.T) {
	if Lantern.Allows(ClassWriteData) {
		t.Fatal("Lantern should block writes")
	}
	if !Tweezers.Allows(ClassWriteData) {
		t.Fatal("Tweezers should allow writes")
	}
	if Chisel.Allows(ClassAdmin) {
		t.Fatal("Chisel should block admin")
	}
}
