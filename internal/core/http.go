package core

import (
	"context"
	"errors"
)

var (
	ErrAuthentication  = errors.New("authentication failed")
	ErrNoLiveConnector = errors.New("no live connector")
)

// Authenticator verifies that a user and its group are enabled and that the
// supplied credentials are valid.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) error
}

// BalanceSnapshot keeps decimal values as strings so the core never loses
// precision. A nil value represents the legacy unlimited quota.
type BalanceSnapshot struct {
	Balance  *string
	SMSCount *string
}

type BalanceReader interface {
	Balance(ctx context.Context, username string) (BalanceSnapshot, error)
}

type RateQuote struct {
	UnitRate      float64
	SubmitSMCount int
}

type RateReader interface {
	Rate(ctx context.Context, username, destination string) (RateQuote, error)
}

type SubmitRequest struct {
	Username    string
	Destination string
	Content     string
	HexContent  string
}

type Submitter interface {
	Submit(ctx context.Context, request SubmitRequest) (messageID string, err error)
}
