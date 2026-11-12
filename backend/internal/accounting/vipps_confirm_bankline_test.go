package accounting

import (
	"errors"
	"testing"
)

// checkVippsConfirmBankLine: the confirm carries the deposit as exactly one
// bank_in debit and posts no other line to a bank account (BRY-674).
func TestCheckVippsConfirmBankLine(t *testing.T) {
	bankIn := func(code string, debit, credit float64) VippsReconcileLine {
		return VippsReconcileLine{Kind: "bank_in", AccountCode: code, Debit: debit, Credit: credit}
	}
	other := func(code string) VippsReconcileLine {
		return VippsReconcileLine{Kind: "revenue", AccountCode: code, Credit: 430}
	}
	cashOnly := inCashRange
	// A club whose configured bank account sits outside the cash range.
	withConfigured := func(code string) bool { return inCashRange(code) || code == "1960" }

	for _, tc := range []struct {
		name     string
		lines    []VippsReconcileLine
		isBank   func(string) bool
		wantCode string
		wantErr  bool
	}{
		{"one bank_in debit", []VippsReconcileLine{bankIn("1920", 430, 0), other("3900")}, cashOnly, "1920", false},
		{"no bank_in", []VippsReconcileLine{other("3900")}, cashOnly, "", true},
		{"two bank_in", []VippsReconcileLine{bankIn("1920", 215, 0), bankIn("1920", 215, 0), other("3900")}, cashOnly, "", true},
		{"bank_in as a credit", []VippsReconcileLine{bankIn("1920", 0, 430), other("3900")}, cashOnly, "", true},
		{"bank_in debit and credit", []VippsReconcileLine{bankIn("1920", 430, 1), other("3900")}, cashOnly, "", true},
		{"bank_in zero debit", []VippsReconcileLine{bankIn("1920", 0, 0), other("3900")}, cashOnly, "", true},
		{"other line on 1900", []VippsReconcileLine{bankIn("1920", 430, 0), other("1900")}, cashOnly, "", true},
		{"other line on 1920", []VippsReconcileLine{bankIn("1925", 430, 0), other("1920")}, cashOnly, "", true},
		{"other line on 1949", []VippsReconcileLine{bankIn("1920", 430, 0), other("1949")}, cashOnly, "", true},
		{"other line on 1950", []VippsReconcileLine{bankIn("1920", 430, 0), other("1950")}, cashOnly, "1920", false},
		{"other line on a configured bank account", []VippsReconcileLine{bankIn("1920", 430, 0), other("1960")}, withConfigured, "", true},
		{"1960 when not a bank account", []VippsReconcileLine{bankIn("1920", 430, 0), other("1960")}, cashOnly, "1920", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, err := checkVippsConfirmBankLine(tc.lines, tc.isBank)
			if tc.wantErr {
				if !errors.Is(err, ErrVippsConfirmBankLine) {
					t.Fatalf("err = %v, want ErrVippsConfirmBankLine", err)
				}
				return
			}
			if err != nil || code != tc.wantCode {
				t.Fatalf("code %q, err %v; want %q", code, err, tc.wantCode)
			}
		})
	}
}
