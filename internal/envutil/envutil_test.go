package envutil

import "testing"

func TestStringSlice(t *testing.T) {
	cases := []struct {
		name string
		env  string
		def  string
		want []string
	}{
		{"empty default", "", "", nil},
		{"single", "", "a", []string{"a"}},
		{"comma", "", "a,b,c", []string{"a", "b", "c"}},
		{"trim whitespace", "", " a , b ,c ", []string{"a", "b", "c"}},
		{"drop empty", "", "a,,b,", []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("TEST_SLICE", c.env)
			}
			got := StringSlice("TEST_SLICE", c.def)
			if !equal(got, c.want) {
				t.Fatalf("want %v, got %v", c.want, got)
			}
		})
	}
}

func TestBool(t *testing.T) {
	cases := []struct {
		env  string
		want bool
	}{
		{"true", true}, {"1", true}, {"yes", true}, {"ON", true},
		{"false", false}, {"0", false}, {"no", false}, {"off", false},
	}
	for _, c := range cases {
		t.Setenv("TEST_BOOL", c.env)
		if got := Bool("TEST_BOOL", !c.want); got != c.want {
			t.Errorf("env=%q want %v got %v", c.env, c.want, got)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
