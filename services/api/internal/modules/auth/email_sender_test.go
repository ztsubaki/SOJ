package auth

import (
	"strings"
	"testing"
)

func TestRegistrationEmailSenderUsesLoggerWithoutSMTPHost(t *testing.T) {
	sender, err := NewRegistrationEmailSender(SMTPConfig{})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	if _, ok := sender.(registrationEmailLogger); !ok {
		t.Fatalf("sender = %T, want registrationEmailLogger", sender)
	}
}

func TestRegistrationEmailSenderValidatesSMTPConfig(t *testing.T) {
	valid := SMTPConfig{
		Host: "smtp.example.edu", From: "mailer@example.edu", PlatformName: "Example OJ",
	}
	sender, err := NewRegistrationEmailSender(valid)
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	smtpSender, ok := sender.(*smtpRegistrationEmailSender)
	if !ok {
		t.Fatalf("sender = %T, want SMTP sender", sender)
	}
	if smtpSender.port != "587" || smtpSender.tlsMode != "starttls" {
		t.Fatalf("SMTP defaults = (%q, %q), want (587, starttls)", smtpSender.port, smtpSender.tlsMode)
	}

	for name, config := range map[string]SMTPConfig{
		"missing from":        {Host: "smtp.example.edu"},
		"invalid port":        {Host: "smtp.example.edu", From: "mailer@example.edu", Port: "0"},
		"partial credentials": {Host: "smtp.example.edu", From: "mailer@example.edu", Username: "mailer"},
		"unencrypted":         {Host: "smtp.example.edu", From: "mailer@example.edu", TLSMode: "none"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRegistrationEmailSender(config); err == nil {
				t.Fatal("NewRegistrationEmailSender succeeded with invalid SMTP config")
			}
		})
	}
}

func TestSMTPRegistrationEmailMessage(t *testing.T) {
	sender := &smtpRegistrationEmailSender{
		from: "mailer@example.edu", platformName: "Example OJ",
	}
	message := sender.message("runner@example.edu", "123456")
	for _, expected := range []string{
		"From: mailer@example.edu",
		"To: runner@example.edu",
		"Content-Type: text/plain; charset=UTF-8",
		"123456",
		"Example OJ",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("message does not contain %q", expected)
		}
	}
	if !strings.Contains(message, "Subject: =?UTF-8?B?") {
		t.Error("message subject is not MIME encoded")
	}
}
