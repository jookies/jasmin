import mock
from twisted.internet import defer, reactor
from test_jcli import jCliWithoutAuthTestCases
from jasmin.protocols.smpp.test.smsc_simulator import *

@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred

class SmppccmTestCases(jCliWithoutAuthTestCases):
    # Wait delay for
    wait = 0.6

    def add_connector(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'smppccm -a', 'expect': r'Adding a new connector\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)

            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True

        if not sessionTerminated:
            commands.append({'command': 'ok',
                             'expect': r'Successfully added connector \[',
                             'wait': self.wait})

        return self._test(finalPrompt, commands)

class LastClientFactory(Factory):
    lastClient = None
    def buildProtocol(self, addr):
        self.lastClient = Factory.buildProtocol(self, addr)
        return self.lastClient

class HappySMSCTestCase(SmppccmTestCases):
    protocol = HappySMSCRecorder

    @defer.inlineCallbacks
    def setUp(self):
        yield SmppccmTestCases.setUp(self)

        self.smsc_f = LastClientFactory()
        self.smsc_f.protocol = self.protocol
        self.SMSCPort = reactor.listenTCP(0, self.smsc_f)

    @defer.inlineCallbacks
    def tearDown(self):
        SmppccmTestCases.tearDown(self)

        yield self.SMSCPort.stopListening()

class BasicTestCases(HappySMSCTestCase):

    def test_list(self):
        commands = [{'command': 'smppccm -l', 'expect': r'Total connectors: 0'}]
        return self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'cid operator_1'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set at least connector id \(cid\) before saving !', 'wait': self.wait}]
        yield self.add_connector(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_add_invalid_configkey(self):
        extraCommands = [{'command': 'cid operator_2'}, {'command': 'anykey anyvalue', 'expect': r'Unknown SMPPClientConfig key: anykey'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_invalid_configkey_value(self):
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'port 22e'},
                         {'command': 'ok', 'expect': r'Error\: port must be an integer', 'wait': self.wait}]
        yield self.add_connector(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_add_long_username(self):
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'username 1234567890123456'},
                         {'command': 'ok', 'expect': r'Error\: username is longer than allowed size \(15\)', 'wait': self.wait}]
        yield self.add_connector(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_add_long_password(self):
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'password 123456789'},
                         {'command': 'ok', 'expect': r'Error\: password is longer than allowed size \(8\)', 'wait': self.wait}]
        yield self.add_connector(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_cancel_add(self):
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'ko'}, ]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_add_and_list(self):
        extraCommands = [{'command': 'cid operator_4'}]
        yield self.add_connector('jcli : ', extraCommands)

        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_4                          stopped None             0      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'cid operator_5'},
                         {'command': 'ko'}, ]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -l', 'expect': r'Total connectors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_and_show(self):
        cid = 'operator_6'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector('jcli : ', extraCommands)

        expectedList = ['ripf 0',
                        'con_fail_delay 10',
                        'dlr_expiry 86400', 'coding 0',
                        'logrotate midnight',
                        'submit_throughput 1',
                        'elink_interval 30',
                        'bind_to 30',
                        'port 2775',
                        'con_fail_retry yes',
                        'password password',
                        'src_addr None',
                        'bind_npi 0',
                        'addr_range None',
                        'dst_ton 1',
                        'res_to 120',
                        'def_msg_id 0',
                        'priority 0',
                        'con_loss_retry yes',
                        'username smppclient',
                        'dst_npi 1',
                        'validity None',
                        'requeue_delay 120',
                        'host 127.0.0.1',
                        'src_npi 1',
                        'trx_to 300',
                        'logfile .*var/log/jasmin/default-%s.log' % cid,
                        'systype ',
                        'ssl no',
                        'cid %s' % cid,
                        'loglevel 20',
                        'bind transceiver',
                        'proto_id None',
                        'dlr_msgid 0',
                        'con_loss_delay 10',
                        'bind_ton 0',
                        'pdu_red_to 10',
                        'src_ton 2']
        commands = [{'command': 'smppccm -s %s' % cid, 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_show_invalid_cid(self):
        commands = [{'command': 'smppccm -s invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_cid(self):
        cid = 'operator_7'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u operator_7', 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'cid 2222', 'expect': r'Connector id can not be modified !'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_update(self):
        cid = 'operator_8'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u operator_8', 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'port 2222'},
                    {'command': 'ok', 'expect': r'Successfully updated connector \[%s\]' % cid}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_and_show(self):
        cid = 'operator_9'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u %s' % cid, 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'port 122223'},
                    {'command': 'ok', 'expect': r'Successfully updated connector \[%s\]' % cid}]
        yield self._test(r'jcli : ', commands)

        expectedList = ['ripf 0',
                        'con_fail_delay 10',
                        'dlr_expiry 86400', 'coding 0',
                        'logrotate midnight',
                        'submit_throughput 1',
                        'elink_interval 30',
                        'bind_to 30',
                        'port 122223',
                        'con_fail_retry yes',
                        'password password',
                        'src_addr None',
                        'bind_npi 0',
                        'addr_range None',
                        'dst_ton 1',
                        'res_to 120',
                        'def_msg_id 0',
                        'priority 0',
                        'con_loss_retry yes',
                        'username smppclient',
                        'dst_npi 1',
                        'validity None',
                        'requeue_delay 120',
                        'host 127.0.0.1',
                        'src_npi 1',
                        'trx_to 300',
                        'logfile .*var/log/jasmin/default-%s.log' % cid,
                        'systype ',
                        'ssl no',
                        'cid %s' % cid,
                        'loglevel 20',
                        'bind transceiver',
                        'proto_id None',
                        'dlr_msgid 0',
                        'con_loss_delay 10',
                        'bind_ton 0',
                        'pdu_red_to 10',
                        'src_ton 2']
        commands = [{'command': 'smppccm -s %s' % cid, 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_invalid_cid(self):
        commands = [{'command': 'smppccm -r invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove(self):
        cid = 'operator_10'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -r %s' % cid, 'expect': r'Successfully removed connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_started_connector(self):
        # Add
        cid = 'operator_11'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'con_fail_retry 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid,
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'smppccm -r %s' % cid,
                     'expect': r'Successfully removed connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_and_list(self):
        # Add
        cid = 'operator_12'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_12                         stopped None             0      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'smppccm -r %s' % cid, 'expect': r'Successfully removed connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'smppccm -l', 'expect': r'Total connectors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_start(self):
        # Add
        cid = 'operator_13'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'con_fail_retry no', 'wait': 0.6}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid,
                    'expect': r'Successfully started connector id\:%s' % cid,
                    'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_start_and_list(self):
        # Add
        cid = 'operator_14'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'port %s' % self.SMSCPort.getHost().port}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid,
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 1}]
        yield self._test(r'jcli : ', commands)

        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_14                         started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid,
                     'expect': r'Successfully stopped connector id',
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_start_invalid_cid(self):
        commands = [{'command': 'smppccm -1 invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_stop(self):
        # Add
        cid = 'operator_15'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'port %s' % self.SMSCPort.getHost().port}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 'expect': r'Failed stopping connector, check log for details'}]
        yield self._test(r'jcli : ', commands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid,
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid,
                     'expect': r'Successfully stopped connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_stop_invalid_cid(self):
        commands = [{'command': 'smppccm -0 invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_start_stop_and_list(self):
        # Add
        cid = 'operator_16'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'port %s' % self.SMSCPort.getHost().port}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid,
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid,
                     'expect': r'Successfully stopped connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_16                         stopped NONE             1      1    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

class ParameterValuesTestCases(SmppccmTestCases):

    @defer.inlineCallbacks
    def test_add_connector(self):
        """Will test for value validation for a set of command keys with smppccm -a
           everything is built through the assert_battery"""

        assert_battery = [
                  {'key': 'src_ton',   'default_value': '2', 'set_value': '3',          'isValid': True},
                  {'key': 'src_ton',   'default_value': '2', 'set_value': '300',        'isValid': False},
                  {'key': 'src_ton',   'default_value': '2', 'set_value': '-1',         'isValid': False},
                  {'key': 'src_ton',   'default_value': '2', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '1',          'isValid': True},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '-1',         'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '300',        'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '6',          'isValid': True},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '1',          'isValid': True},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '300',        'isValid': False},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '6',          'isValid': True},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': '3',          'isValid': True},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': '300',        'isValid': False},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': '-1',         'isValid': False},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '1',          'isValid': True},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '-1',         'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '300',        'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '6',          'isValid': True},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '1',          'isValid': True},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '300',        'isValid': False},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '6',          'isValid': True},
                  {'key': 'priority',  'default_value': '0', 'set_value': '0',          'isValid': True},
                  {'key': 'priority',  'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'priority',  'default_value': '0', 'set_value': 'LEVEL_1',    'isValid': False},
                  {'key': 'priority',  'default_value': '0', 'set_value': '300',        'isValid': False},
                  {'key': 'priority',  'default_value': '0', 'set_value': '3',          'isValid': True},
                  {'key': 'ripf',      'default_value': '0', 'set_value': '0',          'isValid': True},
                  {'key': 'ripf',      'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'ripf',      'default_value': '0', 'set_value': 'xx',         'isValid': False},
                  {'key': 'ripf',      'default_value': '0', 'set_value': 'REPLCACE',   'isValid': False},
                  {'key': 'ripf',      'default_value': '0', 'set_value': '1',          'isValid': True},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'yes', 'isValid': True},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': '1',   'isValid': False},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'xx',  'isValid': False},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'NON', 'isValid': False},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'no',  'isValid': True},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'yes', 'isValid': True},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': '1',   'isValid': False},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'xx',  'isValid': False},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'NON', 'isValid': False},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'no',  'isValid': True},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'yes',       'isValid': True},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': '1',         'isValid': False},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'xx',        'isValid': False},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'NON',       'isValid': False},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'no',        'isValid': True},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': '10',        'isValid': True},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': '1',         'isValid': False},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': 'xx',        'isValid': False},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': 'DEBUG',     'isValid': False},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': '50',        'isValid': True},
                 ]
        cid = 0

        for value in assert_battery:
            if value['isValid']:
                add_expect = '%s %s> ' % (value['key'], value['set_value'])
                show_expect = '%s %s' % (value['key'], value['set_value'])
            else:
                add_expect = 'Unknown value for key %s' % value['key']
                show_expect = '%s %s' % (value['key'], value['default_value'])

            # Add and assertions
            extraCommands = [{'command': 'cid operator_%s' % cid},
                             {'command': '%s %s' % (value['key'], value['set_value']), 'expect': add_expect},
                             {'command': 'ok', 'wait': 0.8}]
            yield self.add_connector(r'jcli : ', extraCommands)

            # Assert value were taken (or not, depending if it's valid)
            commands = [{'command': 'smppccm -s operator_%s' % cid, 'expect': show_expect}]
            yield self._test(r'jcli : ', commands)

            cid+= 1

    @defer.inlineCallbacks
    def test_update_connector(self):
        """Will test for value validation for a set of command keys with smppccm -u
           everything is built through the assert_battery"""

        assert_battery = [
                  {'key': 'src_ton',   'default_value': '2', 'set_value': '3',          'isValid': True},
                  {'key': 'src_ton',   'default_value': '2', 'set_value': '300',        'isValid': False},
                  {'key': 'src_ton',   'default_value': '2', 'set_value': '-1',         'isValid': False},
                  {'key': 'src_ton',   'default_value': '2', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '1',          'isValid': True},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '-1',         'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '300',        'isValid': False},
                  {'key': 'dst_ton',   'default_value': '1', 'set_value': '6',          'isValid': True},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '1',          'isValid': True},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '300',        'isValid': False},
                  {'key': 'bind_ton',  'default_value': '0', 'set_value': '6',          'isValid': True},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': '3',          'isValid': True},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': '300',        'isValid': False},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': '-1',         'isValid': False},
                  {'key': 'src_npi',   'default_value': '1', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '1',          'isValid': True},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '-1',         'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '300',        'isValid': False},
                  {'key': 'dst_npi',   'default_value': '1', 'set_value': '6',          'isValid': True},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '1',          'isValid': True},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': 'NATIONAL',   'isValid': False},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '300',        'isValid': False},
                  {'key': 'bind_npi',  'default_value': '0', 'set_value': '6',          'isValid': True},
                  {'key': 'priority',  'default_value': '0', 'set_value': '0',          'isValid': True},
                  {'key': 'priority',  'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'priority',  'default_value': '0', 'set_value': 'LEVEL_1',    'isValid': False},
                  {'key': 'priority',  'default_value': '0', 'set_value': '300',        'isValid': False},
                  {'key': 'priority',  'default_value': '0', 'set_value': '3',          'isValid': True},
                  {'key': 'ripf',      'default_value': '0', 'set_value': '0',          'isValid': True},
                  {'key': 'ripf',      'default_value': '0', 'set_value': '-1',         'isValid': False},
                  {'key': 'ripf',      'default_value': '0', 'set_value': 'xx',         'isValid': False},
                  {'key': 'ripf',      'default_value': '0', 'set_value': 'REPLCACE',   'isValid': False},
                  {'key': 'ripf',      'default_value': '0', 'set_value': '1',          'isValid': True},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'yes', 'isValid': True},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': '1',   'isValid': False},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'xx',  'isValid': False},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'NON', 'isValid': False},
                  {'key': 'con_fail_retry', 'default_value': 'yes', 'set_value': 'no',  'isValid': True},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'yes', 'isValid': True},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': '1',   'isValid': False},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'xx',  'isValid': False},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'NON', 'isValid': False},
                  {'key': 'con_loss_retry', 'default_value': 'yes', 'set_value': 'no',  'isValid': True},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'yes',       'isValid': True},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': '1',         'isValid': False},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'xx',        'isValid': False},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'NON',       'isValid': False},
                  {'key': 'ssl',       'default_value': 'no', 'set_value': 'no',        'isValid': True},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': '10',        'isValid': True},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': '1',         'isValid': False},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': 'xx',        'isValid': False},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': 'DEBUG',     'isValid': False},
                  {'key': 'loglevel',  'default_value': '20', 'set_value': '50',        'isValid': True},
                 ]
        cid = 0

        for value in assert_battery:
            if value['isValid']:
                add_expect = '%s %s> ' % (value['key'], value['set_value'])
                show_expect = '%s %s' % (value['key'], value['set_value'])
            else:
                add_expect = 'Unknown value for key %s' % value['key']
                show_expect = '%s %s' % (value['key'], value['default_value'])

            # Add connector with defaults
            extraCommands = [{'command': 'cid operator_%s' % cid}]
            yield self.add_connector(r'jcli : ', extraCommands)

            # Update and assert
            commands = [{'command': 'smppccm -u operator_%s' % cid},
                        {'command': 'password password'},
                        {'command': '%s %s' % (value['key'], value['set_value']), 'expect': add_expect},
                        {'command': 'ok', 'wait': 0.8}]
            yield self._test(r'jcli : ', commands)

            # Assert value were taken (or not, depending if it's valid)
            commands = [{'command': 'smppccm -s operator_%s' % cid, 'expect': show_expect}]
            yield self._test(r'jcli : ', commands)

            cid+= 1

