import unittest
from twisted.internet import defer
from test_jcli import jCliWithoutAuthTestCases
from .test_userm import UserTestCases
from .test_smppccm import SmppccmTestCases

class BasicTestCases(jCliWithoutAuthTestCases):

    def test_user(self):
        uid = 'foo'

        commands = [{'command': 'stats --user=%s' % uid, 'expect': r'Unknown User: %s' % uid}]
        return self._test(r'jcli : ', commands)
    
    def test_users(self):
        expectedList = ['#User id\s+SMPP Bound connections\s+SMPP L.A.\s+HTTP requests counter\s+HTTP L.A.', 
                        'Total users: 0']
        commands = [{'command': 'stats --users', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_smppc(self):
        cid = 'foo'

        commands = [{'command': 'stats --smppc=%s' % cid, 'expect': r'Unknown connector: %s' % cid}]
        return self._test(r'jcli : ', commands)
    
    def test_smppcs(self):
        expectedList = ['#Connector id\s+Bound count\s+Connected at\s+Bound at\s+Disconnected at\s+Sent elink at\s+Received elink at', 
                        'Total connectors: 0']
        commands = [{'command': 'stats --smppcs', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    @unittest.skip("Work in progress #123")
    def test_moroute(self):
        order = '20'

        commands = [{'command': 'stats --moroute=%s' % order, 'expect': r'????'}]
        return self._test(r'jcli : ', commands)
    
    @unittest.skip("Work in progress #123")
    def test_moroutes(self):
        commands = [{'command': 'stats --moroutes', 'expect': r'????'}]
        return self._test(r'jcli : ', commands)

    @unittest.skip("Work in progress #123")
    def test_mtroute(self):
        order = '20'

        commands = [{'command': 'stats --mtroute=%s' % order, 'expect': r'????'}]
        return self._test(r'jcli : ', commands)
    
    @unittest.skip("Work in progress #123")
    def test_mtroutes(self):
        commands = [{'command': 'stats --mtroutes', 'expect': r'????'}]
        return self._test(r'jcli : ', commands)

    @unittest.skip("Work in progress #123")
    def test_httpapi(self):
        commands = [{'command': 'stats --httpapi', 'expect': r'????'}]
        return self._test(r'jcli : ', commands)

    @unittest.skip("Work in progress #123")
    def test_smppsapi(self):
        commands = [{'command': 'stats --smppsapi', 'expect': r'????'}]
        return self._test(r'jcli : ', commands)

class UserStatsTestCases(UserTestCases):
    def test_users(self):
        extraCommands = [{'command': 'uid test_users'}]
        self.add_user('jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')

        expectedList = ['#User id\s+SMPP Bound connections\s+SMPP L.A.\s+HTTP requests counter\s+HTTP L.A.', 
                        '#test_users\s+0\s+ND\s+0\s+ND', 
                        'Total users: 1']
        commands = [{'command': 'stats --users', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_user(self):
        extraCommands = [{'command': 'uid test_user'}]
        self.add_user('jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')

        expectedList = ['#Item                     Type         Value', 
                        '#last_activity_at         SMPP Server  ND', 
                        '#bind_count               SMPP Server  0',
                        "#bound_connections_count  SMPP Server  {'bind_transmitter': 0, 'bind_receiver': 0, 'bind_transceiver': 0}",
                        '#submit_sm_request_count  SMPP Server  0',
                        '#qos_last_submit_sm_at    SMPP Server  ND',
                        '#unbind_count             SMPP Server  0',
                        '#qos_last_submit_sm_at    HTTP Api     ND',
                        '#connects_count           HTTP Api     0',
                        '#last_activity_at         HTTP Api     ND',
                        '#submit_sm_request_count  HTTP Api     0']
        commands = [{'command': 'stats --user=test_user', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

class SmppcStatsTestCases(SmppccmTestCases):
    @defer.inlineCallbacks
    def test_smppcs(self):
        extraCommands = [{'command': 'cid test_smppcs'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        expectedList = ['#Connector id\s+Bound count\s+Connected at\s+Bound at\s+Disconnected at\s+Sent elink at\s+Received elink at', 
                        '#test_smppcs\s+0\s+ND\s+ND\s+ND\s+ND\s+ND', 
                        'Total connectors: 1']
        commands = [{'command': 'stats --smppcs', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_smppc(self):
        extraCommands = [{'command': 'cid test_smppc'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        expectedList = ['#Connector id\s+Bound count\s+Connected at\s+Bound at\s+Disconnected at\s+Sent elink at\s+Received elink at', 
                        '#test_smppc\s+0\s+ND\s+ND\s+ND\s+ND\s+ND', 
                        'Total connectors: 1']
        commands = [{'command': 'stats --smppcs', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        expectedList = ['#Item                    Value', 
                        '#disconnected_count      0',
                        '#last_received_pdu_at    ND',
                        '#last_received_elink_at  ND',
                        '#connected_count         0',
                        '#connected_at            ND',
                        '#last_seqNum',
                        '#disconnected_at         ND',
                        '#bound_at                ND',
                        '#created_at              \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}',
                        '#last_sent_elink_at      ND',
                        '#bound_count             0',
                        '#last_seqNum_at          ND',
                        '#last_sent_pdu_at        ND']
        commands = [{'command': 'stats --smppc=test_smppc', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)