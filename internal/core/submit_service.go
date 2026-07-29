package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/warthog618/sms/encoding/gsm7"

	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/cdr"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

var (
	ErrNoRouteMatched      = errors.New("no route matched")
	ErrInvalidSubmitConfig = errors.New("invalid submit service configuration")
	ErrInvalidEnvelopeSet  = errors.New("invalid submit envelope set")
)

type BillingUserDirectory interface {
	GetUserIdentity(username string) (*billing.User, string, error)
}

// ThroughputGate enforces the user's per-second submit ceiling
// (MtMessagingCredential's http_throughput / smpps_throughput). ingress is the
// front door the submit arrived through — legacy keeps a separate allowance per
// protocol on CnxStatus, so HTTP traffic must not consume the SMPPs budget.
//
// Returning false rejects the submit outright; legacy does not queue or delay.
type ThroughputGate interface {
	AllowSubmit(username, ingress string, now time.Time) bool
}

type AMQPPublisher interface {
	Publish(ctx context.Context, exchange, routingKey string, message amqpcompat.Envelope) error
}

// SubmitPublicationBoundary atomically accepts a complete logical submit for
// durable publication. submittransaction.Service implements this interface.
// Keeping this interface in core avoids coupling admission to PostgreSQL.
type SubmitPublicationBoundary interface {
	AdmitSubmit(context.Context, []amqpcompat.Envelope) error
}

type SubmitCommercialPublicationBoundary interface {
	AdmitSubmitWithCDR(context.Context, []amqpcompat.Envelope, cdr.SubmitMetadata) error
}

type SubmitEnvelopeRequest struct {
	MessageID       string
	BillID          string
	CreatedAt       time.Time
	Username        string
	UserID          string
	ConnectorID     string
	SourceAddr      []byte
	DestinationAddr []byte
	DataCoding      uint8
	Priority        uint8
	// Connector PDU defaults resolved from the routed connector (GAP 4).
	SourceAddrTON        uint8
	SourceAddrNPI        uint8
	DestAddrTON          uint8
	DestAddrNPI          uint8
	ServiceType          string
	ProtocolID           uint8
	ReplaceIfPresentFlag uint8
	SmDefaultMsgID       uint8
	ScheduleAt           *time.Time
	ValidityPeriod       *time.Duration
	DLR                  bool
	DLRURL               string
	DLRLevel             int
	DLRMethod            string
	SourceConnector      string
	Bill                 billing.Bill
	Parts                []segmentation.Part
	CustomTLVs           []tlv.TLV
	SMPPSubmit           *smppwire.SubmitSMBody
}

type SubmitEnvelopeBuilder interface {
	BuildSubmitEnvelope(ctx context.Context, request SubmitEnvelopeRequest, part segmentation.Part) (amqpcompat.Envelope, error)
}

// RouteSelector routes a routable to a connector. A *routingtable.Table
// (static) or a *routingtable.AtomicTable (live-swappable for admin
// provisioning) both satisfy it — Table.Select has a value receiver.
type RouteSelector interface {
	Select(routingfilter.Routable) (routingtable.Route, bool, error)
}

// InterceptionTable is the interception seam the submit path consumes. Both
// interceptor.Table (fixed) and interceptor.AtomicTable (live-swappable, used
// when the admin plane can add interceptors at runtime) satisfy it.
type InterceptionTable interface {
	Intercept(ctx context.Context, runner interceptor.Runner, routable routingfilter.Routable) (interceptor.Result, error)
}

