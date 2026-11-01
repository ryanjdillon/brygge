package accounting

import (
	"context"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// fakeVippsReport is an in-memory VippsReportSource: funds and fees are keyed
// by settlement date, so the Report API sync runs without the network.
type fakeVippsReport struct {
	msn     string
	ledgers []vipps.Ledger
	funds   map[string][]vipps.FundsEntry
	fees    map[string][]vipps.FeeEntry

	// ledgersErr, fundsErr and feesErr are returned by the matching call.
	ledgersErr, fundsErr, feesErr error
	// tryLaterFunds and tryLaterFees report a date as not finalised yet.
	tryLaterFunds, tryLaterFees bool

	// fundsCalls and feesCalls record the "ledger/date" each call asked for.
	fundsCalls, feesCalls []string
}

var _ VippsReportSource = (*fakeVippsReport)(nil)

func newFakeVippsReport(msn string) *fakeVippsReport {
	return &fakeVippsReport{
		msn:     msn,
		ledgers: []vipps.Ledger{{LedgerID: "900"}},
		funds:   map[string][]vipps.FundsEntry{},
		fees:    map[string][]vipps.FeeEntry{},
	}
}

func (f *fakeVippsReport) MSN() string { return f.msn }

func (f *fakeVippsReport) LedgersParsed(context.Context) ([]vipps.Ledger, error) {
	return f.ledgers, f.ledgersErr
}

func (f *fakeVippsReport) FundsForDate(_ context.Context, ledgerID, date string) ([]vipps.FundsEntry, bool, error) {
	f.fundsCalls = append(f.fundsCalls, ledgerID+"/"+date)
	if f.fundsErr != nil {
		return nil, false, f.fundsErr
	}
	if f.tryLaterFunds {
		return nil, true, nil
	}
	return f.funds[date], false, nil
}

func (f *fakeVippsReport) FeesForDate(_ context.Context, ledgerID, date string) ([]vipps.FeeEntry, bool, error) {
	f.feesCalls = append(f.feesCalls, ledgerID+"/"+date)
	if f.feesErr != nil {
		return nil, false, f.feesErr
	}
	if f.tryLaterFees {
		return nil, true, nil
	}
	return f.fees[date], false, nil
}
