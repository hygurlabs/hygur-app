package mail

import (
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// ErrEmptyThread is returned when attempting to normalize a thread with no messages.
var ErrEmptyThread = errors.New("cannot normalize empty thread")

// Default configuration values for ThreadNormalizer.
const (
	DefaultMaxBodyLength = 50000
)

// ThreadNormalizer converts email threads into clean text suitable for indexing.
type ThreadNormalizer struct {
	// MaxBodyLength limits the length of each message body (default 50000).
	// Bodies exceeding this limit are truncated.
	MaxBodyLength int

	// IncludeMetadata adds [From: X, Date: Y] headers to each message (default true).
	IncludeMetadata bool

	// StripSignatures removes email signatures (default true).
	StripSignatures bool

	// StripQuotes removes quoted reply text (default true).
	StripQuotes bool
}

// NewThreadNormalizer creates a ThreadNormalizer with default settings.
func NewThreadNormalizer() *ThreadNormalizer {
	return &ThreadNormalizer{
		MaxBodyLength:   DefaultMaxBodyLength,
		IncludeMetadata: true,
		StripSignatures: true,
		StripQuotes:     true,
	}
}

// Normalize converts a thread and its messages into clean text for indexing.
// Messages are sorted chronologically and concatenated with separators.
// Returns ErrEmptyThread if the messages slice is empty.
func (tn *ThreadNormalizer) Normalize(thread *Thread, messages []Message) (string, error) {
	if len(messages) == 0 {
		return "", ErrEmptyThread
	}

	// Sort messages by date (chronological order)
	sortedMessages := make([]Message, len(messages))
	copy(sortedMessages, messages)
	sort.Slice(sortedMessages, func(i, j int) bool {
		return sortedMessages[i].Date.Before(sortedMessages[j].Date)
	})

	var builder strings.Builder
	// Pre-allocate approximate size: metadata + body per message
	builder.Grow(len(messages) * 1000)

	// Prepend the subject so it is captured in every chunk embedding.
	// Without this, a search for "TVA" misses a mail titled "Déclaration TVA …"
	// because only the body text is chunked.
	if thread != nil && thread.Subject != "" {
		builder.WriteString("Subject: ")
		builder.WriteString(thread.Subject)
		builder.WriteString("\n\n")
	}

	for i, msg := range sortedMessages {
		if i > 0 {
			builder.WriteString("\n\n")
		}

		normalized := tn.NormalizeMessage(&msg)
		builder.WriteString(normalized)
	}

	return builder.String(), nil
}

// looksLikeMIME reports whether text is raw MIME content that should be
// discarded. Some Gmail messages have a text/plain part whose content is
// actually raw multipart MIME (forwarded mails, S/MIME envelopes, …).
func looksLikeMIME(text string) bool {
	return strings.HasPrefix(text, "--") ||
		strings.Contains(text, "Content-Type:") ||
		strings.Contains(text, "Content-Transfer-Encoding:")
}

// NormalizeMessage normalizes a single email message.
// It prefers plain text Body over HTMLBody, but falls back to stripped HTML
// when Body is empty, contains HTML tags, or looks like raw MIME content.
func (tn *ThreadNormalizer) NormalizeMessage(msg *Message) string {
	var body string

	plainBody := strings.TrimSpace(msg.Body)
	switch {
	case plainBody == "":
		// No plain-text body: strip HTML.
		body = stripHTML(msg.HTMLBody)
	case looksLikeMIME(plainBody):
		// Plain-text part is actually raw MIME — use the HTML body instead.
		body = stripHTML(msg.HTMLBody)
	case strings.Contains(plainBody, "<html") || strings.Contains(plainBody, "<body") || strings.Contains(plainBody, "<p>"):
		// Plain-text field contains HTML markup — strip it.
		body = stripHTML(plainBody)
	default:
		body = plainBody
	}

	// Decode quoted-printable encoding if present
	body = decodeQuotedPrintable(body)

	// Apply cleaning heuristics
	body = removeAutoTags(body)

	if tn.StripSignatures {
		body = removeSignatures(body)
	}

	if tn.StripQuotes {
		body = removeQuotedReplies(body)
	}

	// Normalize whitespace
	body = normalizeWhitespace(body)

	// Truncate if necessary
	if tn.MaxBodyLength > 0 && len(body) > tn.MaxBodyLength {
		body = body[:tn.MaxBodyLength]
	}

	// Build final output
	var builder strings.Builder

	if tn.IncludeMetadata {
		builder.WriteString(fmt.Sprintf("[From: %s, Date: %s]\n",
			msg.From,
			msg.Date.Format("2006-01-02T15:04:05Z07:00")))
	}

	builder.WriteString(body)

	return builder.String()
}

// stripHTML extracts text content from HTML, removing all tags.
func stripHTML(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// If parsing fails, do a simple regex-based strip
		return simpleStripHTML(htmlContent)
	}

	var builder strings.Builder
	var extractText func(*html.Node)

	extractText = func(n *html.Node) {
		// Skip script and style elements entirely
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "head":
				return
			case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
				// Add newline before block elements
				if builder.Len() > 0 {
					builder.WriteString("\n")
				}
			}
		}

		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
					builder.WriteString(" ")
				}
				builder.WriteString(text)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractText(c)
		}
	}

	extractText(doc)
	return builder.String()
}

