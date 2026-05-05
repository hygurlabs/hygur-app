package mail

import (
	"strings"
	"testing"
	"time"
)

func TestNewThreadNormalizer(t *testing.T) {
	tn := NewThreadNormalizer()

	if tn.MaxBodyLength != DefaultMaxBodyLength {
		t.Errorf("MaxBodyLength = %d, want %d", tn.MaxBodyLength, DefaultMaxBodyLength)
	}
	if !tn.IncludeMetadata {
		t.Error("IncludeMetadata should be true by default")
	}
	if !tn.StripSignatures {
		t.Error("StripSignatures should be true by default")
	}
	if !tn.StripQuotes {
		t.Error("StripQuotes should be true by default")
	}
}

func TestThreadNormalizer_Normalize_EmptyThread(t *testing.T) {
	tn := NewThreadNormalizer()
	thread := &Thread{ID: "thread-1", Subject: "Test"}

	_, err := tn.Normalize(thread, []Message{})
	if err != ErrEmptyThread {
		t.Errorf("Normalize with empty messages = %v, want ErrEmptyThread", err)
	}
}

func TestThreadNormalizer_Normalize_SingleMessage(t *testing.T) {
	tn := NewThreadNormalizer()
	thread := &Thread{ID: "thread-1", Subject: "Test"}
	date := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	messages := []Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-1",
			From:     "alice@example.com",
			Date:     date,
			Body:     "Hello, this is a test message.",
		},
	}

	result, err := tn.Normalize(thread, messages)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	expected := "Subject: Test\n\n[From: alice@example.com, Date: 2024-01-15T10:30:00Z]\nHello, this is a test message."
	if result != expected {
		t.Errorf("Normalize result:\n%s\nwant:\n%s", result, expected)
	}
}

func TestThreadNormalizer_Normalize_ChronologicalSort(t *testing.T) {
	tn := NewThreadNormalizer()
	thread := &Thread{ID: "thread-1", Subject: "Test"}

	// Messages in reverse chronological order
	messages := []Message{
		{
			ID:       "msg-2",
			ThreadID: "thread-1",
			From:     "bob@example.com",
			Date:     time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC),
			Body:     "Reply message",
		},
		{
			ID:       "msg-1",
			ThreadID: "thread-1",
			From:     "alice@example.com",
			Date:     time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			Body:     "First message",
		},
	}

	result, err := tn.Normalize(thread, messages)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	// Should be sorted chronologically (alice first, then bob)
	aliceIdx := strings.Index(result, "alice@example.com")
	bobIdx := strings.Index(result, "bob@example.com")

	if aliceIdx > bobIdx {
		t.Error("Messages not sorted chronologically: alice should come before bob")
	}
}

func TestThreadNormalizer_Normalize_MultipleMessages(t *testing.T) {
	tn := NewThreadNormalizer()
	thread := &Thread{ID: "thread-1", Subject: "Test"}

	messages := []Message{
		{
			ID:   "msg-1",
			From: "alice@example.com",
			Date: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			Body: "First message",
		},
		{
			ID:   "msg-2",
			From: "bob@example.com",
			Date: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC),
			Body: "Second message",
		},
	}

	result, err := tn.Normalize(thread, messages)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	// Check separator between messages (double newline)
	if !strings.Contains(result, "First message\n\n[From: bob") {
		t.Error("Messages should be separated by double newline")
	}
}

func TestThreadNormalizer_NormalizeMessage_HTMLOnly(t *testing.T) {
	tn := NewThreadNormalizer()
	tn.IncludeMetadata = false

	msg := &Message{
		ID:       "msg-1",
		From:     "alice@example.com",
		Date:     time.Now(),
		HTMLBody: "<html><body><p>Hello</p><p>World</p></body></html>",
	}

	result := tn.NormalizeMessage(msg)

	if !strings.Contains(result, "Hello") || !strings.Contains(result, "World") {
		t.Errorf("HTML not properly extracted: %s", result)
	}

	if strings.Contains(result, "<") || strings.Contains(result, ">") {
		t.Errorf("HTML tags not removed: %s", result)
	}
}