type SubmitServiceDependencies struct {
	InterceptorTable  InterceptionTable
	InterceptorRunner interceptor.Runner
	RoutingTable      RouteSelector
	BillingUsers      BillingUserDirectory
	EnvelopeBuilder   SubmitEnvelopeBuilder
	Publisher         AMQPPublisher
	Transaction       SubmitPublicationBoundary
	SelectConnector   func(routingtable.Route) (string, bool)
	// ConnectorPDUDefaults resolves the routed connector's default submit_sm PDU
	// params (TON/NPI, service_type, ...). Optional: when nil, the front-door
	// submit keeps zero defaults (legacy behaviour before GAP 4).
	ConnectorPDUDefaults func(connectorID string) (smppc.PDUDefaults, bool)
	// GroupIdentity resolves the stable legacy gid for CDRs. Billing's numeric
	// group id is an in-process routing identity and can change after a config
	// reorder, so it must never become the commercial identifier.
	GroupIdentity func(username string) (string, bool)
	NewMessageID  func() (string, error)
	NewBillID     func() (string, error)
	NewReference  func() (uint16, error)
	Now           func() time.Time
	// DLRRequestStore, when set, persists the submit-side DLR callback record
	// (dlr:<msgid>) so the DLRLookup correlation legs can resolve a receipt
	// back to this submit. Nil disables it (level-1 callbacks still work via
	// the response path; level-2/3 terminal receipts would find nothing).
	DLRRequestStore DLRRequestStore
	// ConnectorDLRExpiry resolves a routed connector's dlr_expiry (record TTL,
	// seconds). Nil or a non-positive result falls back to the legacy default.
	ConnectorDLRExpiry func(connectorID string) int64
	// Throughput enforces the user's per-second submit ceiling. Nil disables
	// the check, which is the pre-existing behaviour for callers that do not
	// provision the quota.
	Throughput ThroughputGate
	// Logger is the named jasmin-router logger. It records routing, billing,
	// and durable-admission outcomes without logging message content.
	Logger *slog.Logger
}

// DLRRequestStore persists the submit-side DLR request record.
type DLRRequestStore interface {
	StoreHTTPDLRRequest(ctx context.Context, msgID string, request dlr.HTTPDLRRequest) error
	StoreSMPPSDLRRequest(ctx context.Context, msgID string, request dlr.SMPPSDLRRequest) error
}

// DefaultDLRExpirySeconds is the legacy SMPPClientConfig dlr_expiry default.
const DefaultDLRExpirySeconds int64 = 86400

type SubmitService struct {
	dependencies SubmitServiceDependencies
}

