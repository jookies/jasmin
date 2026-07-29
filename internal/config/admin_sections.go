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

// PBAdmin is the shared core of the Perspective Broker admin sections: the
// admin listener bind/port and its credentials. client-management and router
// extend it with a store/pickle/persistence tail (PBServer); smpp-server-pb,
// jcli and interceptor use it directly (see pb_sections.go).
type PBAdmin struct {
	Bind           string
	Port           int
	Authentication bool
	AdminUsername  string
	AdminPassword  []byte // md5 digest, hex-decoded from the config value
}

// BindAddr renders the admin listener host:port.
func (p PBAdmin) BindAddr() string {
	return fmt.Sprintf("%s:%d", p.Bind, p.Port)
}

// loadPBAdmin parses the five core admin-listener fields. admin_password is
// hex-decoded to match binascii.unhexlify; an odd-length or non-hex value is an
// error, as the legacy unhexlify raises.
func loadPBAdmin(file *File, section, defaultBind string, defaultPort int, defaultAdmin, defaultPWHex string) (PBAdmin, error) {
	admin := PBAdmin{
		Bind:          file.Get(section, "bind", defaultBind),
		AdminUsername: file.Get(section, "admin_username", defaultAdmin),
	}
	var err error
	if admin.Port, err = file.GetInt(section, "port", defaultPort); err != nil {
		return PBAdmin{}, err
	}
	if admin.Authentication, err = file.GetBool(section, "authentication", true); err != nil {
		return PBAdmin{}, err
	}
	pwHex := file.Get(section, "admin_password", defaultPWHex)
	admin.AdminPassword, err = hex.DecodeString(pwHex)
	if err != nil {
		return PBAdmin{}, fmt.Errorf("config: [%s] admin_password %q is not valid hex", section, pwHex)
	}
	return admin, nil
}

// PBServer is the 'client-management' and 'router' shape: the PBAdmin core plus
// the pickle store and (router-only) persistence timer.
type PBServer struct {
	PBAdmin
	StorePath      string
	PickleProtocol int
	Log            LogConfig

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
	admin, err := loadPBAdmin(file, section, "0.0.0.0", defaultPort, defaultAdmin, defaultPWHex)
	if err != nil {
		return PBServer{}, err
	}
	server := PBServer{
		PBAdmin:   admin,
		StorePath: file.Get(section, "store_path", ""),
	}
	switch section {
	case "router":
		server.Log = loadLogConfig(file, section, "router.log", "W6")
	default:
		server.Log = loadLogConfig(file, section, "smppclient-manager.log", "W6")
	}
	if server.PickleProtocol, err = file.GetInt(section, "pickle_protocol", 2); err != nil {
		return PBServer{}, err
	}
	if withPersistence {
		if server.PersistenceTimerSecs, err = file.GetInt(section, "persistence_timer_secs", 60); err != nil {
			return PBServer{}, err
		}
	}
	return server, nil
}
