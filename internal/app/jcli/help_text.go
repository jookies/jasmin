package jcli

// Per-command help text, captured verbatim from the frozen console
// (spec/compatibility/fixtures/jcli/J-002-help-commands.jsonl).
//
// The bodies are optparse's own rendering: the usage line, the two-space option
// column, and the continuation lines it emits when an option string is too long
// to sit beside its help. Reimplementing optparse's layout rules in Go would be
// a guess re-derived on every edit; these strings are the contract itself.
//
// Regenerate by re-running the capture script and scripts/compat/gen_jcli_help.py.
var commandHelp = map[string]string{
	"persist": `Persist current configuration profile to disk in PROFILE
Usage: persist [options] 

Options:
  -p PROFILE, --profile=PROFILE
                        Configuration profile, default: jcli-prod
`,
	"load": `Load configuration PROFILE profile from disk
Usage: load [options] 

Options:
  -p PROFILE, --profile=PROFILE
                        Configuration profile, default: jcli-prod
`,
	"user": `User management
Usage: user [options] 

Options:
  -l, --list            List all users or a group users when provided with GID
  -a, --add             Add user
  -e UID, --enable=UID  Enable user
  -d UID, --disable=UID
                        Disable user
  -u UID, --update=UID  Update user using it's UID
  -r UID, --remove=UID  Remove user using it's UID
  -s UID, --show=UID    Show user using it's UID
  --smpp-unbind=UID     Unbind user from smpp server using it's UID
  --smpp-ban=UID        Unbind and ban user from smpp server using it's UID
`,
	"group": `Group management
Usage: group [options] 

Options:
  -l, --list            List groups
  -a, --add             Add group
  -e GID, --enable=GID  Enable group
  -d GID, --disable=GID
                        Disable group
  -r GID, --remove=GID  Remove group using it's GID
`,
	"filter": `Filter management
Usage: filter [options] 

Options:
  -l, --list            List filters
  -a, --add             Add filter
  -r FID, --remove=FID  Remove filter using it's FID
  -s FID, --show=FID    Show filter using it's FID
`,
	"mointerceptor": `MO Interceptor management
Usage: mointerceptor [options] 

Options:
  -l, --list            List MO interceptors
  -a, --add             Add a new MO interceptor
  -r ORDER, --remove=ORDER
                        Remove MO interceptor using it's ORDER
  -s ORDER, --show=ORDER
                        Show MO interceptor using it's ORDER
  -f, --flush           Flush MO interception table
`,
	"mtinterceptor": `MT Interceptor management
Usage: mtinterceptor [options] 

Options:
  -l, --list            List MT interceptors
  -a, --add             Add a new MT interceptor
  -r ORDER, --remove=ORDER
                        Remove MT interceptor using it's ORDER
  -s ORDER, --show=ORDER
                        Show MT interceptor using it's ORDER
  -f, --flush           Flush MT interception table
`,
	"morouter": `MO Router management
Usage: morouter [options] 

Options:
  -l, --list            List MO routes
  -a, --add             Add a new MO route
  -r ORDER, --remove=ORDER
                        Remove MO route using it's ORDER
  -s ORDER, --show=ORDER
                        Show MO route using it's ORDER
  -f, --flush           Flush MO routing table
`,
	"mtrouter": `MT Router management
Usage: mtrouter [options] 

Options:
  -l, --list            List MT routes
  -a, --add             Add a new MT route
  -r ORDER, --remove=ORDER
                        Remove MT route using it's ORDER
  -s ORDER, --show=ORDER
                        Show MT route using it's ORDER
  -f, --flush           Flush MT routing table
`,
	"smppccm": `SMPP connector management
Usage: smppccm [options] 

Options:
  -l, --list            List SMPP connectors
  -a, --add             Add SMPP connector
  -u CID, --update=CID  Update SMPP connector configuration using it's CID
  -r CID, --remove=CID  Remove SMPP connector using it's CID
  -s CID, --show=CID    Show SMPP connector using it's CID
  -1 CID, --start=CID   Start SMPP connector using it's CID
  -0 CID, --stop=CID    Stop SMPP connector using it's CID
`,
	"httpccm": `HTTP client connector management
Usage: httpccm [options] 

Options:
  -l, --list            List HTTP client connectors
  -a, --add             Add a new HTTP client connector
  -r CID, --remove=CID  Remove HTTP client connector using it's CID
  -s CID, --show=CID    Show HTTP client connector using it's CID
`,
	// Fork-local: no legacy counterpart, so this text is ours rather than
	// optparse's recorded output. Kept in the same shape so it does not read as
	// a different kind of command.
	"msgconsumer": `Message read-token management
Usage: msgconsumer [options] 

Options:
  -l, --list            List message read tokens
  -a, --add             Add a new read token (the secret is printed once)
  -r CID, --remove=CID  Remove read token using it's CID
  -s CID, --show=CID    Show read token using it's CID
  -x CID, --revoke=CID  Revoke read token using it's CID
  -e CID, --enable=CID  Re-enable a revoked read token using it's CID
`,
	"stats": `Stats management
Usage: stats [options] 

Options:
  --user=UID   Show user stats using it's UID
  --users      Show all users stats
  --smppc=CID  Show smpp connector stats using it's CID
  --smppcs     Show all smpp connectors stats
  --httpapi    Show HTTP API stats
  --smppsapi   Show SMPP Server API stats
`,
	"quit": `Disconnect from console`,
	"help": `List available commands with "help" or detailed help with "help cmd".`,
}
