package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

func rateQuoteDirectory(t *testing.T, routes []RouteConfig) *runtimeDirectory {
	t.Helper()
	digest := sha256.Sum256([]byte("pw"))
	directory, err := newRuntimeDirectory(Config{Users: []UserConfig{{
		Username: "alice", ExternalID: "alice", PasswordSHA256: hex.EncodeToString(digest[:]),
	}}})
	if err != nil {
		t.Fatalf("new directory: %v", err)
	}
	table, _, defaultRate, err := buildRoutes(routes, directory.resolveUID, directory.lookupGroupID)
	if err != nil {
		t.Fatalf("build routes: %v", err)
	}
	directory.defaultRate = defaultRate
	directory.routes = routingtable.NewAtomicTable(table)
	return directory
}

// A quote that ignored the destination priced every message at the default
// route. Both the console's rate tool and the customer-visible HTTP /rate
// endpoint read this, so the wrong number reached customers.
func TestRateQuoteFollowsTheDestinationsRoute(t *testing.T) {
	directory := rateQuoteDirectory(t, []RouteConfig{
		{ConnectorID: "premium", Order: 10, Rate: 2,
			Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^33"}}},
		{ConnectorID: "default-c", Order: 0, Rate: 0.5, Default: true},
	})

	quote, err := directory.Rate(context.Background(), "alice", "33612345678")
	if err != nil {
		t.Fatal(err)
	}
	if quote.UnitRate != 2 {
		t.Fatalf("filtered destination quoted %v, want the matching route's 2", quote.UnitRate)
	}
	if quote.SubmitSMCount != 1 {
		t.Fatalf("submit_sm_count=%d", quote.SubmitSMCount)
	}

	quote, err = directory.Rate(context.Background(), "alice", "15551234567")
	if err != nil {
		t.Fatal(err)
	}
	if quote.UnitRate != 0.5 {
		t.Fatalf("unmatched destination quoted %v, want the default route's 0.5", quote.UnitRate)
	}
}

// The quote must read the live table, not a snapshot taken at boot, or an
// operator who repriced a route through the admin plane keeps quoting the old
// price until the next restart.
func TestRateQuoteReadsTheLiveRoutingTable(t *testing.T) {
	directory := rateQuoteDirectory(t, []RouteConfig{
		{ConnectorID: "default-c", Order: 0, Rate: 0.5, Default: true},
	})
	reprised, _, _, err := buildRoutes([]RouteConfig{
		{ConnectorID: "premium", Order: 10, Rate: 4,
			Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^33"}}},
		{ConnectorID: "default-c", Order: 0, Rate: 0.5, Default: true},
	}, directory.resolveUID, directory.lookupGroupID)
	if err != nil {
		t.Fatal(err)
	}
	directory.routes.Store(reprised)

	quote, err := directory.Rate(context.Background(), "alice", "33612345678")
	if err != nil {
		t.Fatal(err)
	}
	if quote.UnitRate != 4 {
		t.Fatalf("quote=%v, want the repriced route's 4", quote.UnitRate)
	}
}

// With no default route an unmatched destination has no price at all. The quote
// keeps the pre-existing fallback rather than turning into an error, because
// /rate answering a price is a customer-visible contract and this change is
// about quoting the right route, not about changing that shape.
//
// The fallback is the highest-order route's rate — which is what the endpoint
// used to return for every destination, matched or not.
func TestRateQuoteFallsBackWhenNothingMatches(t *testing.T) {
	directory := rateQuoteDirectory(t, []RouteConfig{
		{ConnectorID: "premium", Order: 10, Rate: 2,
			Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^33"}}},
	})
	quote, err := directory.Rate(context.Background(), "alice", "15551234567")
	if err != nil {
		t.Fatal(err)
	}
	if quote.UnitRate != directory.defaultRate {
		t.Fatalf("quote=%v, want the %v fallback", quote.UnitRate, directory.defaultRate)
	}
}

// The fallback rate is the highest-order route's, which is why the old
// destination-blind quote was wrong in both directions: a customer routed over
// the cheap default route was quoted the expensive filtered route's price.
func TestTheOldFallbackRateWasTheHighestOrderRouteNotTheDefaultRoute(t *testing.T) {
	directory := rateQuoteDirectory(t, []RouteConfig{
		{ConnectorID: "premium", Order: 10, Rate: 2,
			Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^33"}}},
		{ConnectorID: "default-c", Order: 0, Rate: 0.5, Default: true},
	})
	if directory.defaultRate != 2 {
		t.Fatalf("fallback rate=%v, want the highest-order route's 2", directory.defaultRate)
	}
	quote, err := directory.Rate(context.Background(), "alice", "15551234567")
	if err != nil {
		t.Fatal(err)
	}
	if quote.UnitRate != 0.5 {
		t.Fatalf("quote=%v, want the default route's 0.5 that this message would take", quote.UnitRate)
	}
}

func TestRateQuoteStillRejectsAnUnknownUser(t *testing.T) {
	directory := rateQuoteDirectory(t, []RouteConfig{
		{ConnectorID: "default-c", Order: 0, Rate: 0.5, Default: true},
	})
	if _, err := directory.Rate(context.Background(), "mallory", "33612345678"); err == nil {
		t.Fatal("an unknown user was quoted a rate")
	}
}
