package images

import "testing"

func TestParseImageTag(t *testing.T) {
	cases := []struct {
		in      string
		app     string
		rebuild int
		ok      bool
	}{
		{"0.2.40", "0.2.40", 0, true},
		{"v0.2.40", "0.2.40", 0, true},
		{"0.2.40-r1", "0.2.40", 1, true},
		{"0.2.40-r12", "0.2.40", 12, true},
		{"0.2.40-r0", "", 0, false},
		{"0.2.40-1", "", 0, false},
		{"latest", "", 0, false},
	}
	for _, tc := range cases {
		parsed, err := ParseImageTag(tc.in)
		if tc.ok {
			if err != nil {
				t.Fatalf("%q: %v", tc.in, err)
			}
			if parsed.App != tc.app || parsed.Rebuild != tc.rebuild {
				t.Fatalf("%q: got %+v", tc.in, parsed)
			}
		} else if err == nil {
			t.Fatalf("%q: expected error", tc.in)
		}
	}
}

func TestAppGitTag(t *testing.T) {
	if AppGitTag("0.2.40") != "v0.2.40" {
		t.Fatal("app git tag")
	}
}