func TestThreadNormalizer_NormalizeMessage_PreferPlainText(t *testing.T) {
	tn := NewThreadNormalizer()
	tn.IncludeMetadata = false

	msg := &Message{
		ID:       "msg-1",
		From:     "alice@example.com",
		Date:     time.Now(),
		Body:     "Plain text content",
		HTMLBody: "<html><body>HTML content</body></html>",
	}

	result := tn.NormalizeMessage(msg)

	if result != "Plain text content" {
		t.Errorf("Should prefer plain text: got %s", result)
	}
}

func TestThreadNormalizer_NormalizeMessage_StripSignatures(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		want  string
		strip bool
	}{
		{
			name:  "standard signature delimiter",
			body:  "Main content\n\n-- \nJohn Doe\nCEO",
			want:  "Main content",
			strip: true,
		},
		{
			name:  "signature with dashes only",
			body:  "Main content\n\n--\nJohn Doe",
			want:  "Main content",
			strip: true,
		},
		{
			name:  "underscore signature",
			body:  "Main content\n\n__________\nSent from iPhone",
			want:  "Main content",
			strip: true,
		},
		{
			name:  "preserve signature when disabled",
			body:  "Main content\n\n--\nJohn Doe",
			want:  "Main content\n\n--\nJohn Doe",
			strip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tn := NewThreadNormalizer()
			tn.IncludeMetadata = false
			tn.StripSignatures = tt.strip
			tn.StripQuotes = false

			msg := &Message{
				ID:   "msg-1",
				From: "test@example.com",
				Date: time.Now(),
				Body: tt.body,
			}

			result := tn.NormalizeMessage(msg)
			if result != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", result, tt.want)
			}
		})
	}
}

func TestThreadNormalizer_NormalizeMessage_StripQuotes(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		want  string
		strip bool
	}{
		{
			name:  "quoted lines with >",
			body:  "My reply\n\n> Previous message\n> More quote",
			want:  "My reply",
			strip: true,
		},
		{
			name:  "On ... wrote pattern",
			body:  "My reply\n\nOn Mon, Jan 15, 2024 at 10:30 AM Alice <alice@example.com> wrote:\nQuoted text",
			want:  "My reply",
			strip: true,
		},
		{
			name:  "French Le ... a ecrit pattern",
			body:  "Ma reponse\n\nLe 15 janvier 2024 a 10:30, Alice <alice@example.com> a ecrit :\nTexte cite",
			want:  "Ma reponse",
			strip: true,
		},
		{
			name:  "preserve quotes when disabled",
			body:  "My reply\n\n> Previous message",
			want:  "My reply\n\n> Previous message",
			strip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tn := NewThreadNormalizer()
			tn.IncludeMetadata = false
			tn.StripSignatures = false
			tn.StripQuotes = tt.strip

			msg := &Message{
				ID:   "msg-1",
				From: "test@example.com",
				Date: time.Now(),
				Body: tt.body,
			}

			result := tn.NormalizeMessage(msg)
			if result != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", result, tt.want)
			}
		})
	}
}

func TestThreadNormalizer_NormalizeMessage_RemoveAutoTags(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "EXT tag",
			body: "[EXT] Important message",
			want: "Important message",
		},
		{
			name: "EXTERNAL tag",
			body: "[EXTERNAL] Important message",
			want: "Important message",
		},
		{
			name: "EXTERNE tag (French)",
			body: "[EXTERNE] Message important",
			want: "Message important",
		},
		{
			name: "lowercase ext",
			body: "[ext] message",
			want: "message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tn := NewThreadNormalizer()
			tn.IncludeMetadata = false
			tn.StripSignatures = false
			tn.StripQuotes = false

			msg := &Message{
				ID:   "msg-1",
				From: "test@example.com",
				Date: time.Now(),
				Body: tt.body,
			}

			result := tn.NormalizeMessage(msg)
			if result != tt.want {
				t.Errorf("got: %s, want: %s", result, tt.want)
			}
		})
	}
}

func TestThreadNormalizer_NormalizeMessage_MaxBodyLength(t *testing.T) {
	tn := NewThreadNormalizer()
	tn.MaxBodyLength = 20
	tn.IncludeMetadata = false

	msg := &Message{
		ID:   "msg-1",
		From: "test@example.com",
		Date: time.Now(),
		Body: "This is a very long message that exceeds the limit",
	}

	result := tn.NormalizeMessage(msg)

	if len(result) > 20 {
		t.Errorf("Body length %d exceeds MaxBodyLength 20", len(result))
	}
}

