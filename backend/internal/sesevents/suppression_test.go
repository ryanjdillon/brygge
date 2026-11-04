package sesevents

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type fakeSuppressionAPI struct {
	reason    types.SuppressionListReason
	getErr    error
	deleteErr error
	read      string
	deleted   string
}

func (f *fakeSuppressionAPI) GetSuppressedDestination(_ context.Context, in *sesv2.GetSuppressedDestinationInput, _ ...func(*sesv2.Options)) (*sesv2.GetSuppressedDestinationOutput, error) {
	f.read = *in.EmailAddress
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &sesv2.GetSuppressedDestinationOutput{
		SuppressedDestination: &types.SuppressedDestination{EmailAddress: in.EmailAddress, Reason: f.reason},
	}, nil
}

func (f *fakeSuppressionAPI) DeleteSuppressedDestination(_ context.Context, in *sesv2.DeleteSuppressedDestinationInput, _ ...func(*sesv2.Options)) (*sesv2.DeleteSuppressedDestinationOutput, error) {
	f.deleted = *in.EmailAddress
	return &sesv2.DeleteSuppressedDestinationOutput{}, f.deleteErr
}

func TestSuppressionListRemove(t *testing.T) {
	notFound := &types.NotFoundException{Message: aws.String("not found")}
	throttled := errors.New("throttled")
	cases := []struct {
		name        string
		api         fakeSuppressionAPI
		wantRemoved bool
		wantErr     error
		wantDelete  bool
	}{
		{"removed", fakeSuppressionAPI{reason: types.SuppressionListReasonBounce}, true, nil, true},
		{"not on the list", fakeSuppressionAPI{getErr: notFound}, false, nil, false},
		{"gone between read and delete", fakeSuppressionAPI{reason: types.SuppressionListReasonBounce, deleteErr: notFound}, false, nil, true},
		{"complaint stays listed", fakeSuppressionAPI{reason: types.SuppressionListReasonComplaint}, false, ErrComplaintSuppressed, false},
		{"read error", fakeSuppressionAPI{getErr: throttled}, false, throttled, false},
		{"delete error", fakeSuppressionAPI{reason: types.SuppressionListReasonBounce, deleteErr: throttled}, false, throttled, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := c.api
			removed, err := NewSuppressionList(&api).Remove(context.Background(), "medlem@example.org")
			if removed != c.wantRemoved || !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("Remove = %v, %v; want %v, %v", removed, err, c.wantRemoved, c.wantErr)
			}
			if api.read != "medlem@example.org" {
				t.Fatalf("address read = %q", api.read)
			}
			if (api.deleted == "medlem@example.org") != c.wantDelete {
				t.Fatalf("deleted %q, want delete %v", api.deleted, c.wantDelete)
			}
		})
	}
}
