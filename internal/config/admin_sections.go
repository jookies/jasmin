package config

import (
	"encoding/hex"
	"fmt"
)

// The default admin_password digests (md5 of the default password), hex-decoded
// like the legacy binascii.unhexlify default.
const (
	defaultClientMgmtAdminPWHex = "e1c5136acafb7016bc965597c992eb82"
	defaultRouterAdminPWHex     = "82a606ca5a0deea2b5777756788af5c8"
)

// PBServer is the parsed shape shared by the 'client-management' and 'router'
// Perspective Broker admin sections: the admin listener and credentials.
type PBServer struct {
	StorePath      string
	Bind           string
	Port           int
	Authentication bool
	AdminUsername  string
	AdminPassword  []byte // md5 digest, hex-decoded from the config value
	PickleProtocol int

	// PersistenceTimerSecs is router-only (0 for client-management).
	PersistenceTimerSecs int
}

// LoadClientManagement parses the 'client-management' section (SMPPClientPBConfig).
func LoadClientManagement(file *File) (PBServer, error) {
	return loadPBSection(file, "client-management", 8989, "cmadmin", defaultClientMgmtAdminPWHex, false)
}

// LoadRouter parses the 'router' section (RouterPBConfig), including the
// persistence timer.
func LoadRouter(file *File) (PBServer, error) {
	return loadPBSection(file, "router", 8988, "radmin", defaultRouterAdminPWHex, true)
}

func loadPBSection(file *File, section string, defaultPort int, defaultAdmin, defaultPWHex string, withPersistence bool) (PBServer, error) {
	server := PBServer{
		StorePath:     file.Get(section, "store_path", ""),
		Bind:          file.Get(section, "bind", "0.0.0.0"),
		AdminUsername: file.Get(section, "admin_username", defaultAdmin),
	}
	var err error
	if server.Port, err = file.GetInt(section, "port", defaultPort); err != nil {
		return PBServer{}, err
	}
	if server.Authentication, err = file.GetBool(section, "authentication", true); err != nil {
		return PBServer{}, err
	}
	if server.PickleProtocol, err = file.GetInt(section, "pickle_protocol", 2); err != nil {
		return PBServer{}, err
	}
	// admin_password is hex-decoded (binascii.unhexlify); an odd-length or
	// non-hex value is an error, as the legacy unhexlify raises.
	pwHex := file.Get(section, "admin_password", defaultPWHex)
	server.AdminPassword, err = hex.DecodeString(pwHex)
	if err != nil {
		return PBServer{}, fmt.Errorf("config: [%s] admin_password %q is not valid hex", section, pwHex)
	}
	if withPersistence {
		if server.PersistenceTimerSecs, err = file.GetInt(section, "persistence_timer_secs", 60); err != nil {
			return PBServer{}, err
		}
	}
	return server, nil
}

// BindAddr renders the admin listener host:port.
func (p PBServer) BindAddr() string {
	return fmt.Sprintf("%s:%d", p.Bind, p.Port)
}