func TestThreadNormalizer_NormalizeMessage_NormalizeWhitespace(t *testing.T) {
	tn := NewThreadNormalizer()
	tn.IncludeMetadata = false
	tn.StripSignatures = false
	tn.StripQuotes = false

	msg := &Message{
		ID:   "msg-1",
		From: "test@example.com",
		Date: time.Now(),
		Body: "First paragraph\n\n\n\n\nSecond paragraph",
	}

	result := tn.NormalizeMessage(msg)

	// Should have at most 2 consecutive newlines
	if strings.Contains(result, "\n\n\n") {
		t.Errorf("Multiple newlines not reduced: %q", result)
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "simple paragraph",
			html: "<p>Hello World</p>",
			want: "Hello World",
		},
		{
			name: "multiple paragraphs",
			html: "<p>First</p><p>Second</p>",
			want: "First\nSecond",
		},
		{
			name: "div elements",
			html: "<div>First</div><div>Second</div>",
			want: "First\nSecond",
		},
		{
			name: "script removed",
			html: "<p>Text</p><script>alert('xss')</script><p>More</p>",
			want: "Text\nMore",
		},
		{
			name: "style removed",
			html: "<style>.foo{color:red}</style><p>Text</p>",
			want: "Text",
		},
		{
			name: "br as newline",
			html: "Line 1<br>Line 2",
			want: "Line 1\nLine 2",
		},
		{
			name: "nested elements",
			html: "<div><p><strong>Bold</strong> text</p></div>",
			want: "Bold text",
		},
		{
			name: "whitespace handling",
			html: "<p>  Hello   World  </p>",
			want: "Hello   World",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripHTML(tt.html)
			// Normalize for comparison
			result = strings.TrimSpace(result)
			want := strings.TrimSpace(tt.want)

			if result != want {
				t.Errorf("stripHTML(%q):\ngot:  %q\nwant: %q", tt.html, result, want)
			}
		})
	}
}

func TestRemoveSignatures(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "standard delimiter with space",
			body: "Content\n\n-- \nSignature",
			want: "Content\n",
		},
		{
			name: "delimiter without space",
			body: "Content\n\n--\nSignature",
			want: "Content\n",
		},
		{
			name: "underscore delimiter",
			body: "Content\n\n__________\nSent from iPhone",
			want: "Content\n",
		},
		{
			name: "no signature",
			body: "Just content",
			want: "Just content",
		},
		{
			name: "dash in content",
			body: "Content with -- dashes",
			want: "Content with -- dashes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeSignatures(tt.body)
			if result != tt.want {
				t.Errorf("removeSignatures:\ngot:  %q\nwant: %q", result, tt.want)
			}
		})
	}
}

func TestRemoveQuotedReplies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "quoted lines",
			body: "Reply\n\n> Quoted\n> More",
			want: "Reply\n",
		},
		{
			name: "On wrote pattern",
			body: "Reply\n\nOn Jan 15, 2024, at 10:30 AM, Alice wrote:\nQuoted",
			want: "Reply\n",
		},
		{
			name: "Le ecrit pattern",
			body: "Reponse\n\nLe 15 janvier 2024 a 10:30, Alice a ecrit :\nCite",
			want: "Reponse\n",
		},
		{
			name: "no quotes",
			body: "Just a message",
			want: "Just a message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeQuotedReplies(tt.body)
			if result != tt.want {
				t.Errorf("removeQuotedReplies:\ngot:  %q\nwant: %q", result, tt.want)
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "multiple newlines",
			body: "Para 1\n\n\n\n\nPara 2",
			want: "Para 1\n\nPara 2",
		},
		{
			name: "trailing spaces",
			body: "Line 1   \nLine 2  ",
			want: "Line 1\nLine 2",
		},
		{
			name: "leading/trailing whitespace",
			body: "  \n\nContent\n\n  ",
			want: "Content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeWhitespace(tt.body)
			if result != tt.want {
				t.Errorf("normalizeWhitespace:\ngot:  %q\nwant: %q", result, tt.want)
			}
		})
	}
}

