package logging

import "testing"

func TestParseLevel(t *testing.T) {
	cases := map[string]bool{ // level -> should succeed
		"debug": true,
		"info":  true,
		"":     true,
		"warn":  true,
		"ERROR": true,
		"fatal": true, // documented in LoggingConfig.Level
		"nope":  false,
	}
	for level, ok := range cases {
		_, err := parseLevel(level)
		if ok && err != nil {
			t.Errorf("parseLevel(%q): unexpected error %v", level, err)
		}
		if !ok && err == nil {
			t.Errorf("parseLevel(%q): expected error", level)
		}
	}
}
