package sesevents

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SuppressionAPI is the slice of the SESv2 client the suppression list needs;
// tests fake it.
type SuppressionAPI interface {
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
// https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteSuppressedDestination.html
func (s *SuppressionList) Remove(ctx context.Context, address string) (removed bool, err error) {
	_, err = s.api.DeleteSuppressedDestination(ctx, &sesv2.DeleteSuppressedDestinationInput{
		EmailAddress: aws.String(address),
	})
	if err == nil {
		return true, nil
	}
	var nf *types.NotFoundException
	if errors.As(err, &nf) {
		return false, nil
	}
	return false, err
}
