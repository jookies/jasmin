package dlr

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
	"github.com/redis/go-redis/v9"
)

type capturePublisher struct {
	forwards []Forward
	err      error
}

func (p *capturePublisher) PublishDLR(_ context.Context, f Forward) error {
	if p.err != nil {
		return p.err
	}
	p.forwards = append(p.forwards, f)
	return nil
}

func newCorrelator(t *testing.T, cfg Config) (*Correlator, *rediscompat.Client, *capturePublisher) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	client := rediscompat.NewClient(rdb)
	pub := &capturePublisher{}
	return NewCorrelator(client, pub, cfg), client, pub
}

func writeHTTPDLR(t *testing.T, client *rediscompat.Client, queueMsgID string, level int64) {
	t.Helper()
	key, err := rediscompat.BuildDLRKey(queueMsgID)
	if err != nil {
		t.Fatalf("build dlr key: %v", err)
	}
	rec, err := rediscompat.NewHTTPDLRRecord(key, rediscompat.HTTPDLRRequest{
		URL: "http://cb/dlr", Level: level, Method: "POST", Connector: "smpp-01", ExpirySeconds: 86400,
	})
	if err != nil {
		t.Fatalf("build http dlr: %v", err)
	}
	if err := client.WriteHashRecord(context.Background(), rec); err != nil {
		t.Fatalf("seed dlr: %v", err)
	}
}

func mappingExists(t *testing.T, client *rediscompat.Client, canonicalSMSCID string) (map[string]string, bool) {
	t.Helper()
	key, err := rediscompat.BuildQueueMessageKey(canonicalSMSCID)
	if err != nil {
		t.Fatalf("build queue key: %v", err)
	}
	fields, err := client.ReadHash(context.Background(), key)
	if errors.Is(err, rediscompat.ErrKeyNotFound) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read mapping: %v", err)
	}
	return fields, true
}

func dlrDeleted(t *testing.T, client *rediscompat.Client, queueMsgID string) bool {
	t.Helper()
	key, _ := rediscompat.BuildDLRKey(queueMsgID)
	_, err := client.ReadHash(context.Background(), key)
	return errors.Is(err, rediscompat.ErrKeyNotFound)
}

func TestSubmitResp_HTTPLevel1_ForwardAndDelete(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	writeHTTPDLR(t, client, "q1", 1)
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "q1", SMPPMsgID: "abc", Status: "ESME_ROK"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 1 || pub.forwards[0].Target != ForwardHTTP || pub.forwards[0].Level != 1 {
		t.Fatalf("expected one level-1 http forward, got %+v", pub.forwards)
	}
	if !dlrDeleted(t, client, "q1") {
		t.Error("level 1: dlr must be deleted")
	}
	if _, ok := mappingExists(t, client, "ABC"); ok {
		t.Error("level 1: no queue-msgid mapping should be written")
	}
}

func TestSubmitResp_HTTPLevel3_OK_ForwardKeepAndMap(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	writeHTTPDLR(t, client, "q3", 3)
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "q3", SMPPMsgID: "00abc", Status: "ESME_ROK"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 1 {
		t.Fatalf("expected one forward, got %d", len(pub.forwards))
	}
	if dlrDeleted(t, client, "q3") {
		t.Error("level 3 + OK: dlr must be kept for the terminal receipt")
	}
	// SMPPMsgID "00abc" canonicalizes to "ABC".
	m, ok := mappingExists(t, client, "ABC")
	if !ok || m["msgid"] != "q3" || m["connector_type"] != "httpapi" {
		t.Errorf("expected httpapi mapping ABC->q3, got %v (ok=%v)", m, ok)
	}
}

func TestSubmitResp_HTTPLevel3_Error_ForwardAndDeleteNoMap(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	writeHTTPDLR(t, client, "qe", 3)
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "qe", SMPPMsgID: "abc", Status: "ESME_RSYSERR"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 1 {
		t.Fatalf("expected one forward, got %d", len(pub.forwards))
	}
	if !dlrDeleted(t, client, "qe") {
		t.Error("error status: dlr must be deleted (no terminal receipt will follow)")
	}
	if _, ok := mappingExists(t, client, "ABC"); ok {
		t.Error("error status: no mapping should be written")
	}
}

func TestSubmitResp_HTTPLevel2_OK_NoForwardMapOnly(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	writeHTTPDLR(t, client, "q2", 2)
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "q2", SMPPMsgID: "abc", Status: "ESME_ROK"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 0 {
		t.Errorf("level 2: no SMSC-level forward expected, got %d", len(pub.forwards))
	}
	if dlrDeleted(t, client, "q2") {
		t.Error("level 2: dlr must be kept")
	}
	if _, ok := mappingExists(t, client, "ABC"); !ok {
		t.Error("level 2 + OK: mapping should be written")
	}
}

