package demo

import (
	"fmt"
	"testing"
)

func TestAdd(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{
			a:        1,
			b:        2,
			expected: 3,
		},
		{
			a:        1,
			b:        2,
			expected: 3,
		},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("Add(%d, %d)", test.a, test.b), func(t *testing.T) {
			actual := Add(test.a, test.b)
			if actual != test.expected {
				t.Errorf("Add(%d, %d) = %d, expected %d", test.a, test.b, actual, test.expected)
			}
		})
	}
}
