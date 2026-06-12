package ingest

import (
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func TestIsAutomatedSender(t *testing.T) {
	automated := []string{
		"no-reply@wufoo.com",
		"noreply@github.com",
		"notifications-noreply@linkedin.com",
		"LinkedIn <notifications-noreply@linkedin.com>",
		"nepasrepondre@ldlc.pro",
		"pasdereponse@bookmyname.com",
		"mailer-daemon@x.com",
		"no_reply@accounts.google.com",
	}
	for _, a := range automated {
		if !isAutomatedSender(a) {
			t.Errorf("isAutomatedSender(%q) = false, want true", a)
		}
	}
	human := []string{
		"rhys.shekleton@arcussearch.com",
		"antony@fiducense.be",
		"dle@0x0800.com",
		"Alice Durand <alice_durand@example.com>",
		"order-update@amazon.com.be", // a notification, but not a no-reply pattern
		"",
	}
	for _, h := range human {
		if isAutomatedSender(h) {
			t.Errorf("isAutomatedSender(%q) = true, want false", h)
		}
	}
}

func TestClaimsEligible(t *testing.T) {
	mail := func(from string) *store.KnowledgeItem {
		return &store.KnowledgeItem{SourceType: store.SourceTypeMail, Metadata: map[string]any{"mail_from": from}}
	}

	if !claimsEligible(mail("rhys@arcussearch.com"), []string{"HR & Payroll"}) {
		t.Error("human mail should be eligible")
	}
	if !claimsEligible(&store.KnowledgeItem{SourceType: store.SourceTypeNote, Metadata: map[string]any{}}, nil) {
		t.Error("note should be eligible")
	}
	if claimsEligible(mail("noreply@github.com"), nil) {
		t.Error("automated sender must be skipped")
	}
	if claimsEligible(mail("human@x.com"), []string{"Invoicing", "Notifications & Accounts"}) {
		t.Error("Notifications & Accounts category must be skipped")
	}
	if claimsEligible(&store.KnowledgeItem{SourceType: "document", Metadata: map[string]any{}}, nil) {
		t.Error("non-mail/non-note source must be ineligible")
	}
	if claimsEligible(nil, nil) {
		t.Error("nil item must be ineligible")
	}
}
