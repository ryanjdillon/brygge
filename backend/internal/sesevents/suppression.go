package sesevents

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// ErrComplaintSuppressed is returned by Remove for an address SES suppressed
// because its owner reported mail as spam. Such an address is never taken
// off the list from Brygge: sending to a complainer again is what gets an SES
// account paused.
var ErrComplaintSuppressed = errors.New("address is suppressed after a spam complaint")

// SuppressionAPI is the slice of the SESv2 client the suppression list needs;
// tests fake it.
type SuppressionAPI interface {
	GetSuppressedDestination(ctx context.Context, params *sesv2.GetSuppressedDestinationInput, optFns ...func(*sesv2.Options)) (*sesv2.GetSuppressedDestinationOutput, error)
	DeleteSuppressedDestination(ctx context.Context, params *sesv2.DeleteSuppressedDestinationInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteSuppressedDestinationOutput, error)
}

// SuppressionList removes addresses from the account-level suppression list.
//
// SES adds an address to that list after a hard bounce or a complaint and
// refuses later sends to it (the Bounce event with subtype
// OnAccountSuppressionList). A hard bounce caused by our own fault suppresses
// an address that is fine, so an admin can take it off again:
// https://docs.aws.amazon.com/ses/latest/dg/sending-email-suppression-list.html
type SuppressionList struct {
	api SuppressionAPI
}

// NewSuppressionList wraps an SESv2 client (or a fake).
func NewSuppressionList(api SuppressionAPI) *SuppressionList {
	return &SuppressionList{api: api}
}

// Remove takes address off the account-level suppression list. removed is
// false when the address was not on it: SES answers NotFoundException, and
// the caller's goal (the address can receive mail) already holds.
//
// The list entry's reason is read first, because a send refused as
// OnAccountSuppressionList does not say why the address was listed: an
// address listed for a COMPLAINT (possibly about another mail) is refused
// with ErrComplaintSuppressed and stays on the list.
// https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_GetSuppressedDestination.html
// https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteSuppressedDestination.html
func (s *SuppressionList) Remove(ctx context.Context, address string) (removed bool, err error) {
	got, err := s.api.GetSuppressedDestination(ctx, &sesv2.GetSuppressedDestinationInput{
		EmailAddress: aws.String(address),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if got != nil && got.SuppressedDestination != nil &&
		got.SuppressedDestination.Reason == types.SuppressionListReasonComplaint {
		return false, ErrComplaintSuppressed
	}
	_, err = s.api.DeleteSuppressedDestination(ctx, &sesv2.DeleteSuppressedDestinationInput{
		EmailAddress: aws.String(address),
	})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}

func isNotFound(err error) bool {
	var nf *types.NotFoundException
	return errors.As(err, &nf)
}
