#pylint: disable=W0401,W0611

import re
from twisted.trial.unittest import TestCase
from jasmin.routing import jasminApi
from jasmin.routing.jasminApi import *

class GroupTestCase(TestCase):

    def test_normal(self):
        g = Group('GID')

        self.assertEqual(g.gid, 'GID')
        self.assertEqual(str(g), str(g.gid))

    def test_is_enabled_by_default(self):
        g = Group('GID')

        self.assertEqual(g.enabled, True)

    def test_enabled_disable(self):
        g = Group('GID')

        self.assertEqual(g.enabled, True)

        g.disable()
        self.assertEqual(g.enabled, False)
        g.enable()
        self.assertEqual(g.enabled, True)

class UserTestCase(TestCase):

    def setUp(self):
        TestCase.setUp(self)

        self.group = Group('GID')

    def test_normal(self):
        u = User('UID', self.group, 'foo', 'bar')

        self.assertEqual(u.uid, 'UID')
        self.assertEqual(str(u), u.username)

    def test_with_credentials(self):
        mt_c = MtMessagingCredential()
        u = User('UID', self.group, 'foo', 'bar', mt_c)

        self.assertEqual(u.mt_credential, mt_c)

    def test_user_password(self):
        # Password length is 0
        self.assertRaises(jasminApiInvalidParamError,
            User,
            'UID',
            self.group,
            'foo',
            '')

        # Password length is >8
        self.assertRaises(jasminApiInvalidParamError,
            User,
            'UID',
            self.group,
            'foo',
            '123456789')

    def test_is_enabled_by_default(self):
        u = User('UID', self.group, 'foo', 'bar')

        self.assertEqual(u.enabled, True)

    def test_enabled_disable(self):
        u = User('UID', self.group, 'foo', 'bar')

        self.assertEqual(u.enabled, True)

        u.disable()
        self.assertEqual(u.enabled, False)
        u.enable()
        self.assertEqual(u.enabled, True)

class UserAndCredentialsTestCase(TestCase):

    def setUp(self):
        TestCase.setUp(self)
        # Define a GID group with custom messaging credentials
        self.group = Group('GID')

    def test_without_user_defined_credentials(self):
        # Create user in GID without 'user-level' credentials
        u = User('UID', self.group, 'foo', 'bar')

        # Credentials are defaults
        self.assertEqual(u.mt_credential.getAuthorization('http_send'), True)
        self.assertEqual(u.mt_credential.getValueFilter('source_address'), re.compile('.*'))
        self.assertEqual(u.mt_credential.getDefaultValue('source_address'), None)
        self.assertEqual(u.mt_credential.getQuota('balance'), None)
        self.assertEqual(u.mt_credential.getQuota('early_decrement_balance_percent'), None)

    def test_with_user_defined_credentials(self):
        mt_c = MtMessagingCredential()
        mt_c.setQuota('balance', 2)
        mt_c.setQuota('early_decrement_balance_percent', 10)
        # Create user
        u = User('UID', self.group, 'foo', 'bar', mt_c)
        u.mt_credential.setDefaultValue('source_address', 'SunHotel')
        u.mt_credential.setAuthorization('http_send', False)
        u.mt_credential.setValueFilter('source_address', r'^216.*')

        self.assertEqual(u.mt_credential.getQuota('balance'), 2)
        self.assertEqual(u.mt_credential.getQuota('early_decrement_balance_percent'), 10)
        self.assertEqual(u.mt_credential.getDefaultValue('source_address'), 'SunHotel')
        self.assertEqual(u.mt_credential.getAuthorization('http_send'), False)
        self.assertEqual(u.mt_credential.getValueFilter('source_address'), re.compile(r'^216.*'))