// simpleStripHTML is a fallback HTML stripper using regex.
func simpleStripHTML(htmlContent string) string {
	// Remove script and style blocks
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	result := reScript.ReplaceAllString(htmlContent, "")
	result = reStyle.ReplaceAllString(result, "")

	// Remove all HTML tags
	reTag := regexp.MustCompile(`<[^>]*>`)
	result = reTag.ReplaceAllString(result, " ")

	// Decode common HTML entities
	result = decodeHTMLEntities(result)

	return result
}

// decodeHTMLEntities decodes common HTML entities.
func decodeHTMLEntities(s string) string {
	replacer := strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&#39;", "'",
		"&apos;", "'",
	)
	return replacer.Replace(s)
}

// Regex patterns for signature detection.
var (
	// Standard signature delimiter: "-- " at start of line
	reSignatureDash = regexp.MustCompile(`(?m)^--\s*$`)
	// Underscore line (10+ underscores)
	reSignatureUnderscore = regexp.MustCompile(`(?m)^_{10,}$`)
)

// removeSignatures removes email signatures from the body.
func removeSignatures(body string) string {
	// Find signature delimiters and remove everything after
	lines := strings.Split(body, "\n")
	var result []string

	for _, line := range lines {
		// Check for signature delimiters
		if reSignatureDash.MatchString(line) || reSignatureUnderscore.MatchString(line) {
			break
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// Regex patterns for quoted reply detection.
var (
	// Lines starting with >
	reQuotedLine = regexp.MustCompile(`(?m)^>.*$`)
	// "On ... wrote:" pattern (English)
	reOnWrote = regexp.MustCompile(`(?im)^On\s+.*\s+wrote:\s*$`)
	// "Le ... a ecrit :" pattern (French)
	reLeEcrit = regexp.MustCompile(`(?im)^Le\s+.*\s+a\s+[eé]crit\s*:\s*$`)
	// "From: ... Sent: ..." Outlook style
	reOutlookQuote = regexp.MustCompile(`(?im)^-+\s*Original\s+Message\s*-+\s*$`)
)

// removeQuotedReplies removes quoted text from the body.
func removeQuotedReplies(body string) string {
	lines := strings.Split(body, "\n")
	var result []string
	skipRemaining := false

	for _, line := range lines {
		if skipRemaining {
			continue
		}

		// Check for quote introduction patterns
		if reOnWrote.MatchString(line) || reLeEcrit.MatchString(line) || reOutlookQuote.MatchString(line) {
			skipRemaining = true
			continue
		}

		// Skip lines starting with >
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// Regex patterns for auto-tags.
var reAutoTags = regexp.MustCompile(`(?i)\[(EXT|EXTERNAL|EXTERNE)\]\s*`)

// removeAutoTags removes common auto-added tags like [EXT], [EXTERNAL].
func removeAutoTags(body string) string {
	return reAutoTags.ReplaceAllString(body, "")
}

// Regex for multiple newlines.
var reMultipleNewlines = regexp.MustCompile(`\n{3,}`)

// normalizeWhitespace reduces multiple consecutive newlines to at most 2.
func normalizeWhitespace(body string) string {
	// Trim trailing whitespace from each line
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	body = strings.Join(lines, "\n")

	// Reduce multiple newlines to 2
	body = reMultipleNewlines.ReplaceAllString(body, "\n\n")

	// Trim leading and trailing whitespace
	body = strings.TrimSpace(body)

	return body
}

// Regex to detect quoted-printable soft line breaks.
var reQPSoftBreak = regexp.MustCompile(`=\r?\n`)

// decodeQuotedPrintable decodes quoted-printable encoded content.
// Returns the original string if decoding fails.
func decodeQuotedPrintable(s string) string {
	// Quick check: if no '=' present, nothing to decode
	if !strings.Contains(s, "=") {
		return s
	}

	// First, remove soft line breaks (=\r\n or =\n)
	s = reQPSoftBreak.ReplaceAllString(s, "")

	// Try to decode as quoted-printable
	reader := quotedprintable.NewReader(strings.NewReader(s))
	decoded, err := io.ReadAll(reader)
	if err != nil {
		// If decoding fails, return original string
		return s
	}

	return string(decoded)
}
