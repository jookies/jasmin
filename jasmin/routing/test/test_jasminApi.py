#pylint: disable-msg=W0401,W0611
from twisted.trial.unittest import TestCase
from jasmin.routing import jasminApi
from jasmin.routing.jasminApi import *

class GroupTestCase(TestCase):
    
    def test_normal(self):
        g = Group('GID')
        
        self.assertEqual(g.gid, 'GID')
        self.assertEqual(str(g), g.gid)
    
    def test_with_credentials(self):
        mo_c = MoMessagingCredential()
        mt_c = MtMessagingCredential()
        g = Group('GID', mo_c, mt_c)
        
        self.assertEqual(g.mo_credential, mo_c)
        self.assertEqual(g.mt_credential, mt_c)

class UserTestCase(TestCase):
    
    def setUp(self):
        TestCase.setUp(self)
        
        self.group = Group('GID')
    
    def test_normal(self):
        u = User('UID', self.group, 'foo', 'bar')
        
        self.assertEqual(u.uid, 'UID')
        self.assertEqual(str(u), u.username)
    
    def test_with_credentials(self):
        mo_c = MoMessagingCredential()
        mt_c = MtMessagingCredential()
        u = User('UID', self.group, 'foo', 'bar', mo_c, mt_c)
        
        self.assertEqual(u.mo_credential, mo_c)
        self.assertEqual(u.mt_credential, mt_c)

class UserAndCredentialsTestCase(TestCase):
    
    def setUp(self):
        TestCase.setUp(self)
        
        mo_c = MoMessagingCredential()
        mo_c.setAuthorization('receive', False)
        mo_c.setQuota('balance', 3)
        
        mt_c = MtMessagingCredential()
        mt_c.setAuthorization('send', False)
        mt_c.setValueFilter('source_address', '^S.*')
        mt_c.setDefaultValue('source_address', 'SunHotel')
        mt_c.setQuota('balance', 2)
        
        # Define a GID group with custom messaging credentials
        self.group = Group('GID', mo_c, mt_c)

    def test_without_user_level_credentials(self):
        # Create user in GID without 'user-level' credentials
        u = User('UID', self.group, 'foo', 'bar')
        
        # Credentials are inherited from group
        self.assertEqual(u.getMOAuthorization('receive'), False)
        self.assertEqual(u.getMOQuota('balance'), 3)
        self.assertEqual(u.getMTAuthorization('send'), False)
        self.assertEqual(u.getMTValueFilter('source_address'), '^S.*')
        self.assertEqual(u.getMTDefault('source_address'), 'SunHotel')
        self.assertEqual(u.getMTQuota('balance'), 2)

    def test_with_user_level_credentials(self):
        mo_c = MoMessagingCredential()
        mt_c = MtMessagingCredential()
        # Create user in GID without 'user-level' credentials
        u = User('UID', self.group, 'foo', 'bar', mo_c, mt_c)
        
        # Credentials are still inherited from group, this is because the
        # user-level values are None
        self.assertEqual(u.getMOQuota('balance'), 3)
        self.assertEqual(u.getMTQuota('balance'), 2)
        self.assertEqual(u.getMTDefault('source_address'), 'SunHotel')

        # Credentials are taken from user-level scope, ignoring group-level credentials
        self.assertEqual(u.getMOAuthorization('receive'), True)
        self.assertEqual(u.getMTAuthorization('send'), True)
        self.assertEqual(u.getMTValueFilter('source_address'), '.*')

