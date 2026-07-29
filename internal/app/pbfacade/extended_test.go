package pbfacade

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestUserEnableDisableAndQuotaFamiliesMutateLiveService(t *testing.T) {
	users := &mutableUsers{rows: map[string]admin.StoredUser{
		"alice": {
			Username: "alice",
			UID:      4,
			SpecJSON: `{"username":"alice","external_id":"a1","password_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","balance":10,"mt_credential":{"http_throughput":2},"smpps_credential":{"max_bindings":1}}`,
		},
	}}
	handler := mustHandler(t, Deps{Users: users, Token: "secret"})

	for _, test := range []struct {
		method string
		params string
	}{
		{"router.user.disable", `{"id":"alice"}`},
		{"router.user.update_quota", `{"id":"alice","cred":"mt_credential","quota":"balance","value":-2.5}`},
		{"router.user.set_quota", `{"id":"alice","cred":"smpps_credential","quota":"max_bindings","value":7}`},
	} {
		if _, err := handler.dispatch(context.Background(), test.method, json.RawMessage(test.params)); err != nil {
			t.Fatalf("%s: %v", test.method, err)
		}
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(users.rows["alice"].SpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["disabled"] != true || spec["balance"] != 7.5 {
		t.Fatalf("mutated user spec = %#v", spec)
	}
	smpps := spec["smpps_credential"].(map[string]any)
	if smpps["max_bindings"] != float64(7) {
		t.Fatalf("smpps credential = %#v", smpps)
	}
}

func TestBulkFlushAndStopAllAreRealOperations(t *testing.T) {
	routes := &deletingOrderedService{rows: []admin.StoredSpec{{Order: 20}, {Order: 10}}}
	ctx := context.Background()
	lifecycle := &bulkConnectorService{
		views: []admin.ConnectorView{
			{Config: smppc.Config{CID: "one"}, DesiredStarted: true},
			{Config: smppc.Config{CID: "two"}, DesiredStarted: false},
		},
	}
	handler := mustHandler(t, Deps{Connectors: lifecycle, MORoutes: routes, Token: "secret"})
	if _, err := handler.dispatch(ctx, "router.moroute.flush", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(routes.deleted, []int{20, 10}) {
		t.Fatalf("deleted routes = %v", routes.deleted)
	}
	if _, err := handler.dispatch(ctx, "client.connector.stop_all", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lifecycle.stopped, []string{"one"}) {
		t.Fatalf("stopped connectors = %v", lifecycle.stopped)
	}
}

func TestConnectorLifecycleCountersAreProjected(t *testing.T) {
	connectors := &bulkConnectorService{
		views: []admin.ConnectorView{{Config: smppc.Config{CID: "one"}}},
	}
	handler := mustHandler(t, Deps{Connectors: connectors, Token: "secret"})
	ctx := context.Background()
	for _, method := range []string{"client.connector.start", "client.connector.stop", "client.connector.start"} {
		if _, err := handler.dispatch(ctx, method, json.RawMessage(`{"id":"one"}`)); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	result, err := handler.dispatch(ctx, "client.connector.get", json.RawMessage(`{"id":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	projected := result.(map[string]any)
	if projected["start_count"] != 2 || projected["stop_count"] != 1 {
		t.Fatalf("lifecycle counters = %#v", projected)
	}
}

func TestSubmitSMPPAndInterceptorMethodFamilies(t *testing.T) {
	submitter := &pbSubmitter{}
	server := &pbSMPPServer{}
	runner := &pbScriptRunner{}
	handler := mustHandler(t, Deps{
		Connectors:   &bulkConnectorService{views: []admin.ConnectorView{{Config: smppc.Config{CID: "forced-cid"}}}},
		Submitter:    submitter,
		SMPPServer:   server,
		ScriptRunner: runner,
		Token:        "secret",
	})
	message, err := handler.dispatch(context.Background(), "router.submit_sm", json.RawMessage(
		`{"username":"alice","connector_id":"forced-cid","bill":{"id":"bill-1","submit_sm_amount":0.25,"submit_sm_resp_amount":0.75,"decrement_submit_sm_count":1},"priority":3,"validity_period":"2026-07-29 12:34:56","dlr_connector":"receipt-cid","pdu":{"DestinationAddress":"MTIz","SourceAddress":"NDU2","ShortMessage":"AP8=","DataCoding":4}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if message != "message-id" || submitter.request.HexContent != "00ff" || submitter.request.Destination != "123" {
		t.Fatalf("submit result=%#v request=%#v", message, submitter.request)
	}
	trusted := submitter.request.TrustedManagerSubmit
	if trusted == nil || trusted.ConnectorID != "forced-cid" || trusted.BillID != "bill-1" ||
		trusted.Bill.SubmitSmAmount != 0.25 || trusted.Bill.SubmitSmRespAmount != 0.75 ||
		trusted.Bill.DecrementSubmitSmCount != 1 || trusted.DLRConnector != "receipt-cid" ||
		trusted.ValidityPeriod != "2026-07-29 12:34:56" || submitter.request.Priority != 3 {
		t.Fatalf("trusted submit projection=%+v", trusted)
	}
	if _, err := handler.dispatch(context.Background(), "router.submit_sm", json.RawMessage(
		`{"username":"alice","connector_id":"forced-cid","priority":4,"pdu":{"DestinationAddress":"MTIz","ShortMessage":"aGk="}}`,
	)); err == nil {
		t.Fatal("invalid explicit priority was accepted")
	}
	if submitter.calls != 1 {
		t.Fatalf("invalid priority reached submitter: calls=%d", submitter.calls)
	}

	delivered, err := handler.dispatch(context.Background(), "smpps.deliverer_send_request", json.RawMessage(
		`{"system_id":"alice","pdu":{"Header":{"CommandID":5,"SequenceNumber":9},"SM":{"DestinationAddress":"MTIz","ShortMessage":"aGk="}}}`,
	))
	if err != nil || delivered != true || server.delivered.Header.SequenceNumber != 9 {
		t.Fatalf("deliver result=%#v err=%v pdu=%#v", delivered, err, server.delivered)
	}

	result, err := handler.dispatch(context.Background(), "interceptor.run_script", json.RawMessage(
		`{"script":"pass","source_addr":"MQ==","destination_addr":"Mg==","short_message":"aGk=","tags":["old"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	projected := result.(map[string]any)
	if string(projected["short_message"].([]byte)) != "changed" {
		t.Fatalf("script projection = %#v", projected)
	}
}

func TestDeleteQueuesIsAnHonestNonDestructiveArchitectureError(t *testing.T) {
	connectors := &bulkConnectorService{
		views: []admin.ConnectorView{{Config: smppc.Config{CID: "one"}, DesiredStarted: true}},
	}
	handler := mustHandler(t, Deps{Connectors: connectors, Token: "secret"})
	if _, err := handler.dispatch(
		context.Background(),
		"client.connector.stop",
		json.RawMessage(`{"id":"one","delete_queues":true}`),
	); err == nil {
		t.Fatal("delete_queues=true unexpectedly succeeded")
	}
	if len(connectors.stopped) != 0 || !connectors.views[0].DesiredStarted {
		t.Fatalf("unsupported delete mutated connector: %+v", connectors)
	}
}

func TestSubmitPDUWireChainPreservesEveryOrderedPart(t *testing.T) {
	submitter := &pbSubmitter{}
	handler := mustHandler(t, Deps{
		Connectors: &bulkConnectorService{views: []admin.ConnectorView{{Config: smppc.Config{CID: "forced-cid"}}}},
		Submitter:  submitter,
		Token:      "secret",
	})
	wire := make([][]byte, 0, 2)
	for index, content := range [][]byte{[]byte("part-1"), []byte("part-2")} {
		sequence := byte(index + 1)
		total := byte(2)
		reference := uint16(17)
		body := &smppwire.SubmitSMBody{
			SourceAddress:      []byte("123"),
			DestinationAddress: []byte("456"),
			ShortMessage:       content,
			Optional: smppwire.OptionalParameters{
				SARMessageReference: &reference,
				SARTotalSegments:    &total,
				SARSegmentSequence:  &sequence,
			},
		}
		encoded, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: uint32(index + 1)},
			SM:     body,
		})
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, encoded)
	}
	params, err := json.Marshal(map[string]any{
		"username":     "alice",
		"connector_id": "forced-cid",
		"pdu_wires":    wire,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.dispatch(context.Background(), "router.submit_sm", params); err != nil {
		t.Fatal(err)
	}
	chain := submitter.request.TrustedManagerSubmit.SubmitSMChain
	if len(chain) != 2 || string(chain[0].ShortMessage) != "part-1" ||
		string(chain[1].ShortMessage) != "part-2" ||
		chain[0].Optional.SARSegmentSequence == nil || *chain[0].Optional.SARSegmentSequence != 1 ||
		chain[1].Optional.SARSegmentSequence == nil || *chain[1].Optional.SARSegmentSequence != 2 {
		t.Fatalf("decoded ordered chain=%+v", chain)
	}
}

func TestRuntimeProfilesRollsBackRowsAndLiveStateOnReconcileFailure(t *testing.T) {
	store := &fakeSnapshotStore{current: "before", profiles: map[string]string{"bad": "bad"}}
	reconciler := &fakeReconciler{store: store, reject: "bad"}
	profiles, err := NewRuntimeProfiles(store, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.Load(context.Background(), "bad"); err == nil {
		t.Fatal("bad profile load succeeded")
	}
	if store.current != "before" || reconciler.live != "before" {
		t.Fatalf("rollback current=%q live=%q", store.current, reconciler.live)
	}
	if profiles.IsPersisted() {
		t.Fatal("failed restore reported persisted")
	}
}

type mutableUsers struct {
	rows map[string]admin.StoredUser
}

func (s *mutableUsers) CreateUser(_ context.Context, id, spec string) error {
	row := s.rows[id]
	row.Username, row.SpecJSON = id, spec
	s.rows[id] = row
	return nil
}
func (s *mutableUsers) DeleteUser(_ context.Context, id string) error {
	delete(s.rows, id)
	return nil
}
func (s *mutableUsers) ListUsers(context.Context) ([]admin.StoredUser, error) {
	result := make([]admin.StoredUser, 0, len(s.rows))
	for _, row := range s.rows {
		result = append(result, row)
	}
	return result, nil
}
func (s *mutableUsers) GetUser(_ context.Context, id string) (admin.StoredUser, error) {
	row, ok := s.rows[id]
	if !ok {
		return admin.StoredUser{}, admin.ErrUserNotFound
	}
	return row, nil
}

type deletingOrderedService struct {
	rows    []admin.StoredSpec
	deleted []int
}

func (*deletingOrderedService) PutRoute(context.Context, int, string) error { return nil }
func (s *deletingOrderedService) DeleteRoute(_ context.Context, order int) error {
	s.deleted = append(s.deleted, order)
	return nil
}
func (s *deletingOrderedService) ListRoutes(context.Context) ([]admin.StoredSpec, error) {
	return append([]admin.StoredSpec(nil), s.rows...), nil
}
func (*deletingOrderedService) GetRoute(context.Context, int) (admin.StoredSpec, error) {
	return admin.StoredSpec{}, nil
}

type bulkConnectorService struct {
	views   []admin.ConnectorView
	stopped []string
}

func (*bulkConnectorService) CreateConnector(context.Context, smppc.Config, bool) error {
	panic("not used")
}

func (*bulkConnectorService) DeleteConnector(context.Context, string) error { return nil }
func (s *bulkConnectorService) SetStarted(_ context.Context, id string, started bool) error {
	for index := range s.views {
		if s.views[index].Config.CID == id {
			s.views[index].DesiredStarted = started
		}
	}
	if !started {
		s.stopped = append(s.stopped, id)
	}
	return nil
}
func (s *bulkConnectorService) ListConnectors(context.Context) ([]admin.ConnectorView, error) {
	return s.views, nil
}
func (s *bulkConnectorService) GetConnector(_ context.Context, id string) (admin.ConnectorView, error) {
	for _, view := range s.views {
		if view.Config.CID == id {
			return view, nil
		}
	}
	return admin.ConnectorView{}, admin.ErrConnectorNotFound
}

type pbSubmitter struct {
	request core.SubmitRequest
	calls   int
}

func (s *pbSubmitter) Submit(_ context.Context, request core.SubmitRequest) (string, error) {
	s.request = request
	s.calls++
	return "message-id", nil
}

type pbSMPPServer struct {
	delivered smppwire.PDU
}

func (*pbSMPPServer) BoundSystemIDs() []string { return []string{"alice"} }
func (*pbSMPPServer) UnbindUser(string) int    { return 1 }
func (s *pbSMPPServer) Deliver(_ context.Context, _ string, pdu smppwire.PDU) error {
	s.delivered = pdu
	return nil
}

type pbScriptRunner struct{}

func (*pbScriptRunner) Run(_ context.Context, _ interceptor.Script, input interceptor.Context) (interceptor.Result, error) {
	routable := input.Routable.Clone()
	if err := routable.SetShortMessage([]byte("changed")); err != nil {
		return interceptor.Result{}, err
	}
	return interceptor.Result{Routable: routable, Action: interceptor.ActionContinue}, nil
}

type fakeSnapshotStore struct {
	current  string
	profiles map[string]string
}

func (s *fakeSnapshotStore) Save(_ context.Context, profile string) error {
	s.profiles[profile] = s.current
	return nil
}
func (s *fakeSnapshotStore) Load(_ context.Context, profile string) error {
	value, ok := s.profiles[profile]
	if !ok {
		return errors.New("missing profile")
	}
	s.current = value
	return nil
}

type fakeReconciler struct {
	store  *fakeSnapshotStore
	reject string
	live   string
}

func (r *fakeReconciler) LoadAndApply(context.Context) error {
	if r.store.current == r.reject {
		return errors.New("rejected")
	}
	r.live = r.store.current
	return nil
}
