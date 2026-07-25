package config

import (
	"fmt"
	"os"
)

// Jasmin is a fully-parsed jasmin.cfg: every section the gateway understands,
// each parsed by its section loader with the legacy defaults and quirks. Absent
// sections yield their defaults, exactly as the Python config classes do, so a
// partial config file is valid.
type Jasmin struct {
	AMQP  AMQP
	Redis Redis

	SMPPServer SMPPServer
	HTTPAPI    HTTPAPI

	DLR        DLR
	SMListener SMListener

	ClientManagement PBServer
	Router           PBServer

	DeliverSMThrower Thrower
	DLRThrower       Thrower

	SMPPServerPB PBAdmin
	JCli         PBAdmin
	Interceptor  Interceptor

	SMPPServerPBClient PBClient
	InterceptorClient  PBClient
}

// LoadJasmin reads and parses a jasmin.cfg file into a Jasmin.
func LoadJasmin(path string) (*Jasmin, error) {
	handle, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer handle.Close()
	file, err := Parse(handle)
	if err != nil {
		return nil, err
	}
	return LoadJasminFile(file)
}

// LoadJasminFile aggregates every section loader over an already-parsed File.
// The first section that fails to parse aborts with its error.
func LoadJasminFile(file *File) (*Jasmin, error) {
	jasmin := &Jasmin{}
	var err error
	if jasmin.AMQP, err = LoadAMQP(file); err != nil {
		return nil, err
	}
	if jasmin.Redis, err = LoadRedis(file); err != nil {
		return nil, err
	}
	if jasmin.SMPPServer, err = LoadSMPPServer(file); err != nil {
		return nil, err
	}
	if jasmin.HTTPAPI, err = LoadHTTPAPI(file); err != nil {
		return nil, err
	}
	if jasmin.DLR, err = LoadDLR(file); err != nil {
		return nil, err
	}
	if jasmin.SMListener, err = LoadSMListener(file); err != nil {
		return nil, err
	}
	if jasmin.ClientManagement, err = LoadClientManagement(file); err != nil {
		return nil, err
	}
	if jasmin.Router, err = LoadRouter(file); err != nil {
		return nil, err
	}
	if jasmin.DeliverSMThrower, err = LoadDeliverSMThrower(file); err != nil {
		return nil, err
	}
	if jasmin.DLRThrower, err = LoadDLRThrower(file); err != nil {
		return nil, err
	}
	if jasmin.SMPPServerPB, err = LoadSMPPServerPB(file); err != nil {
		return nil, err
	}
	if jasmin.JCli, err = LoadJCli(file); err != nil {
		return nil, err
	}
	if jasmin.Interceptor, err = LoadInterceptor(file); err != nil {
		return nil, err
	}
	if jasmin.SMPPServerPBClient, err = LoadSMPPServerPBClient(file); err != nil {
		return nil, err
	}
	if jasmin.InterceptorClient, err = LoadInterceptorClient(file); err != nil {
		return nil, err
	}
	return jasmin, nil
}
