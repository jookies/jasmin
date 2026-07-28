"""Turn the captured `help <cmd>` transcripts into a Go source file of literals."""
import base64, json

steps = []
for line in open('spec/compatibility/fixtures/jcli/J-002-help-commands.jsonl'):
    rec = json.loads(line)
    if 'step' in rec:
        steps.append(rec)

out = {}
for rec in steps:
    if not rec['input'] or not rec['input'].startswith('help '):
        continue
    cmd = rec['input'].split(' ', 1)[1]
    body = base64.b64decode(rec['output_b64']).decode('ascii')
    # strip the echoed command + its line break, and the trailing prompt
    prefix = rec['input'] + '\r\r\r\n'
    assert body.startswith(prefix), (cmd, body[:40])
    body = body[len(prefix):]
    assert body.endswith('jcli : '), (cmd, body[-20:])
    body = body[:-len('jcli : ')]
    # the reply ends with one line break contributed by sendData's nextLine
    assert body.endswith('\r\r\r\n')
    body = body[:-len('\r\r\r\n')]
    out[cmd] = body.replace('\r\r\r\n', '\n')

order = ['persist','load','user','group','filter','mointerceptor','mtinterceptor',
         'morouter','mtrouter','smppccm','httpccm','stats','quit','help']

def golit(s):
    if '`' in s:
        return json.dumps(s)
    return '`' + s + '`'

with open('internal/app/jcli/help_text.go', 'w') as fh:
    fh.write('''package jcli

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
''')
    for cmd in order:
        if cmd in out:
            fh.write('\t%s: %s,\n' % (json.dumps(cmd), golit(out[cmd])))
    fh.write('}\n')
print('wrote internal/app/jcli/help_text.go with', len(out), 'entries')
