package openapi

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/paymentfilters"
)

// The spec's icon enum is what the SPA types its icon map against; it must
// list the icons the server accepts, in the editor's order.
func TestPaymentFilterIconEnumMatchesAllowList(t *testing.T) {
	want := paymentfilters.IconAllowList()
	for _, c := range []struct {
		typ   any
		field string
	}{
		{PaymentFilterGroup{}, "Icon"},
		{PaymentFilterGroupsListResponse{}, "Icons"},
		{CreatePaymentFilterGroupRequest{}, "Icon"},
		{UpdatePaymentFilterGroupRequest{}, "Icon"},
	} {
		f, ok := reflect.TypeOf(c.typ).FieldByName(c.field)
		if !ok {
			t.Fatalf("%T has no field %s", c.typ, c.field)
		}
		got := strings.Split(f.Tag.Get("enum"), ",")
		if !slices.Equal(got, want) {
			t.Errorf("%T.%s enum = %v, want the allow-list %v", c.typ, c.field, got, want)
		}
	}
}

func TestPaymentFilterLabelKeyEnumCoversSeededKeys(t *testing.T) {
	f, _ := reflect.TypeOf(PaymentFilterGroup{}).FieldByName("LabelKey")
	got := strings.Split(f.Tag.Get("enum"), ",")
	for _, k := range []string{paymentfilters.LabelFaktura, paymentfilters.LabelOther, "guest", "motorhome", "slipping"} {
		if !slices.Contains(got, k) {
			t.Errorf("label_key enum %v lacks %q", got, k)
		}
	}
}
