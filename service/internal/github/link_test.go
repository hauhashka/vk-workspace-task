package github

import "testing"

func TestNextLink(t *testing.T) {
	base := "https://api.github.com/repos/cli/cli/issues?per_page=100"
	cases := []struct{ header, want string }{
		{"", ""},
		{`<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=7>; rel="last"`, "https://api.github.com/x?page=2"},
		{`<https://api.github.com/x?page=7>; rel="last", <https://api.github.com/x?page=1>; rel="prev"`, ""},
		{`<https://api.github.com/x?a=1,2&page=3>; rel="next"`, "https://api.github.com/x?a=1,2&page=3"},
		{`</repos/cli/cli/issues?page=2>; rel=next`, "https://api.github.com/repos/cli/cli/issues?page=2"},
		{`<https://h/x?page=2>; title="t"; REL="prev next"`, "https://h/x?page=2"},
		{`garbage, <broken`, ""},
	}
	for _, tc := range cases {
		if got := NextLink(tc.header, base); got != tc.want {
			t.Errorf("NextLink(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}
