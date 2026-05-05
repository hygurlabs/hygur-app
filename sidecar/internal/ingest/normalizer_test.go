package ingest

import "testing"

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "simple lowercase",
			input: "Hello World",
			want:  "hello world",
		},
		{
			name:  "trim whitespace",
			input: "  hello world  ",
			want:  "hello world",
		},
		{
			name:  "collapse multiple spaces",
			input: "hello    world",
			want:  "hello world",
		},
		{
			name:  "newlines to spaces",
			input: "hello\nworld",
			want:  "hello world",
		},
		{
			name:  "tabs to spaces",
			input: "hello\tworld",
			want:  "hello world",
		},
		{
			name:  "mixed whitespace",
			input: "  hello  \n\t  world  ",
			want:  "hello world",
		},
		{
			name:  "carriage return",
			input: "hello\r\nworld",
			want:  "hello world",
		},
		{
			name:  "control characters removed",
			input: "hello\x00world",
			want:  "helloworld",
		},
		{
			name:  "unicode preserved",
			input: "Bonjour le monde",
			want:  "bonjour le monde",
		},
		{
			name:  "unicode with accents",
			input: "CAFE RESUME",
			want:  "cafe resume",
		},
		{
			name:  "multiple newlines",
			input: "para1\n\n\npara2",
			want:  "para1 para2",
		},
		{
			name:  "only whitespace",
			input: "   \n\t   ",
			want:  "",
		},
		{
			name:  "mixed case and whitespace",
			input: "  The QUICK   Brown FOX  ",
			want:  "the quick brown fox",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeText(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func BenchmarkNormalizeText(b *testing.B) {
	input := "  The QUICK   Brown FOX  \n\n\t  jumps over   the LAZY dog  "
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NormalizeText(input)
	}
}
