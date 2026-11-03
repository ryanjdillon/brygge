package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
)

// newEmailSender builds the app's mail transport from EMAIL_PROVIDER. Every
// relay is wrapped so each send is logged in email_sends (before the relay
// is contacted) and counted by provider. With "ses", the SMTP_* relay
// (Stalwart) stays as the fallback for login links only.
//
// Returns a nil Sender, not an error, when mail is simply not configured:
// handlers treat nil as "email disabled". An EMAIL_PROVIDER=ses that cannot
// be honoured is an error, because silently sending through Stalwart instead
// would hide a broken cutover.
func newEmailSender(cfg *config.Config, sendLog email.SendLog, log zerolog.Logger) (email.Sender, error) {
	wrap := func(s *email.SMTPClient, p email.Provider) email.Sender {
		// A nil *SMTPClient must not reach the Sender interface: a typed nil
		// compares non-nil, so the handlers' "email not configured" checks
		// would pass and the first send would panic.
		if s == nil {
			return nil
		}
		return email.Instrument(email.Record(s, p, sendLog, log), p)
	}

	stalwart := wrap(email.NewSMTPClient(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPPassword,
		cfg.EmailFrom, cfg.EmailReplyTo), email.ProviderStalwart)

	switch strings.ToLower(strings.TrimSpace(cfg.EmailProvider)) {
	case "", "smtp":
		switch {
		case stalwart != nil:
			log.Info().Str("host", cfg.SMTPHost).Int("port", cfg.SMTPPort).Str("from", cfg.EmailFrom).
				Str("reply_to", cfg.EmailReplyTo).Msg("email delivery enabled (SMTP)")
		case cfg.SMTPHost == "":
			log.Warn().Msg("email delivery disabled (SMTP_HOST unset)")
		default:
			log.Warn().Str("host", cfg.SMTPHost).Msg("email delivery disabled (EMAIL_FROM unset)")
		}
		return stalwart, nil

	case "ses":
		var missing []string
		for name, v := range map[string]string{
			"SES_REGION":            cfg.SESRegion,
			"SES_CONFIGURATION_SET": cfg.SESConfigurationSet,
			"AWS_ACCESS_KEY_ID":     cfg.AWSAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": cfg.AWSSecretAccessKey,
			"EMAIL_FROM":            cfg.EmailFrom,
		} {
			if v == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			return nil, fmt.Errorf("EMAIL_PROVIDER=ses but %s unset", strings.Join(missing, ", "))
		}
		host := email.SESSMTPHost(cfg.SESRegion)
		ses := wrap(email.NewSMTPClient(host, email.SESSMTPPort, cfg.AWSAccessKeyID,
			email.SESSMTPPassword(cfg.AWSSecretAccessKey, cfg.SESRegion),
			cfg.EmailFrom, cfg.EmailReplyTo, email.WithSESConfigurationSet(cfg.SESConfigurationSet)),
			email.ProviderSES)
		log.Info().Str("host", host).Str("configuration_set", cfg.SESConfigurationSet).
			Str("from", cfg.EmailFrom).Str("reply_to", cfg.EmailReplyTo).
			Bool("magic_link_fallback", stalwart != nil).Msg("email delivery enabled (Amazon SES)")
		if stalwart == nil {
			log.Warn().Msg("no SMTP_HOST relay: magic links have no fallback if SES refuses them")
		}
		return email.WithMagicLinkFallback(ses, stalwart, log), nil

	default:
		return nil, errors.New("EMAIL_PROVIDER must be smtp or ses, got " + cfg.EmailProvider)
	}
}