// Benchmark for performance requirement: < 2s for 20 messages of 5000 chars each
func BenchmarkThreadNormalizer_Normalize(b *testing.B) {
	tn := NewThreadNormalizer()
	thread := &Thread{ID: "thread-1", Subject: "Benchmark Thread"}

	// Generate 20 messages with 5000 chars each
	messages := make([]Message, 20)
	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 20; i++ {
		// Create a 5000 char body with various patterns
		body := strings.Builder{}
		body.WriteString("Message content with various text.\n\n")

		// Add some quoted text
		body.WriteString("> This is quoted text from previous message\n")
		body.WriteString("> More quoted content here\n\n")

		// Fill to approximately 5000 chars
		filler := "Lorem ipsum dolor sit amet, consectetur adipiscing elit. "
		for body.Len() < 4900 {
			body.WriteString(filler)
		}

		// Add signature
		body.WriteString("\n\n-- \nJohn Doe\nSenior Engineer")

		messages[i] = Message{
			ID:       "msg-" + string(rune('A'+i)),
			ThreadID: "thread-1",
			From:     "user@example.com",
			Date:     baseDate.Add(time.Duration(i) * time.Hour),
			Body:     body.String(),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tn.Normalize(thread, messages)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkThreadNormalizer_HTMLStripping(b *testing.B) {
	tn := NewThreadNormalizer()
	tn.IncludeMetadata = false

	// Create an HTML body
	html := `<html>
	<head><style>.foo{color:red}</style></head>
	<body>
		<div class="header">
			<h1>Important Email</h1>
		</div>
		<div class="content">
			<p>Dear User,</p>
			<p>This is a <strong>very important</strong> message with <em>various</em> HTML elements.</p>
			<ul>
				<li>Point 1</li>
				<li>Point 2</li>
				<li>Point 3</li>
			</ul>
			<p>Please review at your earliest convenience.</p>
		</div>
		<div class="footer">
			<p>Best regards,<br>The Team</p>
		</div>
		<script>console.log('tracking');</script>
	</body>
	</html>`

	msg := &Message{
		ID:       "msg-1",
		From:     "test@example.com",
		Date:     time.Now(),
		HTMLBody: html,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tn.NormalizeMessage(msg)
	}
}

func BenchmarkStripHTML(b *testing.B) {
	html := `<html><body><p>Hello</p><div>World</div><script>alert('x')</script></body></html>`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stripHTML(html)
	}
}

// Test that the complete workflow produces expected output format
func TestThreadNormalizer_OutputFormat(t *testing.T) {
	tn := NewThreadNormalizer()
	thread := &Thread{ID: "thread-1", Subject: "Test Thread"}

	messages := []Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-1",
			From:     "alice@example.com",
			Date:     time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			Body:     "Contenu du premier message...",
		},
		{
			ID:       "msg-2",
			ThreadID: "thread-1",
			From:     "bob@example.com",
			Date:     time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC),
			Body:     "Contenu de la reponse...",
		},
	}

	result, err := tn.Normalize(thread, messages)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	expected := `Subject: Test Thread

[From: alice@example.com, Date: 2024-01-15T10:30:00Z]
Contenu du premier message...

[From: bob@example.com, Date: 2024-01-15T11:00:00Z]
Contenu de la reponse...`

	if result != expected {
		t.Errorf("Output format mismatch:\ngot:\n%s\n\nwant:\n%s", result, expected)
	}
}

// Integration test with complex email
func TestThreadNormalizer_ComplexEmail(t *testing.T) {
	tn := NewThreadNormalizer()

	msg := &Message{
		ID:   "msg-1",
		From: "sender@example.com",
		Date: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Body: `[EXTERNAL] Important meeting update

Hi team,

The meeting has been rescheduled to 3pm.

On Mon, Jan 14, 2024 at 2:00 PM John <john@example.com> wrote:
> The original meeting is at 2pm.
> Please confirm your attendance.

--
Best regards,
Jane Doe
Project Manager`,
	}

	result := tn.NormalizeMessage(msg)

	// Should remove [EXTERNAL] tag
	if strings.Contains(result, "[EXTERNAL]") {
		t.Error("Should remove [EXTERNAL] tag")
	}

	// Should remove quoted text
	if strings.Contains(result, "original meeting") {
		t.Error("Should remove quoted text")
	}

	// Should remove signature
	if strings.Contains(result, "Project Manager") {
		t.Error("Should remove signature")
	}

	// Should keep main content
	if !strings.Contains(result, "meeting has been rescheduled") {
		t.Error("Should keep main content")
	}
}
