package main

import "testing"

func TestVerGreater(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.2", "0.2.1", true},
		{"0.3.0", "0.2.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.2.1", "0.2.2", false},
		{"0.2.2", "0.2.2", false},
		{"0.2", "0.2.1", false},
		{"0.10.0", "0.9.0", true},
	}
	for _, c := range cases {
		if got := verGreater(c.a, c.b); got != c.want {
			t.Errorf("verGreater(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
