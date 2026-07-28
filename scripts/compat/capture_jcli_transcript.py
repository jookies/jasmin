#!/usr/bin/env python
"""Capture jCli transcripts from the frozen Python oracle.

Plan 013 Step 1. The Go console (`internal/app/jcli`) owes the *transcript* --
prompt bytes, line ordering, spacing, success/error text -- so the only honest
way to build it is to record what the real console emits and diff against that.

Each scenario is a whole session: the input lines are fed to a freshly built
`JCliProtocol` over a `twisted.test.proto_helpers.StringTransport`, exactly the
way the frozen test suite drives it (`tests/protocols/cli/test_jcli.py`), and
the bytes the protocol writes back after each line are recorded verbatim.
Mutations are part of the scenario, so a fixture is self-contained: replaying
the same inputs against the Go console must produce the same bytes.

Requires the frozen stack importable and a reachable AMQP broker
(SMPPClientManagerPB refuses to start without one):

    python3 -m venv .venv-oracle
    .venv-oracle/bin/pip install -r requirements.txt
    docker compose -f docker-compose.gateway.yml up -d rabbitmq
    JASMIN_AMQP_PORT=5673 .venv-oracle/bin/python \
        scripts/compat/capture_jcli_transcript.py

Output: one `<id>.jsonl` per scenario under spec/compatibility/fixtures/jcli/.
"""

import base64
import json
import os
import shutil
import sys
import tempfile

_HERE = os.path.dirname(os.path.abspath(__file__))
_REPO_ROOT = os.path.dirname(os.path.dirname(_HERE))
# The frozen `jasmin` package is the repo tree itself, not an installed dist:
# python puts this script's directory on sys.path, never the repo root.
sys.path.insert(0, _HERE)
sys.path.insert(1, _REPO_ROOT)

from twisted.cred import portal
from twisted.internet import defer, reactor, task
from twisted.test import proto_helpers

import jasmin
from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.protocols.cli.configs import JCliConfig
from jasmin.protocols.cli.factory import JCliFactory
from jasmin.protocols.smpp.configs import SMPPServerConfig
from jasmin.protocols.smpp.factory import SMPPServerFactory
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.routing.configs import RouterPBConfig
from jasmin.routing.router import RouterPB
from jasmin.tools.cred.checkers import RouterAuthChecker
from jasmin.tools.cred.portal import SmppsRealm

from jcli_scenarios import SCENARIOS

FIXTURE_DIR = os.path.join(
    os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
    'spec', 'compatibility', 'fixtures', 'jcli')

# Settling delay after each input line. The managers answer through Perspective
# Broker deferreds, so the write to the transport is not synchronous with
# dataReceived(). Too short and a fixture records an *empty* reply for a command
# that simply had not answered yet -- which then gets enshrined as contract, so
# err long. Raise it with JCLI_CAPTURE_SETTLE if a fixture looks truncated.
SETTLE = float(os.environ.get('JCLI_CAPTURE_SETTLE', '0.6'))


import logging.handlers  # noqa: E402  (after the sys.path bootstrap above)

# Where the currently-running capture puts log files. Set per scenario.
_LOG_DIR = tempfile.mkdtemp(prefix='jcli-oracle-log-')


def _writable_log_path(filename):
    """Rewrite a frozen-default log path into the capture's temp directory.

    Config objects are not the only thing that opens a log file: adding a
    connector makes the manager build per-connector and per-service loggers
    whose paths come from deep inside the frozen code (`service-smppclient.log`,
    `default-<cid>.log`). Chasing each one means a scenario dies halfway through
    and records a truncated transcript as if it were contract, so the redirect
    happens at the logging layer where every one of them passes.
    """
    path = str(filename)
    if path.startswith('/var/log/jasmin') or path.startswith('//var/log/jasmin'):
        return os.path.join(_LOG_DIR, os.path.basename(path))
    return filename


_ORIGINAL_FILE_HANDLER = logging.FileHandler


class _RedirectedFileHandler(_ORIGINAL_FILE_HANDLER):
    def __init__(self, filename, *args, **kwargs):
        # Explicit base call, not super(): the rotating handlers reach
        # logging.FileHandler.__init__ through this same patched name, so `self`
        # there is a TimedRotatingFileHandler, which zero-arg super() rejects.
        _ORIGINAL_FILE_HANDLER.__init__(
            self, _writable_log_path(filename), *args, **kwargs)


# Patching FileHandler alone is enough: every rotating handler in the stdlib
# reaches the filesystem through logging.FileHandler.__init__.
logging.FileHandler = _RedirectedFileHandler


def _redirect_logs(config, log_dir):
    """Point a jasmin config's logger at a writable directory.

    Every frozen config defaults `log_file` under /var/log/jasmin, which does
    not exist on a developer machine and is created by the package install.
    Capturing must not require root, and the log content is not part of any
    transcript, so each config gets its own file under a temp directory.
    """
    if hasattr(config, 'log_file'):
        config.log_file = os.path.join(
            log_dir, '%s.log' % type(config).__name__.lower())
    return config


