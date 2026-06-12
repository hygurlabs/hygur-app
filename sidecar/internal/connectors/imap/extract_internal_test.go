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

// TestStripHTMLTags_DropsStyleAndScript guards the regression where the CSS
// inside a <style> block (templated MJML/Mailjet mail) leaked into the indexed
// text as noise, and ensures real body text + UTF-8 survive.
func TestStripHTMLTags_DropsStyleAndScript(t *testing.T) {
	html := `<html><head><style type="text/css">#outlook a { padding:0; } body { margin:0;` +
		`-webkit-text-size-adjust:100%; } @media only screen and (min-width:480px) { ` +
		`.mj-column-per-100 { width:100% !important; } }</style></head>` +
		`<body><script>var x = 1 < 2;</script>` +
		`<p>Paiement réussi ✅</p><p>Votre facture n°20260602 d'un montant de 11.93 €.</p></body></html>`
	got := stripHTMLTags(html)
	for _, leak := range []string{"padding", "margin", "min-width", "width:100%", "var x", "!important"} {
		if strings.Contains(got, leak) {
			t.Errorf("CSS/JS leaked into output (%q): %q", leak, got)
		}
	}
	for _, want := range []string{"Paiement réussi ✅", "Votre facture", "11.93 €"} {
		if !strings.Contains(got, want) {
			t.Errorf("body text dropped (%q): %q", want, got)
		}
	}
	if !utf8.ValidString(got) {
		t.Errorf("output is not valid UTF-8: %q", got)
	}
}