class MtMessagingCredentialTestCase(TestCase):
    def test_normal_noargs(self):
        mc = MtMessagingCredential()

        self.assertEqual(mc.getAuthorization('http_send'), True)
        self.assertEqual(mc.getAuthorization('smpps_send'), True)
        self.assertEqual(mc.getAuthorization('http_long_content'), True)
        self.assertEqual(mc.getAuthorization('set_dlr_level'), True)
        self.assertEqual(mc.getAuthorization('http_set_dlr_method'), True)
        self.assertEqual(mc.getAuthorization('set_source_address'), True)
        self.assertEqual(mc.getAuthorization('set_priority'), True)
        self.assertEqual(mc.getAuthorization('set_validity_period'), True)
        self.assertEqual(mc.getValueFilter('destination_address'), re.compile(r'.*'))
        self.assertEqual(mc.getValueFilter('source_address'), re.compile(r'.*'))
        self.assertEqual(mc.getValueFilter('priority'), re.compile(r'^[0-3]$'))
        self.assertEqual(mc.getValueFilter('content'), re.compile(r'.*'))
        self.assertEqual(mc.getDefaultValue('source_address'), None)
        self.assertEqual(mc.getQuota('balance'), None)
        self.assertEqual(mc.getQuota('submit_sm_count'), None)
        self.assertEqual(mc.getQuota('http_throughput'), None)
        self.assertEqual(mc.getQuota('smpps_throughput'), None)
        self.assertEqual(mc.getQuota('early_decrement_balance_percent'), None)

    def test_normal_defaultsargs(self):
        mc = MtMessagingCredential(default_authorizations = False)

        self.assertEqual(mc.getAuthorization('http_send'), False)
        self.assertEqual(mc.getAuthorization('smpps_send'), False)
        self.assertEqual(mc.getAuthorization('http_long_content'), False)
        self.assertEqual(mc.getAuthorization('set_dlr_level'), False)
        self.assertEqual(mc.getAuthorization('http_set_dlr_method'), False)
        self.assertEqual(mc.getAuthorization('set_source_address'), False)
        self.assertEqual(mc.getAuthorization('set_priority'), False)
        self.assertEqual(mc.getAuthorization('set_validity_period'), False)
        self.assertEqual(mc.getValueFilter('destination_address'), re.compile(r'.*'))
        self.assertEqual(mc.getValueFilter('source_address'), re.compile(r'.*'))
        self.assertEqual(mc.getValueFilter('priority'), re.compile(r'^[0-3]$'))
        self.assertEqual(mc.getValueFilter('content'), re.compile(r'.*'))
        self.assertEqual(mc.getDefaultValue('source_address'), None)
        self.assertEqual(mc.getQuota('balance'), None)
        self.assertEqual(mc.getQuota('submit_sm_count'), None)
        self.assertEqual(mc.getQuota('http_throughput'), None)
        self.assertEqual(mc.getQuota('smpps_throughput'), None)
        self.assertEqual(mc.getQuota('early_decrement_balance_percent'), None)

    def test_set_and_get(self):
        mc = MtMessagingCredential()

        mc.setAuthorization('http_send', False)
        self.assertEqual(mc.getAuthorization('http_send'), False)
        mc.setAuthorization('smpps_send', False)
        self.assertEqual(mc.getAuthorization('smpps_send'), False)
        mc.setAuthorization('http_long_content', False)
        self.assertEqual(mc.getAuthorization('http_long_content'), False)
        mc.setAuthorization('set_dlr_level', False)
        self.assertEqual(mc.getAuthorization('set_dlr_level'), False)
        mc.setAuthorization('http_set_dlr_method', False)
        self.assertEqual(mc.getAuthorization('http_set_dlr_method'), False)
        mc.setAuthorization('set_source_address', False)
        self.assertEqual(mc.getAuthorization('set_source_address'), False)
        mc.setAuthorization('set_priority', False)
        self.assertEqual(mc.getAuthorization('set_priority'), False)
        mc.setAuthorization('set_validity_period', False)
        self.assertEqual(mc.getAuthorization('set_validity_period'), False)
        mc.setValueFilter('destination_address', r'^D.*')
        self.assertEqual(mc.getValueFilter('destination_address'), re.compile(r'^D.*'))
        mc.setValueFilter('source_address', r'^S.*')
        self.assertEqual(mc.getValueFilter('source_address'), re.compile(r'^S.*'))
        mc.setValueFilter('priority', r'[12]')
        self.assertEqual(mc.getValueFilter('priority'), re.compile(r'[12]'))
        mc.setValueFilter('content', r'^C.*')
        self.assertEqual(mc.getValueFilter('content'), re.compile(r'^C.*'))
        mc.setDefaultValue('source_address', 'JasminGateway')
        self.assertEqual(mc.getDefaultValue('source_address'), 'JasminGateway')
        mc.setQuota('balance', 100)
        self.assertEqual(mc.getQuota('balance'), 100)
        mc.setQuota('submit_sm_count', 10000)
        self.assertEqual(mc.getQuota('submit_sm_count'), 10000)
        mc.setQuota('http_throughput', 100)
        mc.setQuota('smpps_throughput', 10)
        self.assertEqual(mc.getQuota('http_throughput'), 100)
        self.assertEqual(mc.getQuota('smpps_throughput'), 10)
        mc.setQuota('early_decrement_balance_percent', 100)
        self.assertEqual(mc.getQuota('early_decrement_balance_percent'), 100)

    def test_get_invalid_key(self):
        mc = MtMessagingCredential()

        self.assertRaises(jasminApiCredentialError, mc.getAuthorization, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getValueFilter, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getDefaultValue, 'anykey')
        self.assertRaises(jasminApiCredentialError, mc.getQuota, 'anykey')

    def test_set_invalid_key(self):
        mc = MtMessagingCredential()

        self.assertRaises(jasminApiCredentialError, mc.setAuthorization, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setValueFilter, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setDefaultValue, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'anykey', 'anyvalue')

    def test_invalid_default_authorization(self):
        "Setting an incorrect default_authorizations would fallback to False as a default_authorizations"
        mc = MtMessagingCredential(default_authorizations = 'True')

        self.assertEqual(mc.getAuthorization('http_send'), False)
        self.assertEqual(mc.getAuthorization('http_long_content'), False)
        self.assertEqual(mc.getAuthorization('set_dlr_level'), False)
        self.assertEqual(mc.getAuthorization('http_set_dlr_method'), False)
        self.assertEqual(mc.getAuthorization('set_source_address'), False)
        self.assertEqual(mc.getAuthorization('set_priority'), False)
        self.assertEqual(mc.getAuthorization('set_validity_period'), False)

    def test_set_invalid_value(self):
        mc = MtMessagingCredential()

        # Authorization must be a boolean
        mc.setAuthorization('http_send', True)
        self.assertRaises(jasminApiCredentialError, mc.setAuthorization, 'http_send', 'anyvalue')
        # ValueFilter must be a compilable regex pattern
        mc.setValueFilter('destination_address', r'.*')
        self.assertRaises(jasminApiCredentialError, mc.setValueFilter, 'destination_address', 1)
        self.assertRaises(jasminApiCredentialError, mc.setValueFilter, 'destination_address', None)
        # Balance must be None or a positive float
        mc.setQuota('balance', None)
        mc.setQuota('balance', 0)
        mc.setQuota('balance', 2.2)
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'balance', -1.0)
        # early_decrement_balance_percent must be None or 1-100
        mc.setQuota('early_decrement_balance_percent', None)
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'early_decrement_balance_percent', 0)
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'early_decrement_balance_percent', 101)
        # submit_sm_count must be a positive int
        mc.setQuota('submit_sm_count', 10)
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'submit_sm_count', -1)
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'submit_sm_count', 1.1)
        # *_throughput must be a positive float
        mc.setQuota('http_throughput', 10)
        mc.setQuota('http_throughput', None)
        mc.setQuota('http_throughput', 2.5)
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'http_throughput', -1)
        mc.setQuota('smpps_throughput', 10)
        mc.setQuota('smpps_throughput', None)
        mc.setQuota('smpps_throughput', 2.5)
        self.assertRaises(jasminApiCredentialError, mc.setQuota, 'smpps_throughput', -1)

    def test_quotas_updated(self):
        mc = MtMessagingCredential()
        mc.setQuota('submit_sm_count', 2)

        self.assertEqual(mc.quotas_updated, False)
        mc.updateQuota('submit_sm_count', 1)
        self.assertEqual(mc.quotas_updated, True)

    def test_quotas_update_types(self):
        sc = MtMessagingCredential()
        sc.setQuota('submit_sm_count', 2)
        sc.setQuota('balance', 2)

        self.assertRaises(jasminApiCredentialError, sc.updateQuota, 'balance', 'A')
        self.assertRaises(jasminApiCredentialError, sc.updateQuota, 'submit_sm_count', 0.2)

