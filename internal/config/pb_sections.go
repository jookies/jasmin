package config

import "fmt"

// Default admin_password digests for the lean PB admin sections, hex-decoded
// like the legacy binascii.unhexlify default.
const (
	defaultSMPPServerPBAdminPWHex = "e97ab122faa16beea8682d84f3d2eea4"
	defaultJCliAdminPWHex         = "79e9b0aa3f3e7c53e916f7ac47439bcb"
	defaultInterceptorAdminPWHex  = "dd8b84cdb60655fed3b9b2d668c5bd9e"
)

// LoadSMPPServerPB parses the 'smpp-server-pb' section (SMPPServerPBConfig): the
// SMPP server's management PB listener. It is the bare PBAdmin shape — no store,
// pickle or persistence.
func LoadSMPPServerPB(file *File) (PBAdmin, error) {
	return loadPBAdmin(file, "smpp-server-pb", "0.0.0.0", 14000, "smppsadmin", defaultSMPPServerPBAdminPWHex)
}

// LoadJCli parses the 'jcli' section (JCliConfig): the jCli management console
// listener. Note its bind default is 127.0.0.1 (loopback), unlike the other PB
// admin sections which default to 0.0.0.0.
func LoadJCli(file *File) (PBAdmin, error) {
	return loadPBAdmin(file, "jcli", "127.0.0.1", 8990, "jcliadmin", defaultJCliAdminPWHex)
}

// Interceptor is the 'interceptor' section (InterceptorPBConfig): the PBAdmin
// core plus the slow-script logging threshold.
type Interceptor struct {
	PBAdmin
	LogSlowScript int
}

// LoadInterceptor parses the 'interceptor' section.
func LoadInterceptor(file *File) (Interceptor, error) {
	admin, err := loadPBAdmin(file, "interceptor", "0.0.0.0", 8987, "iadmin", defaultInterceptorAdminPWHex)
	if err != nil {
		return Interceptor{}, err
	}
	interceptor := Interceptor{PBAdmin: admin}
	if interceptor.LogSlowScript, err = file.GetInt("interceptor", "log_slow_script", 1); err != nil {
		return Interceptor{}, err
	}
	return interceptor, nil
}

// PBClient is the shared shape of the Perspective Broker client sections
// ('smpp-server-pb-client', 'interceptor-client'): the endpoint and plaintext
// credentials a component uses to dial its PB server. Unlike the admin sections
// the password is a plain string here, not a hex digest.
type PBClient struct {
	Host     string
	Port     int
	Username string
	Password string
}

// Addr renders the PB server host:port the client dials.
func (c PBClient) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func loadPBClient(file *File, section, defaultHost string, defaultPort int, defaultUser, defaultPass string) (PBClient, error) {
	client := PBClient{
		Host:     file.Get(section, "host", defaultHost),
		Username: file.Get(section, "username", defaultUser),
		Password: file.Get(section, "password", defaultPass),
	}
	var err error
	if client.Port, err = file.GetInt(section, "port", defaultPort); err != nil {
		return PBClient{}, err
	}
	return client, nil
}

// LoadSMPPServerPBClient parses the 'smpp-server-pb-client' section
// (SMPPServerPBClientConfig).
func LoadSMPPServerPBClient(file *File) (PBClient, error) {
	return loadPBClient(file, "smpp-server-pb-client", "127.0.0.1", 14000, "smppsadmin", "smppspwd")
}

// LoadInterceptorClient parses the 'interceptor-client' section
// (InterceptorPBClientConfig).
func LoadInterceptorClient(file *File) (PBClient, error) {
	return loadPBClient(file, "interceptor-client", "127.0.0.1", 8987, "iadmin", "ipwd")
}
