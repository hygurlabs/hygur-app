package recognize

import (
	"fmt"
	"strconv"
	"testing"
)

// mkNISS builds a checksum-VALID Belgian national number from a 9-digit base (fictional).
func mkNISS(base9 string, post2000 bool) string {
	var b int64
	if post2000 {
		b, _ = strconv.ParseInt("2"+base9, 10, 64)
	} else {
		b, _ = strconv.ParseInt(base9, 10, 64)
	}
	return base9 + fmt.Sprintf("%02d", 97-(b%97))
}

// mkBCE builds a checksum-VALID Belgian enterprise number from an 8-digit base (starts 0/1).
func mkBCE(base8 string) string {
	b, _ := strconv.ParseInt(base8, 10, 64)
	return base8 + fmt.Sprintf("%02d", 97-(b%97))
}

func TestValidators(t *testing.T) {
	niss := mkNISS("850701123", false) // fictional, born <2000
	if _, ok := validNISS(niss); !ok {
		t.Errorf("valid NISS %q rejected", niss)
	}
	// Tamper the check digits → an 11-digit number that is NOT a valid national number
	// (this is the "birth-certificate act number never mistaken for a NN" guarantee).
	bad := niss[:9] + fmt.Sprintf("%02d", (mustInt(niss[9:])+1)%100)
	if _, ok := validNISS(bad); ok {
		t.Errorf("tampered NISS %q accepted — checksum not enforced", bad)
	}
	// Implausible date is rejected even if the checksum matched by luck.
	if _, ok := validNISS("859901123" + niss[9:]); ok {
		t.Error("NISS with month 99 accepted")
	}

	bce := mkBCE("01234567")
	if _, ok := validBCE(bce); !ok {
		t.Errorf("valid BCE %q rejected", bce)
	}
	if _, ok := validBCE("be" + bce); !ok {
		t.Errorf("valid VAT be%q rejected", bce)
	}
	if _, ok := validBCE(bce[:9] + fmt.Sprintf("%d", (mustInt(bce[9:10])+1)%10)); ok {
		t.Error("tampered BCE accepted")
	}

	// Standard ISO 13616 example IBAN (fictional, checksum-valid).
	if _, ok := validIBAN("gb82west12345698765432"); !ok {
		t.Error("valid example IBAN rejected")
	}
	if _, ok := validIBAN("gb82west12345698765433"); ok {
		t.Error("tampered IBAN accepted")
	}
}

func TestRecognize(t *testing.T) {
	niss := mkNISS("850701123", false)
	// Format it the way a real document does (dots, colon, dash), amid prose.
	formatted := niss[0:2] + "." + niss[2:4] + "." + niss[4:6] + ":" + niss[6:9] + "-" + niss[9:11]
	text := "Composition de ménage. Numéro national: " + formatted + " pour la personne. Fin."

	got := Recognize(text)
	found := false
	for _, r := range got {
		if r.Type == TypeNationalNumber && r.Value == niss {
			found = true
		}
	}
	if !found {
		t.Errorf("Recognize did not type the formatted NISS; got %+v", got)
	}

	// An 11-digit number with a bad checksum (an act-number stand-in) must NOT be typed.
	bad := niss[:9] + fmt.Sprintf("%02d", (mustInt(niss[9:])+1)%100)
	for _, r := range Recognize("Acte n° " + bad + " enregistré.") {
		if r.Type == TypeNationalNumber {
			t.Errorf("bad-checksum number %q wrongly typed as national number", bad)
		}
	}
}

func mustInt(s string) int { n, _ := strconv.Atoi(s); return n }
