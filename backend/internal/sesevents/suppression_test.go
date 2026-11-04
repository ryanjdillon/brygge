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
	err     error
	address string
}

func (f *fakeSuppressionAPI) DeleteSuppressedDestination(_ context.Context, in *sesv2.DeleteSuppressedDestinationInput, _ ...func(*sesv2.Options)) (*sesv2.DeleteSuppressedDestinationOutput, error) {
	f.address = *in.EmailAddress
	return &sesv2.DeleteSuppressedDestinationOutput{}, f.err
}

func TestSuppressionListRemove(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantRemoved bool
		wantErr     bool
	}{
		{"removed", nil, true, false},
		{"not on the list", &types.NotFoundException{Message: aws.String("not found")}, false, false},
		{"other error", errors.New("throttled"), false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &fakeSuppressionAPI{err: c.err}
			removed, err := NewSuppressionList(api).Remove(context.Background(), "medlem@example.org")
			if removed != c.wantRemoved || (err != nil) != c.wantErr {
				t.Fatalf("Remove = %v, %v; want %v, err %v", removed, err, c.wantRemoved, c.wantErr)
			}
			if api.address != "medlem@example.org" {
				t.Fatalf("address sent = %q", api.address)
			}
		})
	}
}