class SMSCTestCases(HappySMSCTestCase):

    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)

        # A connector list to be stopped on tearDown
        self.startedConnectors = []

    @defer.inlineCallbacks
    def tearDown(self):
        # Stop all started connectors
        for startedConnector in self.startedConnectors:
            yield self.stop_connector(startedConnector)

        yield HappySMSCTestCase.tearDown(self)

    @defer.inlineCallbacks
    def start_connector(self, cid, finalPrompt = r'jcli : ', wait = 0.6, expect = None):
        commands = [{'command': 'smppccm -1 %s' % cid, 'wait': wait, 'expect': expect}]
        yield self._test(finalPrompt, commands)

        # Add cid to the connector list to be stopped in tearDown
        self.startedConnectors.append(cid)

    @defer.inlineCallbacks
    def stop_connector(self, cid, finalPrompt = r'jcli : ', wait = 0.6, expect = None):
        commands = [{'command': 'smppccm -0 %s' % cid, 'wait': wait, 'expect': expect}]
        yield self._test(finalPrompt, commands)

    @defer.inlineCallbacks
    def test_systype(self):
        """Testing for #64, will set systype key to any value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'systype 999999'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_systype(self):
        """Testing for #64, will set systype key to any value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Update the connector to set systype and start it
        commands = [{'command': 'smppccm -u operator_1'},
                    {'command': 'systype 999999'},
                    {'command': 'ok'}]
        yield self._test(r'jcli : ', commands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_username(self):
        """Testing for #105, will set username to an int value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'username 999999'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_username(self):
        """Testing for #105, will set username to an int value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Update the connector to set systype and start it
        commands = [{'command': 'smppccm -u operator_1'},
                    {'command': 'username 999999'},
                    {'command': 'ok'}]
        yield self._test(r'jcli : ', commands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_password(self):
        """Testing for #105, will set password to an int value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'password 999999'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_password(self):
        """Testing for #105, will set password to an int value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Update the connector to set systype and start it
        commands = [{'command': 'smppccm -u operator_1'},
                    {'command': 'password 999999'},
                    {'command': 'ok'}]
        yield self._test(r'jcli : ', commands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_quick_restart(self):
        "Testing for #68, restarting quickly a connector will loose its session state"

        # Add a connector and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1', wait = 8)

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Stop and start very quickly will lead to an error starting the connector because there were
        # no sufficient time for unbind to complete
        yield self.stop_connector('operator_1', finalPrompt = None, wait = 0)
        yield self.start_connector('operator_1', finalPrompt = None,
                                    wait = 20,
                                    expect= 'Failed starting connector, check log for details')

        # List and assert it is stopped (start command errored)
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          stopped NONE             1      1    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_restart_on_update(self):
        "Testing for #68, updating a config key from RequireRestartKeys will lead to a quick restart"

        # Add a connector and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        1      0    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Update loglevel which is in RequireRestartKeys and will lead to a connector restart
        commands = [{'command': 'smppccm -u operator_1'},
                    {'command': 'systype ANY'},
                    {'command': 'ok', 'wait': 7,
                        'expect': ['Restarting connector \[operator_1\] for updates to take effect ...',
                                   'Failed starting connector, will retry in 5 seconds',
                                   'Successfully updated connector \[operator_1\]']},]
        yield self._test(r'jcli : ', commands)

        # List and assert it is started (restart were successful)
        expectedList = ['#Connector id                        Service Session          Starts Stops',
                        '#operator_1                          started BOUND_TRX        2      1    ',
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_bind_ton_npi_and_address_range(self):
        """Testing for #104, updating bind_ton & bind_npi through jcli must take effect"""

        # Add a connector, set bind_ton
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'bind_ton 1'},
                         {'command': 'bind_npi 0'},
                         {'command': 'addr_range ^32.*{6}$'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Update connector and start it
        commands = [{'command': 'smppccm -u operator_1'},
                    {'command': 'bind_ton 5'}, # ALPHANUMERIC
                    {'command': 'bind_npi 8'}, # NATIONAL
                    {'command': 'addr_range ^34.*{6}$'}, # NATIONAL
                    {'command': 'ok'}]
        yield self._test(r'jcli : ', commands)
        yield self.start_connector('operator_1')

        # Assert bind_ton value
        self.assertEqual(1, len(self.SMSCPort.factory.lastClient.pduRecords))
        self.assertEqual('ALPHANUMERIC', str(self.SMSCPort.factory.lastClient.pduRecords[0].params['addr_ton']))
        self.assertEqual('NATIONAL', str(self.SMSCPort.factory.lastClient.pduRecords[0].params['addr_npi']))
        self.assertEqual('^34.*{6}$', str(self.SMSCPort.factory.lastClient.pduRecords[0].params['address_range']))
