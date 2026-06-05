package mail

import (
	"testing"
	"unicode/utf8"
)

func TestDecodeCharset(t *testing.T) {
	latin1 := []byte{'g', 0xE9, 'n', 0xE9, 'r', 'a', 'l'} // "général" in Latin-1
	cases := []struct {
		name, charset, want string
		in                  []byte
	}{
		{"iso-8859-1", "iso-8859-1", "général", latin1},
		{"windows-1252", "windows-1252", "général", latin1},
		{"valid utf-8 declared utf-8", "utf-8", "général", []byte("général")},
		{"mislabeled utf-8 falls back to 1252", "utf-8", "général", latin1},
		{"no charset, 8-bit falls back to 1252", "", "général", latin1},
		{"no charset, valid utf-8 kept", "", "général", []byte("général")},
		{"unknown charset returned untouched", "x-weird", "général", []byte("général")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DecodeCharset(c.in, c.charset)
			if !utf8.ValidString(got) {
				t.Fatalf("result not valid UTF-8: %q", got)
			}
			if got != c.want {
				t.Fatalf("DecodeCharset(%v, %q) = %q, want %q", c.in, c.charset, got, c.want)
			}
		})
	}
}
