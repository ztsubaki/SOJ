package serverconfig

import (
	"reflect"
	"testing"
)

func TestEmailDomains(t *testing.T) {
	t.Setenv("REGISTRATION_EMAIL_DOMAINS", " Example.EDU,students.example.edu,example.edu ")
	domains, err := EmailDomains("REGISTRATION_EMAIL_DOMAINS")
	if err != nil {
		t.Fatalf("EmailDomains: %v", err)
	}
	want := []string{"example.edu", "students.example.edu"}
	if !reflect.DeepEqual(domains, want) {
		t.Fatalf("EmailDomains = %v, want %v", domains, want)
	}
}

func TestEmailDomainsRejectsInvalidDomain(t *testing.T) {
	t.Setenv("REGISTRATION_EMAIL_DOMAINS", "example.edu,@invalid")
	if _, err := EmailDomains("REGISTRATION_EMAIL_DOMAINS"); err == nil {
		t.Fatal("EmailDomains accepted an invalid domain")
	}
}
