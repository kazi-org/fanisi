package clamp

import "testing"

func TestClamp(t *testing.T) {
	for _, tc := range []struct {
		name            string
		value, min, max int
		want            int
	}{
		{"below", -2, 0, 10, 0},
		{"above", 12, 0, 10, 10},
		{"inside", 4, 0, 10, 4},
		{"negative_interval", -20, -10, -5, -10},
		{"lower_boundary", 0, 0, 10, 0},
		{"upper_boundary", 10, 0, 10, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clamp(tc.value, tc.min, tc.max); got != tc.want {
				t.Errorf("Clamp(%d, %d, %d) = %d; want %d", tc.value, tc.min, tc.max, got, tc.want)
			}
		})
	}
}
