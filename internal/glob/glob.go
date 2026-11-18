// Package glob implements the import-path globs of the policy contract
// (docs/superpowers/specs/policy/README.md, section 3): "/"-separated
// segments that are literal text, "*" (exactly one segment) or "**" (zero or
// more whole segments). Matching is whole-path, bytewise and case-sensitive.
package glob

import (
	"errors"
	"fmt"
	"strings"
)

// Validate reports why pattern is not a valid import-path glob.
func Validate(pattern string) error {
	if pattern == "" {
		return errors.New("empty pattern")
	}
	if strings.ContainsAny(pattern, " \t\r\n") {
		return errors.New("whitespace in pattern")
	}
	for _, seg := range strings.Split(pattern, "/") {
		if err := validSegment(seg); err != nil {
			return fmt.Errorf("segment %q: %w", seg, err)
		}
	}
	return nil
}

func validSegment(seg string) error {
	switch {
	case seg == "":
		return errors.New("empty segment (leading, trailing or doubled /)")
	case seg == "." || seg == "..":
		return errors.New("dot segment")
	case seg == "*" || seg == "**":
		return nil
	case strings.Contains(seg, "*"):
		return errors.New("wildcard must be a whole segment")
	case strings.ContainsAny(seg, "?[]{}\\"):
		return errors.New("unsupported metacharacter")
	}
	return nil
}

// Match reports whether path matches pattern. pattern must be valid.
func Match(pattern, path string) bool {
	return match(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func match(p, s []string) bool {
	for len(p) > 0 {
		switch p[0] {
		case "**":
			for i := 0; i <= len(s); i++ {
				if match(p[1:], s[i:]) {
					return true
				}
			}
			return false
		case "*":
			if len(s) == 0 || s[0] == "" {
				return false
			}
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
		}
		p, s = p[1:], s[1:]
	}
	return len(s) == 0
}