func NewSubmitService(dependencies SubmitServiceDependencies) (*SubmitService, error) {
	if dependencies.InterceptorTable == nil || dependencies.RoutingTable == nil ||
		dependencies.BillingUsers == nil || dependencies.EnvelopeBuilder == nil ||
		(dependencies.Publisher == nil && dependencies.Transaction == nil) {
		return nil, ErrInvalidSubmitConfig
	}
	if dependencies.NewMessageID == nil {
		dependencies.NewMessageID = randomUUID
	}
	if dependencies.NewBillID == nil {
		dependencies.NewBillID = randomUUID
	}
	if dependencies.NewReference == nil {
		dependencies.NewReference = randomReference
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	return &SubmitService{dependencies: dependencies}, nil
}

func (service *SubmitService) Submit(ctx context.Context, request SubmitRequest) (string, error) {
	user, externalUserID, err := service.dependencies.BillingUsers.GetUserIdentity(request.Username)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthentication, err)
	}

	payload, err := submitPayload(request)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidParameter, err)
	}
	// A payload carrying the ESME's own UDH is binary, not text: its first bytes
	// are the header (05 00 03 ref total seq for 8-bit concatenation). Running
	// it through the GSM 03.38 encoder would replace every unmappable header
	// byte with '?', so the receiving SMSC sees a mangled header followed by
	// mangled content — the classic symptom of an ESME's long messages arriving
	// as garbage.
	if request.SMPPSubmit == nil && request.HexContent == "" && request.Coding == 0 && !request.HasUDHI() {
		payload = encodeLegacyGSM0338(payload)
	}
	state := user.GetState()
	createdAt := service.dependencies.Now()
	var groupID int64
	var cdrGroupID string
	if state.GID != nil {
		groupID = *state.GID
		if service.dependencies.GroupIdentity != nil {
			cdrGroupID, _ = service.dependencies.GroupIdentity(request.Username)
		}
	}
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		UserID:          user.UID(),
		GroupID:         groupID,
		SourceAddr:      routingfilter.BytesField{Present: request.From != "", Value: []byte(request.From)},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(request.Destination)},
		ShortMessage:    routingfilter.BytesField{Present: request.HexContent == "", Value: payload},
		MessagePayload:  routingfilter.BytesField{Present: request.HexContent != "", Value: payload},
		Timestamp:       createdAt,
		Tags:            append([]string(nil), request.Tags...),
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidParameter, err)
	}

	intercepted, err := service.dependencies.InterceptorTable.Intercept(ctx, service.dependencies.InterceptorRunner, routable)
	if err != nil {
		return "", err
	}
	if intercepted.Action == interceptor.ActionReject {
		service.logWarn("MT submit rejected by interceptor [user:%s] [source:%s]", request.Username, sourceConnectorOf(request))
		return "", ErrFilterRejected
	}
	route, found, err := service.dependencies.RoutingTable.Select(intercepted.Routable)
	if err != nil {
		service.logError("MT route selection failed [user:%s]: %v", request.Username, err)
		return "", err
	}
	if !found {
		service.logWarn("No MT route matched [user:%s] [source:%s]", request.Username, sourceConnectorOf(request))
		return "", ErrNoRouteMatched
	}
	connectorID := route.Connector().ID()
	if service.dependencies.SelectConnector != nil {
		selected, available := service.dependencies.SelectConnector(route)
		if !available {
			service.logWarn("MT route has no available connector [user:%s]", request.Username)
			return "", ErrNoRouteMatched
		}
		connectorID = selected
	}
	service.logDebug("Selected MT route [user:%s] [cid:%s] [rate:%g]", request.Username, connectorID, route.Rate())

	// QoS ceiling. Legacy checks this after routing and before billing, so an
	// over-rate submit is refused without being charged and without consuming a
	// message id (send.py:294, factory.py:427). One check per logical submit,
	// not per segment — a long message costs the user one slot regardless of
	// how many parts it becomes.
	if service.dependencies.Throughput != nil {
		if !service.dependencies.Throughput.AllowSubmit(request.Username, sourceConnectorOf(request), createdAt) {
			return "", ErrThroughputExceeded
		}
	}

	reference, err := service.dependencies.NewReference()
	if err != nil {
		return "", err
	}
	messageField := intercepted.Routable.ShortMessage()
	if !messageField.Present {
		messageField = intercepted.Routable.MessagePayload()
	}
	segmented, err := segmentation.Segment(segmentation.Request{
		Payload:            messageField.Value,
		DataCoding:         uint8(request.Coding),
		SplitMethod:        segmentation.SplitSAR,
		MaxParts:           10,
		Reference:          reference,
		CustomTLVs:         request.CustomTLVs,
		PreEncodedUDH:      request.HasUDHI(),
		PreserveSinglePart: request.SMPPSubmit != nil,
	})
	if err != nil {
		return "", err
	}
	parts := segmented.Parts()
	aggregateBill := billing.CalculateBill(route.Rate(), len(parts), user)
	perPartBill := billing.CalculateBill(route.Rate(), 1, user)
	messageID, err := service.dependencies.NewMessageID()
	if err != nil {
		return "", err
	}
	billID, err := service.dependencies.NewBillID()
	if err != nil {
		return "", err
	}
	priority := request.Priority
	if priority < 0 || priority > 3 {
		return "", fmt.Errorf("%w: priority %d", ErrInvalidParameter, priority)
	}
	// Resolve the routed connector's default submit_sm PDU params (GAP 4). The
	// HTTP front door does not expose TON/NPI etc. as user params, so the
	// connector defaults apply; an unset provider keeps zero (pre-GAP-4).
	var pduDefaults smppc.PDUDefaults
	if service.dependencies.ConnectorPDUDefaults != nil {
		if resolved, ok := service.dependencies.ConnectorPDUDefaults(connectorID); ok {
			pduDefaults = resolved
		}
	}
	sourceAddr := intercepted.Routable.SourceAddr().Value
	if len(sourceAddr) == 0 && pduDefaults.SourceAddr != "" {
		sourceAddr = []byte(pduDefaults.SourceAddr)
	}
	envelopeRequest := SubmitEnvelopeRequest{
		MessageID:            messageID,
		BillID:               billID,
		CreatedAt:            createdAt,
		Username:             request.Username,
		UserID:               externalUserID,
		ConnectorID:          connectorID,
		SourceAddr:           sourceAddr,
		DestinationAddr:      intercepted.Routable.DestinationAddr().Value,
		DataCoding:           uint8(request.Coding),
		Priority:             uint8(priority),
		SourceAddrTON:        pduDefaults.SourceAddrTON,
		SourceAddrNPI:        pduDefaults.SourceAddrNPI,
		DestAddrTON:          pduDefaults.DestAddrTON,
		DestAddrNPI:          pduDefaults.DestAddrNPI,
		ServiceType:          pduDefaults.ServiceType,
		ProtocolID:           pduDefaults.ProtocolID,
		ReplaceIfPresentFlag: pduDefaults.ReplaceIfPresentFlag,
		SmDefaultMsgID:       pduDefaults.SmDefaultMsgID,
		ScheduleAt:           cloneTime(request.SDT),
		ValidityPeriod:       cloneDuration(request.ValidityPeriod),
		DLR:                  request.DLR,
		DLRURL:               request.DLRUrl,
		DLRLevel:             request.DLRLevel,
		DLRMethod:            request.DLRMethod,
		SourceConnector:      sourceConnectorOf(request),
		Bill:                 perPartBill,
		Parts:                parts,
		CustomTLVs:           cloneTLVs(request.CustomTLVs),
		SMPPSubmit:           cloneSubmitSMBody(request.SMPPSubmit),
	}
	envelopes := make([]amqpcompat.Envelope, 0, len(parts))
	for index, part := range parts {
		if part.Sequence() != uint8(index+1) {
			return "", fmt.Errorf("%w: part %d has sequence %d", ErrInvalidEnvelopeSet, index, part.Sequence())
		}
		envelope, err := service.dependencies.EnvelopeBuilder.BuildSubmitEnvelope(ctx, envelopeRequest, part)
		if err != nil {
			return "", err
		}
		if err := validateSubmitEnvelope(envelope, connectorID, index); err != nil {
			return "", err
		}
		envelopes = append(envelopes, envelope)
	}

	// Persist the submit-side DLR request before enqueue (legacy
	// SMPPClientManagerPB write), so the response and terminal-receipt
	// correlation legs can resolve a receipt back to this message. Only the
	// httpapi front door writes here; the SMPPs path maps its own record.
	if request.DLR && request.DLRUrl != "" && service.dependencies.DLRRequestStore != nil && sourceConnectorOf(request) == "httpapi" {
		expiry := DefaultDLRExpirySeconds
		if service.dependencies.ConnectorDLRExpiry != nil {
			if resolved := service.dependencies.ConnectorDLRExpiry(connectorID); resolved > 0 {
				expiry = resolved
			}
		}
		if err := service.dependencies.DLRRequestStore.StoreHTTPDLRRequest(ctx, messageID, dlr.HTTPDLRRequest{
			URL:           request.DLRUrl,
			Level:         request.DLRLevel,
			Method:        request.DLRMethod,
			Connector:     connectorID,
			ExpirySeconds: expiry,
		}); err != nil {
			return "", fmt.Errorf("persist DLR request: %w", err)
		}
	}

	// The SMPPs equivalent. Legacy gates this on the ESME having asked for a
	// receipt at all (registered_delivery.receipt !=
	// NO_SMSC_DELIVERY_RECEIPT_REQUESTED, managers/clients.py:618) rather than on
	// a callback URL, because the receipt goes back over the bind rather than to
	// an HTTP endpoint.
	if request.SMPPSOrigin != nil && service.dependencies.DLRRequestStore != nil && sourceConnectorOf(request) == "smppsapi" {
		expiry := DefaultDLRExpirySeconds
		if service.dependencies.ConnectorDLRExpiry != nil {
			if resolved := service.dependencies.ConnectorDLRExpiry(connectorID); resolved > 0 {
				expiry = resolved
			}
		}
		origin := request.SMPPSOrigin
		if err := service.dependencies.DLRRequestStore.StoreSMPPSDLRRequest(ctx, messageID, dlr.SMPPSDLRRequest{
			SystemID:           origin.SystemID,
			SourceAddrTON:      origin.SourceAddrTON,
			SourceAddrNPI:      origin.SourceAddrNPI,
			SourceAddress:      request.From,
			DestinationAddrTON: origin.DestinationAddrTON,
			DestinationAddrNPI: origin.DestinationAddrNPI,
			DestinationAddress: request.Destination,
			SubmissionDate:     legacySubmissionDate(createdAt),
			RegisteredDelivery: origin.RegisteredDelivery,
			ExpirySeconds:      expiry,
		}); err != nil {
			return "", fmt.Errorf("persist SMPPs DLR request: %w", err)
		}
	}

	if err := user.AuthorizeAndApplyCalculatedSubmit(route.Rate(), len(parts), aggregateBill); err != nil {
		service.logWarn("Charging user failed [user:%s] [cid:%s] [parts:%d]: %v",
			request.Username, connectorID, len(parts), err)
		return "", fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}
	if service.dependencies.Transaction != nil {
		var admissionErr error
		if commercial, ok := service.dependencies.Transaction.(SubmitCommercialPublicationBoundary); ok {
			admissionErr = commercial.AdmitSubmitWithCDR(ctx, envelopes, cdr.SubmitMetadata{
				GroupID: cdrGroupID, RouteID: route.ID(),
				Ingress: sourceConnectorOf(request), Rate: route.Rate(),
				Currency:    cdr.DefaultCurrency,
				EarlyAmount: perPartBill.SubmitSmAmount,
				LateAmount:  perPartBill.SubmitSmRespAmount,
			})
		} else {
			admissionErr = service.dependencies.Transaction.AdmitSubmit(ctx, envelopes)
		}
		if admissionErr != nil {
			// Legacy HTTP/SMPP paths charge before invoking the client manager and
			// never refund on a downstream/durable-admission failure.
			service.logError("Durable submit admission failed [user:%s] [cid:%s] [msgid:%s]: %v",
				request.Username, connectorID, messageID, admissionErr)
			return "", admissionErr
		}
	} else {
		for _, envelope := range envelopes {
			if err := service.dependencies.Publisher.Publish(ctx, "messaging", envelope.RoutingKey(), envelope); err != nil {
				// Explicit compatibility fallback for non-production callers. The
				// no-refund behavior is intentionally preserved.
				service.logError("Submit publish failed [user:%s] [cid:%s] [msgid:%s]: %v",
					request.Username, connectorID, messageID, err)
				return "", err
			}
		}
	}
	service.logInfo("MT submit routed [user:%s] [cid:%s] [msgid:%s] [parts:%d]",
		request.Username, connectorID, messageID, len(parts))
	return messageID, nil
}

