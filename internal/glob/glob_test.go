package glob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"example.com/demo/internal/repo/**", "example.com/demo/internal/repo", true}, // ** matches the root
		{"example.com/demo/internal/repo/**", "example.com/demo/internal/repo/sql/x", true},
		{"example.com/demo/internal/repo/**", "example.com/demo/internal/repository", false},
		{"example.com/demo/internal/repo", "example.com/demo/internal/repo/sql", false}, // no prefix matching
		{"example.com/demo/internal/repo/*/**", "example.com/demo/internal/repo", false},
		{"example.com/demo/internal/repo/*/**", "example.com/demo/internal/repo/sql", true},
		{"example.com/**/private", "example.com/private", true},
		{"example.com/**/private", "example.com/a/b/private", true},
		{"example.com/**/private", "example.com/a/private/x", false},
		{"example.com/*", "example.com/a", true},
		{"example.com/*", "example.com/a/b", false},
		{"Example.com/a", "example.com/a", false},
		{"**", "net/http", true},
	}
	for _, c := range cases {
		if err := Validate(c.pattern); err != nil {
			t.Fatalf("%q invalid: %v", c.pattern, err)
		}
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	for _, p := range []string{"", "repo*", "*_test", "a/**b", "a/?", "a/[x]", "a/{b}", `a\b`, "/a", "a/", "a//b", "a/./b", "a/../b", "a b"} {
		if Validate(p) == nil {
			t.Errorf("Validate(%q) accepted an invalid pattern", p)
		}
	}
}