class SmppsCredentialTestCase(TestCase):
    def test_normal_noargs(self):
        sc = SmppsCredential()

        self.assertEqual(sc.getAuthorization('bind'), True)
        self.assertEqual(sc.getQuota('max_bindings'), None)

    def test_normal_defaultsargs(self):
        sc = SmppsCredential(default_authorizations = False)

        self.assertEqual(sc.getAuthorization('bind'), False)
        self.assertEqual(sc.getQuota('max_bindings'), None)

    def test_set_and_get(self):
        sc = SmppsCredential()

        sc.setAuthorization('bind', False)
        self.assertEqual(sc.getAuthorization('bind'), False)
        sc.setQuota('max_bindings', 100)
        self.assertEqual(sc.getQuota('max_bindings'), 100)

    def test_get_invalid_key(self):
        sc = SmppsCredential()

        self.assertRaises(jasminApiCredentialError, sc.getAuthorization, 'anykey')
        self.assertRaises(jasminApiCredentialError, sc.getValueFilter, 'anykey')
        self.assertRaises(jasminApiCredentialError, sc.getDefaultValue, 'anykey')
        self.assertRaises(jasminApiCredentialError, sc.getQuota, 'anykey')

    def test_set_invalid_key(self):
        sc = SmppsCredential()

        self.assertRaises(jasminApiCredentialError, sc.setAuthorization, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, sc.setValueFilter, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, sc.setDefaultValue, 'anykey', 'anyvalue')
        self.assertRaises(jasminApiCredentialError, sc.setQuota, 'anykey', 'anyvalue')

    def test_invalid_default_authorization(self):
        "Setting an incorrect default_authorizations would fallback to False as a default_authorizations"
        sc = SmppsCredential(default_authorizations = 'True')

        self.assertEqual(sc.getAuthorization('bind'), False)

    def test_set_invalid_value(self):
        sc = SmppsCredential()

        # Authorization must be a boolean
        sc.setAuthorization('bind', True)
        self.assertRaises(jasminApiCredentialError, sc.setAuthorization, 'bind', 'anyvalue')
        # max_bindings must be None or a positive int
        sc.setQuota('max_bindings', None)
        sc.setQuota('max_bindings', 0)
        sc.setQuota('max_bindings', 2)
        self.assertRaises(jasminApiCredentialError, sc.setQuota, 'max_bindings', 1.0)
        self.assertRaises(jasminApiCredentialError, sc.setQuota, 'max_bindings', -1.0)
        self.assertRaises(jasminApiCredentialError, sc.setQuota, 'max_bindings', -1)

    def test_quotas_updated(self):
        sc = SmppsCredential()
        sc.setQuota('max_bindings', 2)

        self.assertEqual(sc.quotas_updated, False)
        sc.updateQuota('max_bindings', 1)
        self.assertEqual(sc.quotas_updated, True)

    def test_quotas_update_types(self):
        sc = SmppsCredential()
        sc.setQuota('max_bindings', 2)

        self.assertRaises(jasminApiCredentialError, sc.updateQuota, 'max_bindings', 'A')
        self.assertRaises(jasminApiCredentialError, sc.updateQuota, 'max_bindings', 0.2)

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
        self.assertEqual(c._str, 'smppc Connector')
        self.assertEqual(c._repr, '<smppc Connector>')

