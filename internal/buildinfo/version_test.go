package buildinfo

import "testing"

func TestReleaseVersion(t *testing.T) {
	cases := []struct {
		module string
		want   string
		ok     bool
	}{
		{module: "v0.4.0", want: "0.4.0", ok: true},
		{module: "v1.12.3", want: "1.12.3", ok: true},
		{module: "(devel)"},
		{module: ""},
		{module: "v0.4.1-0.20261028063712-83f22efc08cf"},
		{module: "v0.4.1-0.20261028063712-83f22efc08cf+dirty"},
		{module: "v0.4.0+dirty"},
		{module: "v0.5.0-rc.1"},
		{module: "v0.4"},
		{module: "0.4.0"},
	}
	for _, tc := range cases {
		got, ok := releaseVersion(tc.module)
		if got != tc.want || ok != tc.ok {
			t.Errorf("releaseVersion(%q) = %q, %v; want %q, %v", tc.module, got, ok, tc.want, tc.ok)
		}
	}
}
