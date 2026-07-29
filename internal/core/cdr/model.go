// Package cdr defines the durable commercial record emitted by the Go submit
// lifecycle. CDRs deliberately contain routing and billing metadata only:
// message content, source addresses, destination addresses, credentials and
// callback URLs are never persisted in this model.
package cdr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

var (
	ErrNotFound     = errors.New("cdr not found")
	ErrInvalidInput = errors.New("invalid cdr input")
)

// DefaultCurrency is ISO 4217 XXX ("no currency"). Existing Jasmin route
// prices are unitless, so assigning a real currency without an operator-owned
// contract would make the CDR commercially misleading.
const DefaultCurrency = "XXX"

type State string

const (
	StateAdmitted         State = "ADMITTED"
	StateRetryPending     State = "RETRY_PENDING"
	StateUnknownAfterSend State = "UNKNOWN_AFTER_SEND"
	StateSMSCAccepted     State = "SMSC_ACCEPTED"
	StateSMSCRejected     State = "SMSC_REJECTED"
	StateTerminalTimeout  State = "TERMINAL_TIMEOUT"
)

func (state State) Terminal() bool {
	switch state {
	case StateSMSCAccepted, StateSMSCRejected, StateTerminalTimeout:
		return true
	default:
		return false
	}
}

type EventKind string

const (
	EventAdmitted         EventKind = "ADMITTED"
	EventRetryPending     EventKind = "RETRY_PENDING"
	EventUnknownAfterSend EventKind = "UNKNOWN_AFTER_SEND"
	EventSMSCAccepted     EventKind = "SMSC_ACCEPTED"
	EventSMSCRejected     EventKind = "SMSC_REJECTED"
	EventTerminalTimeout  EventKind = "TERMINAL_TIMEOUT"
)

type BillingMode string

const (
	BillingFree     BillingMode = "FREE"
	BillingPrepaid  BillingMode = "PREPAID"
	BillingPostpaid BillingMode = "POSTPAID"
	BillingSplit    BillingMode = "SPLIT"
)

func ModeForAmounts(early, late float64) BillingMode {
	switch {
	case early == 0 && late == 0:
		return BillingFree
	case early > 0 && late == 0:
		return BillingPrepaid
	case early == 0 && late > 0:
		return BillingPostpaid
	default:
		return BillingSplit
	}
}

// SubmitMetadata is the typed, process-local commercial context passed to the
// durable transaction. It intentionally does not travel in the legacy AMQP
// envelope, where adding CDR-only headers would change the compatibility
// surface observed by existing consumers.
type SubmitMetadata struct {
	GroupID     string
	RouteID     string
	Ingress     string
	Rate        float64
	Currency    string
	EarlyAmount float64
	LateAmount  float64
}

// Admission is the immutable commercial identity captured when one logical
// submit part crosses the durable admission boundary.
type Admission struct {
	ID          string
	MessageID   string
	PartNumber  int
	PartCount   int
	UserID      string
	GroupID     string
	RouteID     string
	ConnectorID string
	Ingress     string
	BillID      string
	Rate        float64
	Currency    string
	EarlyAmount float64
	LateAmount  float64
	BillingMode BillingMode
	OccurredAt  time.Time
}

// Record is the current projection for one admitted part. The quoted amounts
// are split explicitly: EarlyAmount was applied before durable admission;
// LateAmount is only eligible after an SMSC acceptance and is applied by the
// existing idempotent late-billing ledger.
type Record struct {
	Admission
	State         State
	AttemptID     int64
	SMPPStatus    string
	SMSCMessageID string
	UpdatedAt     time.Time
	TerminalAt    *time.Time
}

// Event is the immutable audit trail. Event.Key is the dedupe identity:
//
//   - cdr:<part-key>:admitted
//   - cdr:<part-key>:attempt:<attempt-id>:unknown
//   - cdr:<part-key>:attempt:<attempt-id>:result
//
// Replaying the same lifecycle transition therefore cannot create a second
// commercial event.
type Event struct {
	Key           string
	CDRID         string
	Kind          EventKind
	State         State
	AttemptID     int64
	SMPPStatus    string
	SMSCMessageID string
	OccurredAt    time.Time
}

type Repository interface {
	GetCDR(context.Context, string) (Record, error)
	ListCDREvents(context.Context, string) ([]Event, error)
}

func AdmissionEventKey(cdrID string) string {
	return "cdr:" + cdrID + ":admitted"
}

func UnknownEventKey(cdrID string, attemptID int64) string {
	return fmt.Sprintf("cdr:%s:attempt:%d:unknown", cdrID, attemptID)
}

func ResultEventKey(cdrID string, attemptID int64) string {
	return fmt.Sprintf("cdr:%s:attempt:%d:result", cdrID, attemptID)
}

func ValidateAdmission(value Admission) error {
	if strings.TrimSpace(value.ID) == "" ||
		strings.TrimSpace(value.MessageID) == "" ||
		value.PartNumber < 1 ||
		value.PartCount < value.PartNumber ||
		strings.TrimSpace(value.ConnectorID) == "" ||
		strings.TrimSpace(value.RouteID) == "" ||
		!validCurrency(value.Currency) ||
		!validAmount(value.Rate) ||
		!validAmount(value.EarlyAmount) ||
		!validAmount(value.LateAmount) ||
		value.OccurredAt.IsZero() {
		return ErrInvalidInput
	}
	switch value.BillingMode {
	case BillingFree, BillingPrepaid, BillingPostpaid, BillingSplit:
		if value.BillingMode != ModeForAmounts(value.EarlyAmount, value.LateAmount) {
			return ErrInvalidInput
		}
		return nil
	default:
		return ErrInvalidInput
	}
}

func validAmount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}