class SmppServerSystemIdTestCase(TestCase):
    def test_normal(self):
        c = SmppServerSystemIdConnector(system_id = 'nathalie')

        self.assertEqual(c.type, 'smpps')
        self.assertEqual(c._str, 'smpps Connector')
        self.assertEqual(c._repr, '<smpps Connector>')
        self.assertEqual(c.cid, 'nathalie')
        self.assertEqual(c.cid, c.system_id)

class MOInterceptorScriptTestCase(TestCase):
    def test_normal(self):
        i = MOInterceptorScript('somecode')

        self.assertEqual(i.type, 'moi')
        self.assertEqual(i.pyCode, 'somecode')
        self.assertEqual(i._repr, '<MOIS (pyCode=%s ..)>' % (i.pyCode[:10].replace('\n', '')))
        self.assertEqual(i._str, 'MOInterceptorScript:\n%s' % (i.pyCode))

class MTInterceptorScriptTestCase(TestCase):
    def test_normal(self):
        i = MTInterceptorScript('somecode')

        self.assertEqual(i.type, 'mti')
        self.assertEqual(i.pyCode, 'somecode')
        self.assertEqual(i._repr, '<MTIS (pyCode=%s ..)>' % (i.pyCode[:10].replace('\n', '')))
        self.assertEqual(i._str, 'MTInterceptorScript:\n%s' % (i.pyCode))