func TestSubmitResp_MissingKey(t *testing.T) {
	c, _, _ := newCorrelator(t, Config{})
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "nope", SMPPMsgID: "x", Status: "ESME_ROK"}); !errors.Is(err, ErrDLRMapNotFound) {
		t.Errorf("missing dlr: got %v, want ErrDLRMapNotFound", err)
	}
}

func TestSubmitResp_UnknownSC(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	c := NewCorrelator(rediscompat.NewClient(rdb), &capturePublisher{}, Config{})
	// Inject a raw hash with an unrecognized sc, bypassing the validating constructors.
	mr.HSet("dlr:weird", "sc", "bogus", "level", "1", "expiry", "10")
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "weird", SMPPMsgID: "x", Status: "ESME_ROK"}); !errors.Is(err, ErrDLRMapInvalid) {
		t.Errorf("unknown sc: got %v, want ErrDLRMapInvalid", err)
	}
}

func writeSMPPSDLR(t *testing.T, client *rediscompat.Client, queueMsgID, rdReceipt string) {
	t.Helper()
	key, _ := rediscompat.BuildDLRKey(queueMsgID)
	rec, err := rediscompat.NewSMPPSDLRRecord(key, rediscompat.SMPPSDLRRequest{
		SystemID: "sys1", SourceAddrTON: "AddrTon.INTERNATIONAL", SourceAddrNPI: "AddrNpi.ISDN",
		SourceAddress: "12345", DestinationAddrTON: "AddrTon.INTERNATIONAL", DestinationAddrNPI: "AddrNpi.ISDN",
		DestinationAddress: "447700", SubmissionDate: "2101011200", RegisteredDeliveryReceipt: rdReceipt, ExpirySeconds: 3600,
	})
	if err != nil {
		t.Fatalf("build smpps dlr: %v", err)
	}
	if err := client.WriteHashRecord(context.Background(), rec); err != nil {
		t.Fatalf("seed smpps dlr: %v", err)
	}
}

func TestSubmitResp_SMPPS_OK_Requested_NoReceiptOnSuccess_MapOnly(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{SMPPReceiptOnSuccessSubmitSmResp: false})
	writeSMPPSDLR(t, client, "s1", rdReceiptRequested)
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "s1", SMPPMsgID: "abc", Status: "ESME_ROK"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 0 {
		t.Errorf("OK + !receiptOnSuccess: no forward, got %d", len(pub.forwards))
	}
	if _, ok := mappingExists(t, client, "ABC"); !ok {
		t.Error("OK: smppsapi mapping should be written")
	}
}

func TestSubmitResp_SMPPS_OK_Requested_ReceiptOnSuccess_ForwardAndMap(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{SMPPReceiptOnSuccessSubmitSmResp: true})
	writeSMPPSDLR(t, client, "s2", rdReceiptRequested)
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "s2", SMPPMsgID: "abc", Status: "ESME_ROK"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 1 || pub.forwards[0].Target != ForwardSMPPS || pub.forwards[0].SystemID != "sys1" {
		t.Fatalf("expected one smpps forward for sys1, got %+v", pub.forwards)
	}
	if _, ok := mappingExists(t, client, "ABC"); !ok {
		t.Error("OK: smppsapi mapping should be written")
	}
}

func TestSubmitResp_SMPPS_Error_ForFailure_ForwardNoMap(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	writeSMPPSDLR(t, client, "s3", rdReceiptRequestedForFailure)
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "s3", SMPPMsgID: "abc", Status: "ESME_RSYSERR"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 1 {
		t.Fatalf("error + FOR_FAILURE: expected one forward, got %d", len(pub.forwards))
	}
	if _, ok := mappingExists(t, client, "ABC"); ok {
		t.Error("error status: no mapping should be written")
	}
}

func TestSubmitResp_SMPPS_Error_RequestedOnly_Nothing(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	writeSMPPSDLR(t, client, "s4", rdReceiptRequested) // not FOR_FAILURE
	if err := c.OnSubmitResp(context.Background(), SubmitRespEvent{QueueMsgID: "s4", SMPPMsgID: "abc", Status: "ESME_RSYSERR"}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	if len(pub.forwards) != 0 {
		t.Errorf("error + REQUESTED (not FOR_FAILURE): nothing forwarded, got %d", len(pub.forwards))
	}
	if _, ok := mappingExists(t, client, "ABC"); ok {
		t.Error("no mapping should be written")
	}
}
