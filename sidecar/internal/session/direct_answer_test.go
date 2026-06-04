package session

import (
	"strings"
	"testing"
)

func TestCanAnswerFromContext_KnownIBAN(t *testing.T) {
	ctx := &SessionContext{}
	ctx.AppendResolvedQuery(ResolvedQuery{
		Question: "what is the VAT I should send to?",
		Answer:   "IBAN BE68 5390 0754 7034 for TVA EXMPL Q1 2026",
	}, "TVA Q1 2026")
	ctx.AddEntity(Entity{Type: EntityIBAN, Value: "BE68539007547034", Source: "email:tva-1"})

	ans, ok := CanAnswerFromContext("can you show me the IBAN?", ctx)
	if !ok {
		t.Fatal("expected direct answer to fire")
	}
	if !strings.Contains(ans.Text, "BE68539007547034") {
		t.Errorf("expected IBAN in answer, got %q", ans.Text)
	}
	if ans.EntityType != EntityIBAN {
		t.Errorf("EntityType = %q, want %q", ans.EntityType, EntityIBAN)
	}
	if len(ans.SourceIDs) != 1 || ans.SourceIDs[0] != "email:tva-1" {
		t.Errorf("SourceIDs = %v, want [email:tva-1]", ans.SourceIDs)
	}
}

func TestCanAnswerFromContext_NoMatchingEntity(t *testing.T) {
	ctx := &SessionContext{}
	ctx.AppendResolvedQuery(ResolvedQuery{Question: "what is the VAT?", Answer: "..."}, "TVA")
	ctx.AddEntity(Entity{Type: EntityAmount, Value: "7421.85 EUR"})

	if _, ok := CanAnswerFromContext("can you show me the IBAN?", ctx); ok {
		t.Error("expected no direct answer when entity type missing from context")
	}
}

func TestCanAnswerFromContext_NoEntityTypeInQuery(t *testing.T) {
	ctx := &SessionContext{}
	ctx.AppendResolvedQuery(ResolvedQuery{Question: "what is the VAT?", Answer: "..."}, "TVA")
	ctx.AddEntity(Entity{Type: EntityIBAN, Value: "BE22..."})

	if _, ok := CanAnswerFromContext("how does TVA work?", ctx); ok {
		t.Error("expected no direct answer when query doesn't target an entity type")
	}
}

func TestCanAnswerFromContext_TopicShift_FallsThrough(t *testing.T) {
	ctx := &SessionContext{}
	ctx.AppendResolvedQuery(ResolvedQuery{
		Question: "what is the VAT I should send to?",
		Answer:   "IBAN BE68 5390 0754 7034 for TVA Q1 2026",
	}, "TVA Q1 2026")
	ctx.AddEntity(Entity{Type: EntityIBAN, Value: "BE68539007547034"})

	// Query introduces "Stripe" — a capitalized proper noun absent from prior
	// turns. Lowercase common nouns must NOT trigger this guard (otherwise
	// "show me the iban for compte X" would always fall through).
	if _, ok := CanAnswerFromContext("show me the IBAN for Stripe", ctx); ok {
		t.Error("expected fall-through on topic shift (new proper noun: Stripe)")
	}
}

func TestCanAnswerFromContext_FrenchEntityFollowUp_FiresEvenWithCommonNouns(t *testing.T) {
	// Regression for the demo Q2: lowercase common nouns like "compte" or
	// "communication" must not block the direct-answer path.
	ctx := &SessionContext{}
	ctx.AppendResolvedQuery(ResolvedQuery{
		Question: "Donne moi le montant de la dernière TVA que je dois payer",
		Answer:   "Le montant est 7421.85 EUR pour la TVA EXMPL Q1 2026",
	}, "TVA EXMPL")
	ctx.AddEntity(Entity{Type: EntityStructuredCom, Value: "+++090/9337/55493+++", Source: "email:tva-payment"})

	ans, ok := CanAnswerFromContext("Sur quel compte et quelle communication je dois ajouter pour le virement ?", ctx)
	if !ok {
		t.Fatal("expected direct answer to fire for French follow-up using common nouns")
	}
	if !strings.Contains(ans.Text, "+++090/9337/55493+++") {
		t.Errorf("expected structured comm in answer, got %q", ans.Text)
	}
}

func TestCanAnswerFromContext_LongQuery_FallsThrough(t *testing.T) {
	ctx := &SessionContext{}
	ctx.AppendResolvedQuery(ResolvedQuery{Question: "VAT?", Answer: "IBAN BE22..."}, "TVA")
	ctx.AddEntity(Entity{Type: EntityIBAN, Value: "BE22..."})

	long := "can you show me the IBAN and also tell me who the recipient is and when this is due"
	if _, ok := CanAnswerFromContext(long, ctx); ok {
		t.Error("expected fall-through on long query (>12 words)")
	}
}

func TestCanAnswerFromContext_NilContext(t *testing.T) {
	if _, ok := CanAnswerFromContext("show me the IBAN", nil); ok {
		t.Error("expected false for nil context")
	}
}

func TestDetectQueryEntityType(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"show me the IBAN", EntityIBAN},
		{"numéro de compte ?", EntityIBAN},
		{"what is the amount?", EntityAmount},
		{"combien je dois", EntityAmount},
		{"the communication reference", EntityStructuredCom},
		{"who sent the email?", ""},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			if got := DetectQueryEntityType(tc.query); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractEntities_TVAEmail(t *testing.T) {
	text := `Montant : 7 421,85 €
IBAN : BE68 5390 0754 7034
Communication : +++090/9337/55493+++`
	ents := ExtractEntities(text, "email:abc")

	gotTypes := map[string]bool{}
	for _, e := range ents {
		gotTypes[e.Type] = true
		if e.Source != "email:abc" {
			t.Errorf("Source = %q, want email:abc", e.Source)
		}
	}
	for _, want := range []string{EntityIBAN, EntityAmount, EntityStructuredCom} {
		if !gotTypes[want] {
			t.Errorf("missing entity type %q in %v", want, ents)
		}
	}
}

func TestIntroducesTopicShiftNoun_NoPriorQueries(t *testing.T) {
	ctx := &SessionContext{}
	if !introducesTopicShiftNoun("show me the iban", ctx) {
		t.Error("expected topic-shift true when no prior queries (no anchor)")
	}
}
