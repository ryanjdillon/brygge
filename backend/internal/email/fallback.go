package email

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
)

// WithMagicLinkFallback sends through primary and, for login links only,
// tries fallback when primary failed outright. A member who cannot get a
// login link cannot reach anything else, so that one kind gets a second
// relay; every other kind reports the failure to its caller as is.
//
// An ambiguous failure (ErrDeliveryAmbiguous) is never retried: primary may
// have the message, and a second copy through another relay is a duplicate,
// not a recovery. Neither is a send whose context has ended, which the
// fallback could not complete either. Returns primary when fallback is nil.
func WithMagicLinkFallback(primary, fallback Sender, logger zerolog.Logger) Sender {
	if primary == nil {
		return fallback
	}
	if fallback == nil {
		return primary
	}
	return &fallbackSender{primary: primary, fallback: fallback, logger: logger}
}

type fallbackSender struct {
	primary, fallback Sender
	logger            zerolog.Logger
}

var _ Sender = (*fallbackSender)(nil)

func (f *fallbackSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	return f.try(ctx, func(s Sender) error { return s.Send(ctx, to, subject, htmlBody) })
}

func (f *fallbackSender) SendWithAttachment(ctx context.Context, to, subject, htmlBody, filename string, attachment []byte) error {
	return f.try(ctx, func(s Sender) error {
		return s.SendWithAttachment(ctx, to, subject, htmlBody, filename, attachment)
	})
}

func (f *fallbackSender) SendWithHeaders(ctx context.Context, to, subject, htmlBody string, extraHeaders map[string]string) error {
	return f.try(ctx, func(s Sender) error { return s.SendWithHeaders(ctx, to, subject, htmlBody, extraHeaders) })
}

func (f *fallbackSender) try(ctx context.Context, send func(Sender) error) error {
	err := send(f.primary)
	if err == nil || kindFrom(ctx) != KindMagicLink ||
		errors.Is(err, ErrDeliveryAmbiguous) || ctx.Err() != nil {
		return err
	}
	f.logger.Warn().Err(err).Msg("magic link send failed on the primary relay; retrying on the fallback relay")
	if ferr := send(f.fallback); ferr != nil {
		return errors.Join(err, ferr)
	}
	return nil
}