func (service *SubmitService) logDebug(format string, args ...any) {
	if service.dependencies.Logger != nil {
		service.dependencies.Logger.Debug(fmt.Sprintf(format, args...))
	}
}

func (service *SubmitService) logInfo(format string, args ...any) {
	if service.dependencies.Logger != nil {
		service.dependencies.Logger.Info(fmt.Sprintf(format, args...))
	}
}

func (service *SubmitService) logWarn(format string, args ...any) {
	if service.dependencies.Logger != nil {
		service.dependencies.Logger.Warn(fmt.Sprintf(format, args...))
	}
}

func (service *SubmitService) logError(format string, args ...any) {
	if service.dependencies.Logger != nil {
		service.dependencies.Logger.Error(fmt.Sprintf(format, args...))
	}
}

func submitPayload(request SubmitRequest) ([]byte, error) {
	if request.HexContent != "" {
		payload, err := hex.DecodeString(request.HexContent)
		if err != nil {
			return nil, err
		}
		return payload, nil
	}
	return []byte(request.Content), nil
}

// encodeLegacyGSM0338 mirrors Python's text.encode("gsm0338", "replace"):
// extension-table runes consume ESC plus one septet and unsupported runes are
// replaced with '?'. These unpacked septets are the legacy segmentation input.
func encodeLegacyGSM0338(payload []byte) []byte {
	encoded := make([]byte, 0, len(payload))
	for _, value := range string(payload) {
		part, err := gsm7.Encode([]byte(string(value)))
		if err != nil {
			encoded = append(encoded, 0x3f)
			continue
		}
		encoded = append(encoded, part...)
	}
	return encoded
}

