import unittest
from test_jcli import jCliWithoutAuthTestCases
from .test_userm import UserTestCases

class BasicTestCases(jCliWithoutAuthTestCases):

    def test_user(self):
        uid = 'foo'

        commands = [{'command': 'stats --user=%s' % uid, 'expect': r'Unknown User: %s' % uid}]
        return self._test(r'jcli : ', commands)
    
    def test_users(self):
        expectedList = ['#User id    SMPP Bound connections    SMPP L.A.    HTTP requests counter    HTTP L.A.', 
                        'Total users: 0']
        commands = [{'command': 'stats --users', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    @unittest.skip("Work in progress #123")
    def test_smppc(self):
        cid = 'foo'

        commands = [{'command': 'stats --smppc=%s' % cid, 'expect': r'????'}]
        return self._test(r'jcli : ', commands)
    
    @unittest.skip("Work in progress #123")
    def test_smppcs(self):
        commands = [{'command': 'stats --smppcs', 'expect': r'????'}]
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
        extraCommands = [{'command': 'uid user_1'}]
        self.add_user('jcli : ', extraCommands, GID = 'AnyGroup', Username = 'AnyUsername')

        expectedList = ['#User id    SMPP Bound connections    SMPP L.A.    HTTP requests counter    HTTP L.A.', 
                        '#user_1     0                         ND           0                        ND', 
                        'Total users: 1']
        commands = [{'command': 'stats --users', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)

    def test_user(self):
        extraCommands = [{'command': 'uid user_1'}]
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
        commands = [{'command': 'stats --user=user_1', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)