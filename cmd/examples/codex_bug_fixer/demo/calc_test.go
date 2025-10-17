//go:build broken

package demo

import "testing"

func TestSubtract(t *testing.T) {
	if got := Subtract(5, 3); got != 2 {
		t.Fatalf("Subtract(5, 3) = %d, want 2", got)
	}
}