class MoMessagingCredentialTestCase(TestCase):
    messaging_cred_class = 'MoMessagingCredential'

    def test_normal(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()

        self.assertEqual(mc.getAuthorization('receive'), True)
        self.assertEqual(mc.getQuota('balance'), None)
        self.assertEqual(mc.getQuota('submit_sm_count'), None)

    def test_set_and_get(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()
        
        mc.setAuthorization('receive', False)
        self.assertEqual(mc.getAuthorization('receive'), False)
        mc.setQuota('balance', 100)
        self.assertEqual(mc.getQuota('balance'), 100)
        mc.setQuota('submit_sm_count', 10000)
        self.assertEqual(mc.getQuota('submit_sm_count'), 10000)
    
    def test_get_invalid_key(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()
        
        self.assertRaises(jasminApiCredentialError, mc.getAuthorization, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getValueFilter, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getDefaultValue, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getQuota, 'anykey')

    def test_set_invalid_key(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()

        self.assertRaises(jasminApiCredentialError, mc.setAuthorization, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setValueFilter, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setDefaultValue, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'anykey', 'anyvalue')

class MtMessagingCredentialTestCase(TestCase):
    messaging_cred_class = 'MtMessagingCredential'

    def test_normal(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()
        
        self.assertEqual(mc.getAuthorization('send'), True)
        self.assertEqual(mc.getAuthorization('long_content'), True)
        self.assertEqual(mc.getAuthorization('set_dlr_level'), True)
        self.assertEqual(mc.getAuthorization('set_dlr_method'), True)
        self.assertEqual(mc.getAuthorization('set_source_address'), True)
        self.assertEqual(mc.getAuthorization('set_priority'), True)
        self.assertEqual(mc.getValueFilter('destination_address'), r'.*')
        self.assertEqual(mc.getValueFilter('source_address'), r'.*')
        self.assertEqual(mc.getValueFilter('priority'), r'[123]')
        self.assertEqual(mc.getValueFilter('content'), r'.*')
        self.assertEqual(mc.getDefaultValue('source_address'), None)
        self.assertEqual(mc.getQuota('balance'), None)
        self.assertEqual(mc.getQuota('submit_sm_count'), None)

    def test_set_and_get(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()
        
        mc.setAuthorization('send', False)
        self.assertEqual(mc.getAuthorization('send'), False)
        mc.setAuthorization('long_content', False)
        self.assertEqual(mc.getAuthorization('long_content'), False)
        mc.setAuthorization('set_dlr_level', False)
        self.assertEqual(mc.getAuthorization('set_dlr_level'), False)
        mc.setAuthorization('set_dlr_method', False)
        self.assertEqual(mc.getAuthorization('set_dlr_method'), False)
        mc.setAuthorization('set_source_address', False)
        self.assertEqual(mc.getAuthorization('set_source_address'), False)
        mc.setAuthorization('set_priority', False)
        self.assertEqual(mc.getAuthorization('set_priority'), False)
        mc.setValueFilter('destination_address', r'^D.*')
        self.assertEqual(mc.getValueFilter('destination_address'), r'^D.*')
        mc.setValueFilter('source_address', r'^S.*')
        self.assertEqual(mc.getValueFilter('source_address'), r'^S.*')
        mc.setValueFilter('priority', r'[12]')
        self.assertEqual(mc.getValueFilter('priority'), r'[12]')
        mc.setValueFilter('content', r'^C.*')
        self.assertEqual(mc.getValueFilter('content'), r'^C.*')
        mc.setDefaultValue('source_address', 'JasminGateway')
        self.assertEqual(mc.getDefaultValue('source_address'), 'JasminGateway')
        mc.setQuota('balance', 100)
        self.assertEqual(mc.getQuota('balance'), 100)
        mc.setQuota('submit_sm_count', 10000)
        self.assertEqual(mc.getQuota('submit_sm_count'), 10000)
    
    def test_get_invalid_key(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()
        
        self.assertRaises(jasminApiCredentialError, mc.getAuthorization, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getValueFilter, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getDefaultValue, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getQuota, 'anykey')

    def test_set_invalid_key(self):
        mc = getattr(jasminApi, self.messaging_cred_class)()

        self.assertRaises(jasminApiCredentialError, mc.setAuthorization, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setValueFilter, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setDefaultValue, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'anykey', 'anyvalue')

class HttpConnectorTestCase(TestCase):
    def test_normal(self):
        c = HttpConnector('CID', 'http://127.0.0.1/api/receive-mo')
        
        self.assertEqual(c.type, 'http')
        self.assertEqual(c.cid, 'CID')
        self.assertEqual(c.baseurl, 'http://127.0.0.1/api/receive-mo')
        self.assertEqual(c.method, 'GET')
        self.assertEqual(str(c), '%s:\ncid = %s\nbaseurl = %s\nmethod = %s' % ('HttpConnector', 
                                                                  c.cid, 
                                                                  c.baseurl, 
                                                                  c.method))
        self.assertEqual(repr(c), '<%s (cid=%s, baseurl=%s, method=%s)>' % ('HttpConnector', 
                                                               c.cid, 
                                                               c.baseurl, 
                                                               c.method))
    
    def test_set_method(self):
        c = HttpConnector('CID', 'http://127.0.0.1/api/receive-mo', 'POST')
        self.assertEqual(c.method, 'POST')
    
    def test_set_invalid_syntax(self):
        # Invalid CID
        self.assertRaises(jasminApiInvalidParamError, HttpConnector, 'Wrong CID', 'http://127.0.0.1/api/receive-mo')
        # Invalid Base url
        self.assertRaises(jasminApiInvalidParamError, HttpConnector, 'CID', 'fttp://127.0.0.1/api/receive-mo')
        # Invalid method
        self.assertRaises(jasminApiInvalidParamError, HttpConnector, 'CID', 'http://127.0.0.1/api/receive-mo', 'PAST')

class SmppConnectorTestCase(TestCase):
    def test_normal(self):
        c = SmppClientConnector('CID')
        
        self.assertEqual(c.type, 'smppc')