import unittest
from test_jcli import jCliWithoutAuthTestCases

class BasicTestCases(jCliWithoutAuthTestCases):
    
    @unittest.skip("Work in progress #123")
    def test_user(self):
        uid = 'foo'

        commands = [{'command': 'stats --user=%s' % uid, 'expect': r'????'}]
        return self._test(r'jcli : ', commands)
    
    @unittest.skip("Work in progress #123")
    def test_users(self):
        commands = [{'command': 'stats --users', 'expect': r'????'}]
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