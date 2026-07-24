package mtcredential

import "strconv"

// SubmitRequest projects a submit_sm PDU for smpps credential validation. Fields map to
// SubmitSm.params: DestinationAddr (destination_addr), SourceAddr (source_addr),
// PriorityFlag (priority_flag, expected 0..3 — the wire codec enforces the enum),
// ShortMessage (short_message). DLRRequested is true when registered_delivery differs from
// NO_SMSC_DELIVERY_RECEIPT_REQUESTED (i.e. the ESME asked for any receipt/ack).
type SubmitRequest struct {
	DestinationAddr []byte
	SourceAddr      []byte
	PriorityFlag    byte
	ShortMessage    []byte
	DLRRequested    bool
}

// ValidateSubmit runs Jasmin's SmppsCredentialValidator 'Send' action against a submit_sm:
// _checkSendAuthorizations then _checkSendFilters, returning the first failure as a
// *ValidationError (nil when allowed). It is the smpps twin of ValidateSend over the same
// MtMessagingCredential — the triggers are PDU field values, not HTTP argument presence,
// and there are no validity_period / hex-content / schedule / long-content checks.
func ValidateSubmit(c *Credential, r SubmitRequest) error {
	if err := checkSubmitAuthorizations(c, r); err != nil {
		return err
	}
	return checkSubmitFilters(c, r)
}

// checkSubmitAuthorizations mirrors validation.py:SmppsCredentialValidator._checkSendAuthorizations.
// Order is significant: the first unauthorized attribute is the one reported.
func checkSubmitAuthorizations(c *Credential, r SubmitRequest) error {
	if !c.Authorization(AuthSMPPSSend) {
		return authErr(AuthSMPPSSend)
	}
	// A non-default registered_delivery (any receipt requested) needs set_dlr_level.
	if !c.Authorization(AuthSetDLRLevel) && r.DLRRequested {
		return authErr(AuthSetDLRLevel)
	}
	// A non-empty source_addr needs set_source_address.
	if !c.Authorization(AuthSetSourceAddress) && len(r.SourceAddr) > 0 {
		return authErr(AuthSetSourceAddress)
	}
	// A non-zero priority_flag needs set_priority (Python: != priority_flag_value_map[0]).
	if !c.Authorization(AuthSetPriority) && r.PriorityFlag != 0 {
		return authErr(AuthSetPriority)
	}
	return nil
}

// checkSubmitFilters mirrors validation.py:SmppsCredentialValidator._checkSendFilters. Every
// filter runs unconditionally (each maps to a mandatory PDU field), each skipped only when
// its pattern still equals its own default literal. The priority value filtered is the
// decimal priority level ("0".."3"), matching priority_flag_name_map[priority_flag.name].
func checkSubmitFilters(c *Credential, r SubmitRequest) error {
	if err := applyFilter(c, FilterDestinationAddress, r.DestinationAddr, patternAny); err != nil {
		return err
	}
	if err := applyFilter(c, FilterSourceAddress, r.SourceAddr, patternAny); err != nil {
		return err
	}
	priorityValue := []byte(strconv.Itoa(int(r.PriorityFlag)))
	if err := applyFilter(c, FilterPriority, priorityValue, patternPriority); err != nil {
		return err
	}
	return applyFilter(c, FilterContent, r.ShortMessage, patternAny)
}

// ApplyDefaultSourceAddressSubmit implements the smpps updatePDUWithUserDefaults: the user
// default source replaces an absent OR empty source_addr. This differs from the HTTP
// variant (ApplyDefaultSourceAddress), which only replaces an absent/nil source — the
// smpps path additionally treats a zero-length source_addr as needing the default.
func (c *Credential) ApplyDefaultSourceAddressSubmit(source []byte) []byte {
	if def, ok := c.DefaultSourceAddress(); ok && len(source) == 0 {
		return def
	}
	return source
}
