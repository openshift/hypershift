package etcdsanfix

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirmYesNo(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"y", true},
		{"Y\n", false},
		{"yes\n", false},
		{"n\n", false},
		{"\n", false},
		{"", false},
	}
	for _, tc := range cases {
		got, err := confirm(strings.NewReader(tc.in), &bytes.Buffer{}, step{title: "t"})
		if err != nil {
			t.Fatalf("in=%q err=%v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("confirm(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestConfirmToken(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"PATCH-CERT\n", true},
		{"PATCH-CERT \n", true},
		{"patch-cert\n", false},
		{"y\n", false},
		{"", false},
	}
	for _, tc := range cases {
		got, err := confirm(strings.NewReader(tc.in), &bytes.Buffer{}, step{title: "d", token: "PATCH-CERT"})
		if err != nil {
			t.Fatalf("in=%q err=%v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("confirm(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestRenderShowsVerify(t *testing.T) {
	var buf bytes.Buffer
	_, _ = confirm(strings.NewReader("n\n"), &buf, step{title: "p", verify: []string{"etcdctl member list"}})
	if !strings.Contains(buf.String(), "$ etcdctl member list") {
		t.Errorf("render missing verify command; got:\n%s", buf.String())
	}
}