class OracleSession:
    """One jCli session over a fake transport, with its own router state."""

    def __init__(self, store_path, log_dir):
        self.store_path = store_path
        self.log_dir = log_dir

    @defer.inlineCallbacks
    def start(self, authentication):
        amqp_config = _redirect_logs(AmqpConfig(), self.log_dir)
        amqp_config.reconnectOnConnectionLoss = False
        amqp_config.host = os.environ.get('JASMIN_AMQP_HOST', '127.0.0.1')
        amqp_config.port = int(os.environ.get('JASMIN_AMQP_PORT', '5672'))
        # The spec path defaults under /etc/jasmin, which only exists after a
        # package install; the file ships in the repo.
        amqp_config.spec = os.path.join(
            _REPO_ROOT, 'misc', 'config', 'resource', 'amqp0-9-1.xml')
        # Capture must not share a vhost with a running Go gateway: that stack
        # declares the `messaging` exchange durable and the frozen one declares
        # it transient, so the second declaration dies with PRECONDITION_FAILED
        # and every connector mutation silently fails mid-transcript.
        amqp_config.vhost = os.environ.get('JASMIN_AMQP_VHOST', '/')
        self.amqp_broker = AmqpFactory(amqp_config)
        self.amqp_broker.preConnect()
        self.amqp_client = reactor.connectTCP(
            amqp_config.host, amqp_config.port, self.amqp_broker)
        yield self.amqp_broker.getChannelReadyDeferred()

        router_config = _redirect_logs(RouterPBConfig(), self.log_dir)
        router_config.authentication = False
        router_config.store_path = self.store_path
        self.router_config = router_config
        self.router = RouterPB(router_config)

        client_config = _redirect_logs(SMPPClientPBConfig(), self.log_dir)
        client_config.authentication = False
        self.client_manager = SMPPClientManagerPB(client_config)
        yield self.client_manager.addAmqpBroker(self.amqp_broker)

        smpps_config = _redirect_logs(SMPPServerConfig(), self.log_dir)
        smpps_portal = portal.Portal(SmppsRealm(smpps_config.id, self.router))
        smpps_portal.registerChecker(RouterAuthChecker(self.router))
        self.smpps_factory = SMPPServerFactory(
            config=smpps_config, auth_portal=smpps_portal,
            RouterPB=self.router, SMPPClientManagerPB=self.client_manager)

        jcli_config = _redirect_logs(JCliConfig(), self.log_dir)
        jcli_config.authentication = authentication
        self.factory = JCliFactory(
            jcli_config, self.client_manager, self.router, self.smpps_factory)
        self.proto = self.factory.buildProtocol(('127.0.0.1', 0))
        self.transport = proto_helpers.StringTransport()
        self.proto.makeConnection(self.transport)

    @defer.inlineCallbacks
    def stop(self):
        try:
            self.proto.connectionLost(None)
        except Exception:
            pass
        yield self.router.cancelPersistenceTimer()
        yield self.amqp_client.disconnect()
        yield self.amqp_broker.disconnect()

    def drain(self):
        value = self.transport.value()
        self.transport.clear()
        return value

    @defer.inlineCallbacks
    def send(self, line):
        # A line ending in TAB is completion input: send it as typed, with no
        # RETURN, so the console answers with its completion behaviour.
        if line.endswith('\t'):
            self.proto.dataReceived(line.encode('ascii'))
        else:
            self.proto.dataReceived(('%s\r\n' % line).encode('ascii'))
        yield task.deferLater(reactor, SETTLE, lambda: None)


# Interceptor scripts are validated at add time: the frozen manager opens the
# path and compiles it, so a fixture cannot reference a file that is not there.
# A fixed path keeps the transcript reproducible across machines.
INTERCEPTOR_SCRIPT = '/tmp/jasmin-oracle-interceptor.py'


def _write_interceptor_script():
    with open(INTERCEPTOR_SCRIPT, 'w') as handle:
        handle.write('routable = routable\n')


@defer.inlineCallbacks
def capture(scenario):
    global _LOG_DIR
    store_path = tempfile.mkdtemp(prefix='jcli-oracle-store-')
    log_dir = tempfile.mkdtemp(prefix='jcli-oracle-log-')
    _LOG_DIR = log_dir
    session = OracleSession(store_path, log_dir)
    yield session.start(scenario.get('authentication', True))

    steps = [{'input': None, 'output': session.drain()}]  # the greeting
    for line in scenario['inputs']:
        line = line.replace('{SCRIPT}', INTERCEPTOR_SCRIPT)
        yield session.send(line)
        steps.append({'input': line, 'output': session.drain()})

    yield session.stop()
    shutil.rmtree(store_path, ignore_errors=True)
    shutil.rmtree(log_dir, ignore_errors=True)

    defer.returnValue({
        'id': scenario['id'],
        'title': scenario['title'],
        'authentication': scenario.get('authentication', True),
        'release': jasmin.get_release(),
        'steps': steps,
    })


def write_fixture(result):
    os.makedirs(FIXTURE_DIR, exist_ok=True)
    path = os.path.join(FIXTURE_DIR, '%s.jsonl' % result['id'])
    with open(path, 'w') as handle:
        handle.write(json.dumps({
            'fixture': result['id'],
            'title': result['title'],
            'authentication': result['authentication'],
            'release': result['release'],
            'oracle': 'jasmin.protocols.cli via StringTransport',
        }, sort_keys=True) + '\n')
        for index, step in enumerate(result['steps']):
            handle.write(json.dumps({
                'step': index,
                'input': step['input'],
                'output_b64': base64.b64encode(step['output']).decode('ascii'),
            }, sort_keys=True) + '\n')
    return path


@defer.inlineCallbacks
def main(_reactor, selected):
    _write_interceptor_script()
    scenarios = [s for s in SCENARIOS if not selected or s['id'] in selected]
    if not scenarios:
        sys.exit('no scenario matched %s' % (selected,))
    for scenario in scenarios:
        result = yield capture(scenario)
        path = write_fixture(result)
        total = sum(len(step['output']) for step in result['steps'])
        print('%-10s %-46s %5d bytes  %s' % (
            result['id'], result['title'], total, os.path.relpath(path)))


if __name__ == '__main__':

    task.react(main, (set(sys.argv[1:]),))