func validateSubmitEnvelope(envelope amqpcompat.Envelope, connectorID string, index int) error {
	route := envelope.Route()
	if route.Kind() != amqpcompat.RouteSubmitSM || route.Target() != connectorID {
		return fmt.Errorf("%w: envelope %d routes to %q", ErrInvalidEnvelopeSet, index, envelope.RoutingKey())
	}
	return nil
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func randomReference() (uint16, error) {
	var value [2]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}
	return uint16(value[0])<<8 | uint16(value[1]), nil
}

// legacySubmissionDate renders sub_date the way the frozen record stores it:
// Python writes `datetime.datetime.now()` into the hash, and redis stringifies
// it as `str(datetime)` — "2006-01-02 15:04:05.999999". Python omits the
// fractional part entirely when the microsecond field is zero, so this does
// too; a trailing ".000000" would not match a captured fixture.
func legacySubmissionDate(at time.Time) string {
	if at.Nanosecond() == 0 {
		return at.Format("2006-01-02 15:04:05")
	}
	return at.Format("2006-01-02 15:04:05.000000")
}

// sourceConnectorOf returns the request's ingress name, defaulting to the
// legacy "httpapi" when unset so existing callers are unchanged.
func sourceConnectorOf(request SubmitRequest) string {
	if request.SourceConnector == "smppsapi" {
		return "smppsapi"
	}
	return "httpapi"
}

// cloneTLVs copies the tuple list; fields (tag, length hint, value) are shared
// as immutable, mirroring Python's shared tuples.
func cloneTLVs(values []tlv.TLV) []tlv.TLV {
	if values == nil {
		return nil
	}
	return append([]tlv.TLV(nil), values...)
}

