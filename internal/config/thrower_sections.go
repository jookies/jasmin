package config

// Thrower is the parsed shape shared by the 'deliversm-thrower' and
// 'dlr-thrower' sections (jasmin/routing/configs.py): the retry/timeout policy
// a thrower applies when delivering deliver_sm / DLR payloads to user
// endpoints.
type Thrower struct {
	TimeoutSecs    int // http_timeout
	RetryDelaySecs int // retry_delay
	MaxRetries     int // max_retries
	Log            LogConfig

	// DLRPDU is the receipt PDU the DLR thrower emits over SMPP; it is only
	// read for 'dlr-thrower' ("" for deliversm-thrower).
	DLRPDU string
}

// LoadDeliverSMThrower parses the 'deliversm-thrower' section
// (deliverSmThrowerConfig).
func LoadDeliverSMThrower(file *File) (Thrower, error) {
	return loadThrower(file, "deliversm-thrower", false)
}

// LoadDLRThrower parses the 'dlr-thrower' section (DLRThrowerConfig), including
// the dlr_pdu selector.
func LoadDLRThrower(file *File) (Thrower, error) {
	return loadThrower(file, "dlr-thrower", true)
}

func loadThrower(file *File, section string, withDLRPDU bool) (Thrower, error) {
	thrower := Thrower{Log: loadLogConfig(file, section, section+".log", "W6")}
	var err error
	if thrower.TimeoutSecs, err = file.GetInt(section, "http_timeout", 30); err != nil {
		return Thrower{}, err
	}
	if thrower.RetryDelaySecs, err = file.GetInt(section, "retry_delay", 30); err != nil {
		return Thrower{}, err
	}
	if thrower.MaxRetries, err = file.GetInt(section, "max_retries", 3); err != nil {
		return Thrower{}, err
	}
	if withDLRPDU {
		thrower.DLRPDU = file.Get(section, "dlr_pdu", "deliver_sm")
	}
	return thrower, nil
}
