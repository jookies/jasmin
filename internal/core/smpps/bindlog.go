package smpps

import "fmt"

// bindTypeName renders a BindType as Python's `%s` of the smpp CommandId enum —
// the fully-qualified form the legacy SMPPServerFactory logs (e.g.
// "CommandId.bind_transceiver"), matching connection.bind_type in the bind line.
func bindTypeName(t BindType) string {
	switch t {
	case BindTransmitter:
		return "CommandId.bind_transmitter"
	case BindTransceiver:
		return "CommandId.bind_transceiver"
	default:
		return "CommandId.bind_receiver"
	}
}

// boundCountsStr reproduces getBoundConnectionCountsStr: the per-type bind counts
// in the legacy _binds order (transceiver, transmitter, receiver), each rendered
// "CommandId.bind_X: N" and joined by ", ".
func boundCountsStr(manager *BindManager) string {
	return fmt.Sprintf("%s: %d, %s: %d, %s: %d",
		bindTypeName(BindTransceiver), manager.CountByType(BindTransceiver),
		bindTypeName(BindTransmitter), manager.CountByType(BindTransmitter),
		bindTypeName(BindReceiver), manager.CountByType(BindReceiver))
}

// logBind emits the legacy SMPPServerFactory bind/unbind line at INFO (logger
// smpp.server.<id>). action is "Added" or "Dropped"; the manager counts are read
// under the server lock the callers already hold, so the count reflects the state
// after the add/remove, matching the legacy which logs after (add|remove)Binding.
func (s *Server) logBind(action string, bindType BindType, systemID string, manager *BindManager) {
	if s.logger == nil {
		return
	}
	s.logger.Info(fmt.Sprintf("%s %s bind for '%s'. Active binds: %s.",
		action, bindTypeName(bindType), systemID, boundCountsStr(manager)))
}
