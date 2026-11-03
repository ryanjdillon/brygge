package main

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
)

type nopSendLog struct{}

func (nopSendLog) Start(context.Context, email.LoggedSend) error                { return nil }
func (nopSendLog) Finish(context.Context, string, string, string, string) error { return nil }

func TestNewEmailSender(t *testing.T) {
	stalwart := config.Config{SMTPHost: "mail.example.org", SMTPPort: 465, EmailFrom: "relay@example.org"}
	ses := stalwart
	ses.EmailProvider = "ses"
	ses.SESRegion = "eu-north-1"
	ses.SESConfigurationSet = "brygge-test"
	ses.AWSAccessKeyID = "AKIAEXAMPLE"
	ses.AWSSecretAccessKey = "example-secret"

	for _, tc := range []struct {
		name      string
		mutate    func(*config.Config)
		base      config.Config
		wantNil   bool
		wantError string
	}{
		{name: "smtp", base: stalwart},
		{name: "smtp unconfigured", base: config.Config{}, wantNil: true},
		{name: "ses", base: ses},
		{name: "ses without fallback relay", base: ses, mutate: func(c *config.Config) { c.SMTPHost = "" }},
		{name: "ses without credentials", base: ses, mutate: func(c *config.Config) {
			c.AWSSecretAccessKey, c.SESConfigurationSet = "", ""
		}, wantError: "AWS_SECRET_ACCESS_KEY, SES_CONFIGURATION_SET unset"},
		{name: "unknown provider", base: stalwart, mutate: func(c *config.Config) { c.EmailProvider = "sendgrid" },
			wantError: "EMAIL_PROVIDER must be smtp or ses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.base
			if tc.mutate != nil {
				tc.mutate(&cfg)
			}
			s, err := newEmailSender(&cfg, nopSendLog{}, zerolog.Nop())
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("err = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (s == nil) != tc.wantNil {
				t.Errorf("sender = %v, want nil %v", s, tc.wantNil)
			}
		})
	}
}