func cloneSubmitSMBody(value *smppwire.SubmitSMBody) *smppwire.SubmitSMBody {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.ServiceType = append([]byte(nil), value.ServiceType...)
	cloned.SourceAddress = append([]byte(nil), value.SourceAddress...)
	cloned.DestinationAddress = append([]byte(nil), value.DestinationAddress...)
	cloned.ScheduleDeliveryTime = append([]byte(nil), value.ScheduleDeliveryTime...)
	cloned.ValidityPeriod = append([]byte(nil), value.ValidityPeriod...)
	cloned.ShortMessage = append([]byte(nil), value.ShortMessage...)
	cloned.Optional = cloneSMPPOptional(value.Optional)
	cloned.VendorTLVs = append([]byte(nil), value.VendorTLVs...)
	cloned.CapturedVendorTLVs = make([]smppwire.CapturedVendorTLV, len(value.CapturedVendorTLVs))
	for index, item := range value.CapturedVendorTLVs {
		cloned.CapturedVendorTLVs[index] = smppwire.CapturedVendorTLV{
			Tag: item.Tag, Value: append([]byte(nil), item.Value...),
		}
	}
	return &cloned
}

func cloneSMPPOptional(value smppwire.OptionalParameters) smppwire.OptionalParameters {
	cloned := value
	if value.SARMessageReference != nil {
		item := *value.SARMessageReference
		cloned.SARMessageReference = &item
	}
	for source, target := range map[*byte]**byte{
		value.SARTotalSegments:   &cloned.SARTotalSegments,
		value.SARSegmentSequence: &cloned.SARSegmentSequence,
		value.MoreMessagesToSend: &cloned.MoreMessagesToSend,
		value.SourceAddrSubunit:  &cloned.SourceAddrSubunit,
		value.DestAddrSubunit:    &cloned.DestAddrSubunit,
		value.UserResponseCode:   &cloned.UserResponseCode,
		value.PayloadType:        &cloned.PayloadType,
		value.PrivacyIndicator:   &cloned.PrivacyIndicator,
		value.LanguageIndicator:  &cloned.LanguageIndicator,
		value.DisplayTime:        &cloned.DisplayTime,
		value.NumberOfMessages:   &cloned.NumberOfMessages,
		value.MessageState:       &cloned.MessageState,
		value.SourceNetworkType:  &cloned.SourceNetworkType,
		value.DestNetworkType:    &cloned.DestNetworkType,
		value.SourceBearerType:   &cloned.SourceBearerType,
		value.DestBearerType:     &cloned.DestBearerType,
	} {
		if source != nil {
			item := *source
			*target = &item
		}
	}
	for source, target := range map[*uint16]**uint16{
		value.UserMessageReference: &cloned.UserMessageReference,
		value.SourcePort:           &cloned.SourcePort,
		value.DestinationPort:      &cloned.DestinationPort,
		value.SourceTelematicsID:   &cloned.SourceTelematicsID,
		value.DestTelematicsID:     &cloned.DestTelematicsID,
	} {
		if source != nil {
			item := *source
			*target = &item
		}
	}
	if value.QoSTimeToLive != nil {
		item := *value.QoSTimeToLive
		cloned.QoSTimeToLive = &item
	}
	cloned.MessagePayload = cloneOptionalBytes(value.MessagePayload)
	cloned.ReceiptedMessageID = cloneOptionalBytes(value.ReceiptedMessageID)
	cloned.NetworkErrorCode = cloneOptionalBytes(value.NetworkErrorCode)
	cloned.SMSSignal = cloneOptionalBytes(value.SMSSignal)
	if value.SourceSubaddress != nil {
		subaddress := *value.SourceSubaddress
		subaddress.Value = append([]byte(nil), value.SourceSubaddress.Value...)
		cloned.SourceSubaddress = &subaddress
	}
	if value.DestSubaddress != nil {
		subaddress := *value.DestSubaddress
		subaddress.Value = append([]byte(nil), value.DestSubaddress.Value...)
		cloned.DestSubaddress = &subaddress
	}
	if value.CallbackNum != nil {
		callback := *value.CallbackNum
		callback.Digits = append([]byte(nil), value.CallbackNum.Digits...)
		cloned.CallbackNum = &callback
	}
	return cloned
}

func cloneOptionalBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte{}, value...)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneDuration(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
