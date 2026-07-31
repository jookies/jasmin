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
	ErrForbidden    = errors.New("cdr access forbidden")
	ErrDisabled     = errors.New("cdr operation disabled")
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
	// StateTerminatedLocally is the terminating gateway's own acceptance: the
	// message stopped here, was decoded and spooled, and this process is the
	// destination rather than a relay.
	//
	// It exists instead of reusing SMSC_ACCEPTED because the two are different
	// facts and only one of them involves an upstream carrier. Overloading
	// SMSC_ACCEPTED would also inherit its side effect -- that transition is
	// what publishes the late-billing intent (smppc/response_publish.go) -- and
	// silently settle billing for terminated traffic as a side effect of fixing
	// receipts. Billing for terminated traffic is governed by the MT route's
	// rate, exactly as it is for relayed traffic: rate 0 bills nothing.
	StateTerminatedLocally State = "TERMINATED_LOCALLY"
)

func (state State) Terminal() bool {
	switch state {
	case StateSMSCAccepted, StateSMSCRejected, StateTerminalTimeout,
		StateTerminatedLocally:
		return true
	default:
		return false
	}
}

// DeliveryState is the normalized final handset-delivery outcome. Submission
// and delivery are separate dimensions: SMSC_ACCEPTED remains the immutable
// submission result while a later final DLR fills these fields.
type DeliveryState string

const (
	DeliveryNone          DeliveryState = ""
	DeliveryDelivered     DeliveryState = "DELIVERED"
	DeliveryExpired       DeliveryState = "EXPIRED"
	DeliveryDeleted       DeliveryState = "DELETED"
	DeliveryUndeliverable DeliveryState = "UNDELIVERABLE"
	DeliveryRejected      DeliveryState = "REJECTED"
)

func DeliveryStateForStatus(status string) (DeliveryState, error) {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "DELIVRD":
		return DeliveryDelivered, nil
	case "EXPIRED":
		return DeliveryExpired, nil
	case "DELETED":
		return DeliveryDeleted, nil
	case "UNDELIV":
		return DeliveryUndeliverable, nil
	case "REJECTD":
		return DeliveryRejected, nil
	default:
		return DeliveryNone, ErrInvalidInput
	}
}

type BillingOutcome string

const (
	BillingNotApplicable BillingOutcome = "NOT_APPLICABLE"
	BillingPending       BillingOutcome = "PENDING"
	BillingApplied       BillingOutcome = "APPLIED"
	BillingRejected      BillingOutcome = "REJECTED"
)

type EventKind string

const (
	EventAdmitted         EventKind = "ADMITTED"
	EventRetryPending     EventKind = "RETRY_PENDING"
	EventUnknownAfterSend EventKind = "UNKNOWN_AFTER_SEND"
	EventSMSCAccepted     EventKind = "SMSC_ACCEPTED"
	EventSMSCRejected     EventKind = "SMSC_REJECTED"
	EventTerminalTimeout  EventKind = "TERMINAL_TIMEOUT"
	// EventTerminatedLocally records the terminating connector accepting the
	// message. It is what makes the final DLR admissible below.
	EventTerminatedLocally EventKind = "TERMINATED_LOCALLY"
	EventFinalDLR          EventKind = "FINAL_DLR"
	EventLateBillApplied   EventKind = "LATE_BILLING_APPLIED"
	EventLateBillRejected  EventKind = "LATE_BILLING_REJECTED"
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
	State              State
	AttemptID          int64
	SMPPStatus         string
	SMSCMessageID      string
	DeliveryState      DeliveryState
	DeliveryStatus     string
	DeliveryError      string
	DeliveryDoneAt     *time.Time
	DeliveryReceivedAt *time.Time
	BillingOutcome     BillingOutcome
	ActualLateAmount   float64
	LateBillingAt      *time.Time
	UpdatedAt          time.Time
	TerminalAt         *time.Time
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
	Key              string
	CDRID            string
	Kind             EventKind
	State            State
	AttemptID        int64
	SMPPStatus       string
	SMSCMessageID    string
	DeliveryState    DeliveryState
	DeliveryStatus   string
	DeliveryError    string
	DeliveryDoneAt   *time.Time
	BillingOutcome   BillingOutcome
	ActualLateAmount float64
	OccurredAt       time.Time
}

type Repository interface {
	GetCDR(context.Context, string) (Record, error)
	ListCDREvents(context.Context, string) ([]Event, error)
}

// FinalDLR is the content-free terminal receipt projection recorded after the
// legacy correlation map has resolved the submit identity. DoneAt is the SMSC
// timestamp when parseable; ReceivedAt is the gateway's authoritative receipt
// observation time.
type FinalDLR struct {
	QueueMessageID string
	ConnectorID    string
	SMSCMessageID  string
	Status         string
	Error          string
	DoneAt         *time.Time
	ReceivedAt     time.Time
}

type FinalDLRRecorder interface {
	RecordFinalDLR(context.Context, FinalDLR) error
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

func FinalDLREventKey(cdrID string) string {
	return fmt.Sprintf("cdr:%s:dlr:final", cdrID)
}

func LateBillingEventKey(cdrID string, outcome BillingOutcome) string {
	return fmt.Sprintf("cdr:%s:late-billing:%s", cdrID, strings.ToLower(string(outcome)))
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

// ValidateCurrency accepts the ISO-4217-shaped currency code stored in a CDR.
// XXX remains the safe default for unitless legacy route prices.
func ValidateCurrency(value string) error {
	if !validCurrency(value) {
		return ErrInvalidInput
	}
	return nil
}
