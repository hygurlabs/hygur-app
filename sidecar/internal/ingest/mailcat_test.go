package ingest

import (
	"reflect"
	"testing"
)

func TestMatchCategories(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"exact comma list", "Invoicing, Banking & Finance", []string{"Invoicing", "Banking & Finance"}},
		{"case-insensitive", "invoicing", []string{"Invoicing"}},
		{"first word resolves multiword", "Banking", []string{"Banking & Finance"}},
		{"prose wrapper", "Categories: Tax & VAT and Accounting.", []string{"Tax & VAT", "Accounting"}},
		{"unknown dropped", "Spaceships, Invoicing", []string{"Invoicing"}},
		{"nothing", "no idea", nil},
		{"taxonomy order preserved", "Subscriptions, Telecom", []string{"Telecom", "Subscriptions"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchCategories(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("matchCategories(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// "achats" must not match inside "rachats" — whole-word boundary check.
func TestContainsWordBoundary(t *testing.T) {
	if containsWord("les rachats anticipés", "achats") {
		t.Error("containsWord matched inside a larger word")
	}
	if !containsWord("nos achats du mois", "achats") {
		t.Error("containsWord missed a whole-word match")
	}
}

func TestCategoriesFromMetadata(t *testing.T) {
	if got := categoriesFromMetadata(map[string]any{"mail_categories": []any{"Invoicing", ""}}); !reflect.DeepEqual(got, []string{"Invoicing"}) {
		t.Errorf("[]any case = %v", got)
	}
	if got := categoriesFromMetadata(map[string]any{"mail_categories": []string{"Health"}}); !reflect.DeepEqual(got, []string{"Health"}) {
		t.Errorf("[]string case = %v", got)
	}
	if got := categoriesFromMetadata(map[string]any{}); got != nil {
		t.Errorf("missing key = %v, want nil", got)
	}
}
