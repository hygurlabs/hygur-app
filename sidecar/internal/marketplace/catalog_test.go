package marketplace

import "testing"

func TestCalDAVCatalogEntry(t *testing.T) {
	c := FindByID("caldav")
	if c == nil {
		t.Fatal("caldav not present in built-in catalog")
	}
	if !c.MultiInstance {
		t.Error("caldav must be multi-instance (drives the + add-account UI)")
	}
	if !c.IsBuiltIn {
		t.Error("caldav must be marked built-in")
	}
	if c.TypeName != "caldav" {
		t.Errorf("TypeName = %q", c.TypeName)
	}
}
