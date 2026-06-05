package imap

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestExtractPlainText_DecodesBase64Part guards the regression where a
// base64-encoded text part leaked raw base64 into the index instead of its
// decoded text (polluting search + bloating LLM context).
func TestExtractPlainText_DecodesBase64Part(t *testing.T) {
	body := "Voici le reçu de votre recharge. Montant TTC : 11,67 €. Bien à vous."
	enc := base64.StdEncoding.EncodeToString([]byte(body))

	// Wrap at 76 cols with CRLF, like a real MIME encoder.
	var wrapped strings.Builder
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		wrapped.WriteString(enc[i:end])
		wrapped.WriteString("\r\n")
	}

	raw := "Subject: Reçu\r\n" +
		"Content-Type: multipart/alternative; boundary=\"B\"\r\n\r\n" +
		"--B\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		wrapped.String() +
		"--B--\r\n"

	got := extractPlainText([]byte(raw))
	if !strings.Contains(got, "Montant TTC : 11,67") {
		t.Fatalf("expected decoded amount in extracted text, got %q", got)
	}
	// The raw base64 must NOT survive into the extracted text.
	if strings.Contains(got, enc[:24]) {
		t.Fatalf("raw base64 leaked into extracted text: %q", got)
	}
}

// TestDecodeTransferEncoding_Base64 covers the decoder directly, including the
// line-wrapping that the std base64 decoder otherwise rejects.
func TestDecodeTransferEncoding_Base64(t *testing.T) {
	want := "déclaration TVA trimestrielle"
	enc := base64.StdEncoding.EncodeToString([]byte(want))
	withBreaks := enc[:8] + "\r\n" + enc[8:]
	if got := decodeTransferEncoding("base64", []byte(withBreaks), "utf-8"); got != want {
		t.Fatalf("decodeTransferEncoding(base64) = %q, want %q", got, want)
	}
}

// TestExtractPlainText_DecodesLatin1Charset guards the regression where an
// ISO-8859-1 / Windows-1252 body (common in French/Belgian business mail)
// leaked raw 8-bit bytes — "é" as 0xE9 — that are invalid UTF-8 and surfaced as
// the replacement character "" in the UI.
func TestExtractPlainText_DecodesLatin1Charset(t *testing.T) {
	// "=E9" is quoted-printable for byte 0xE9 = "é" in ISO-8859-1.
	raw := "Subject: Test\r\n" +
		"Content-Type: text/plain; charset=iso-8859-1\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"Monsieur le Directeur g=E9n=E9ral,\r\n"

	got := extractPlainText([]byte(raw))
	if !utf8.ValidString(got) {
		t.Fatalf("extracted text is not valid UTF-8: %q", got)
	}
	if !strings.Contains(got, "Directeur général") {
		t.Fatalf("expected accented text decoded, got %q", got)
	}
}
