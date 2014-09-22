# -*- coding: utf-8 -*- 

import pickle
import copy
import time
import urllib
from twisted.internet import reactor
from twisted.web.client import getPage
from twisted.internet import defer
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router import HappySMSCTestCase
from jasmin.routing.jasminApi import User, Group, SmppClientConnector
from jasmin.routing.Routes import DefaultRoute
from jasmin.protocols.smpp.configs import SMPPClientConfig

class CredentialsTestCases(RouterPBProxy, HappySMSCTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)
        
        # Init
        self.group1 = Group('g1')
        self.user1 = User('1', self.group1, 'u1', 'password')
        self.c1 = SmppClientConnector('smpp_c1')
    
    @defer.inlineCallbacks
    def prepareRoutingsAndStartConnector(self, user, group, default_route = None):
        # Routing stuff
        yield self.group_add(group)
        
        yield self.user_add(user)
        if default_route is None:
            yield self.mtroute_add(DefaultRoute(self.c1), 0)
        else:
            yield self.mtroute_add(default_route, 0)

        # Now we'll create the connecter
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid, port = self.SMSCPort.getHost().port, 
                                    bindOperation = 'transceiver')
        yield self.SMPPClientManagerPBProxy.add(c1Config)

        # Start the connector
        yield self.SMPPClientManagerPBProxy.start(self.c1.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(self.c1.cid)
            if ssRet[:6] == 'BOUND_':
                break;
            else:
                time.sleep(0.2)

        # Configuration
        self.method = 'GET'
        self.postdata = None
        self.params = {'to': '98700177', 
                        'username': user.username, 
                        'password': 'password', 
                        'content': 'test'}

    @defer.inlineCallbacks
    def stopSmppClientConnectors(self):
        # Disconnect the connector
        yield self.SMPPClientManagerPBProxy.stop(self.c1.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(self.c1.cid)
            if ssRet == 'NONE':
                break;
            else:
                time.sleep(0.2)

    @defer.inlineCallbacks
    def run_test(self, user, group, content = 'anycontent', 
                 dlr_level = None, dlr_method = None, source_address = None, 
                 priority = None, destination_address = None, default_route = None):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(user, group, default_route)
        
        # Set content
        self.params['content'] = content
        if dlr_level is not None:
            self.params['dlr-level'] = dlr_level
        if dlr_method is not None:
            self.params['dlr-method'] = dlr_method
        if source_address is not None:
            self.params['from'] = source_address
        if priority is not None:
            self.params['priority'] = priority
        if destination_address is not None:
            self.params['to'] = destination_address
        baseurl = 'http://127.0.0.1:%s/send?%s' % (1401, urllib.urlencode(self.params))
        
        # Send a MT
        # We should receive a msg id
        try:
            response_text = yield getPage(baseurl, method = self.method, postdata = self.postdata)
            response_code = 'Success'
        except Exception, error:
            response_text = error.response
            response_code = str(error)
        
        # Wait 2 seconds before stopping SmppClientConnectors
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
        defer.returnValue((response_text, response_code))

class AuthorizationsTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_send(self):
        # User have default authorization to send an sms
        response_text, response_code = yield self.run_test(self.user1, self.group1)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
    @defer.inlineCallbacks
    def test_unauthorized_user_send(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # User unauthorized
        user.mt_credential.setAuthorization('send', False)
        response_text, response_code = yield self.run_test(user, group)
        self.assertEqual(response_text, 'Error "Authorization failed for username [u1] (Can not send MT messages)."')
        self.assertEqual(response_code, '400 Bad Request')
    
    @defer.inlineCallbacks
    def test_authorized_user_send(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # User authorized and group is not authorized
        user.mt_credential.setAuthorization('send', True)
        response_text, response_code = yield self.run_test(user, group)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_long_content(self):
        # User have default authorization to send long content
        response_text, response_code = yield self.run_test(self.user1, self.group1, content = 'X' * 300)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
    @defer.inlineCallbacks
    def test_unauthorized_user_long_content(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # User unauthorized
        user.mt_credential.setAuthorization('long_content', False)
        response_text, response_code = yield self.run_test(user, group, content = 'X' * 300)
        self.assertEqual(response_text, 'Error "Authorization failed for username [u1] (Long content is not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_authorized_user_long_content(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # User authorized and group is not authorized
        user.mt_credential.setAuthorization('long_content', True)
        response_text, response_code = yield self.run_test(user, group, content = 'X' * 300)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_set_dlr_level(self):
        # User have default authorization to set dlr level
        response_text, response_code = yield self.run_test(self.user1, self.group1, dlr_level = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_unauthorized_set_dlr_level(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # User unauthorized
        user.mt_credential.setAuthorization('set_dlr_level', False)
        response_text, response_code = yield self.run_test(user, group, dlr_level = 3)
        self.assertEqual(response_text, 'Error "Authorization failed for username [u1] (Setting dlr level is not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_authorized_user_set_dlr_level(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # User authorized and group is not authorized
        user.mt_credential.setAuthorization('set_dlr_level', True)
        response_text, response_code = yield self.run_test(user, group, dlr_level = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_set_dlr_method(self):
        # User have default authorization to set dlr method
        response_text, response_code = yield self.run_test(self.user1, self.group1, dlr_method = 'post')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_unauthorized_set_dlr_method(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # User unauthorized
        user.mt_credential.setAuthorization('set_dlr_method', False)
        response_text, response_code = yield self.run_test(user, group, dlr_method = 'post')
        self.assertEqual(response_text, 'Error "Authorization failed for username [u1] (Setting dlr method is not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_authorized_user_set_dlr_method(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # User authorized and group is not authorized
        user.mt_credential.setAuthorization('set_dlr_method', True)
        response_text, response_code = yield self.run_test(user, group, dlr_method = 'post')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_set_source_address(self):
        # User have default authorization to set source address
        response_text, response_code = yield self.run_test(self.user1, self.group1, source_address = 'JASMINTEST')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_unauthorized_set_source_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # User unauthorized
        user.mt_credential.setAuthorization('set_source_address', False)
        response_text, response_code = yield self.run_test(user, group, source_address = 'JASMINTEST')
        self.assertEqual(response_text, 'Error "Authorization failed for username [u1] (Setting source address is not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_authorized_user_set_source_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # User authorized and group is not authorized
        user.mt_credential.setAuthorization('set_source_address', True)
        response_text, response_code = yield self.run_test(user, group, source_address = 'JASMINTEST')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_set_priority(self):
        # User have default authorization to set message priority
        response_text, response_code = yield self.run_test(self.user1, self.group1, priority = 2)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_unauthorized_set_priority(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # User unauthorized
        user.mt_credential.setAuthorization('set_priority', False)
        response_text, response_code = yield self.run_test(user, group, priority = 2)
        self.assertEqual(response_text, 'Error "Authorization failed for username [u1] (Setting priority is not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_authorized_user_set_priority(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # User authorized and group is not authorized
        user.mt_credential.setAuthorization('set_priority', True)
        response_text, response_code = yield self.run_test(user, group, priority = 2)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

class ValueFiltersTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_destination_address(self):
        # User have default value filter
        response_text, response_code = yield self.run_test(self.user1, self.group1)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_invalid_destination_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # Invalid filter (user-level)
        user.mt_credential.setValueFilter('destination_address', r'^2.*')
        response_text, response_code = yield self.run_test(user, group, destination_address = '1200')
        self.assertEqual(response_text, 'Error "Value filter failed for username [u1] (destination_address filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_valid_user_destination_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # Valid filter (user-level) with user-level invalid filter
        user.mt_credential.setValueFilter('destination_address', r'^1200$')
        response_text, response_code = yield self.run_test(user, group, destination_address = '1200')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_source_address(self):
        # User have default value filter
        response_text, response_code = yield self.run_test(self.user1, self.group1, source_address = 'JasminTest')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_invalid_source_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # Invalid filter (user-level)
        user.mt_credential.setValueFilter('source_address', r'^2.*')
        response_text, response_code = yield self.run_test(user, group, source_address = 'JasminTest')
        self.assertEqual(response_text, 'Error "Value filter failed for username [u1] (source_address filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_valid_user_source_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # Valid filter (user-level) with user-level invalid filter
        user.mt_credential.setValueFilter('source_address', r'^JasminTest')
        response_text, response_code = yield self.run_test(user, group, source_address = 'JasminTest')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_priority(self):
        # User have default value filter
        response_text, response_code = yield self.run_test(self.user1, self.group1, priority = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_invalid_priority(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # Invalid filter (user-level)
        user.mt_credential.setValueFilter('priority', r'^[0-2]$')
        response_text, response_code = yield self.run_test(user, group, priority = 3)
        self.assertEqual(response_text, 'Error "Value filter failed for username [u1] (priority filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_valid_user_priority(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # Valid filter (user-level) with user-level invalid filter
        user.mt_credential.setValueFilter('priority', r'^[0-3]$')
        response_text, response_code = yield self.run_test(user, group, priority = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_default_content(self):
        # User have default value filter
        response_text, response_code = yield self.run_test(self.user1, self.group1)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

    @defer.inlineCallbacks
    def test_invalid_content(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # Invalid filter (user-level)
        user.mt_credential.setValueFilter('content', r'^fixed_content$')
        response_text, response_code = yield self.run_test(user, group, content = 'any content')
        self.assertEqual(response_text, 'Error "Value filter failed for username [u1] (content filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')
        
    @defer.inlineCallbacks
    def test_valid_user_content(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # Valid filter (user-level) with user-level invalid filter
        user.mt_credential.setValueFilter('content', r'.*')
        response_text, response_code = yield self.run_test(user, group, content = 'any content')
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

class DefaultValuesTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_source_address(self):
        # User have no default source address
        response_text, response_code = yield self.run_test(self.user1, self.group1)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
        self.assertEqual(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr'], '')

    @defer.inlineCallbacks
    def test_undefined_source_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)

        # Defining default value in group-level
        user.mt_credential.setDefaultValue('source_address', None)
        response_text, response_code = yield self.run_test(user, group)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

        self.assertEqual(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr'], '')

    @defer.inlineCallbacks
    def test_defined_source_address(self):
        user = copy.copy(self.user1)
        group = copy.copy(self.group1)
        
        # Defining default value in user-level
        user.mt_credential.setDefaultValue('source_address', 'JASMINTEST')
        response_text, response_code = yield self.run_test(user, group)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

        self.assertEqual(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr'], 'JASMINTEST')

class QuotasTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_unrated_route(self):
        """
        Default quotas, everything is unlimited
        """

        # Send default SMS
        response_text, response_code = yield self.run_test(self.user1, self.group1)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        # User quotas still unlimited
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), None)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), None)
        remote_group = remote_user.group
        # Group quotas still unlimited
        self.assertEqual(remote_group.mt_credential.getQuota('balance'), None)
        self.assertEqual(remote_group.mt_credential.getQuota('submit_sm_count'), None)
        
    @defer.inlineCallbacks
    def test_unrated_route_limited_quotas(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', 10)
        user.mt_credential.setQuota('submit_sm_count', 10)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), 9)

    @defer.inlineCallbacks
    def test_unrated_route_unlimited_quotas(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', None)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), None)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), None)

    @defer.inlineCallbacks
    def test_rated_route_limited_quotas(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', 10)
        user.mt_credential.setQuota('submit_sm_count', 10)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group, default_route = route)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 8.8)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), 9)

    @defer.inlineCallbacks
    def test_rated_route_unlimited_quotas(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', None)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group, default_route = route)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)
        
        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), None)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), None)

    @defer.inlineCallbacks
    def test_rated_route_insufficient_balance(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', 1.1)
        user.mt_credential.setQuota('submit_sm_count', None)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group, default_route = route)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_unrated_route_insufficient_submit_sm_count(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', 0)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_rated_route_insufficient_submit_sm_count(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', 0)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group, default_route = route)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_rated_route_early_decrement_balance_percent_insufficient_balance(self):
        '''Balance is greater than the early_decrement_balance_percent but lower than the final rate, 
        user must not be charged in this case, he have to get a balance covering the total rate'''
        
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', 1)
        user.mt_credential.setQuota('early_decrement_balance_percent', 25)
        route = DefaultRoute(self.c1, rate = 2.0)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group, default_route = route)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')
    
    @defer.inlineCallbacks
    def test_rated_route_early_decrement_balance_percent(self):
        group = Group(1)
        user = User('1', group, 'u1', 'password')
        user.mt_credential.setQuota('balance', 10)
        user.mt_credential.setQuota('early_decrement_balance_percent', 25)
        route = DefaultRoute(self.c1, rate = 2.0)

        # Send default SMS
        response_text, response_code = yield self.run_test(user, group, default_route = route)
        self.assertEqual(response_text[:7], 'Success')
        self.assertTrue(response_code)

        # Assert quotas after SMS is sent and before receiving a submit_sm_resp
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 
                         user.mt_credential.getQuota('balance') - (2.0*25/100))

        # Assert quotas after SMS is sent and after receiving a submit_sm_resp
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 
                         user.mt_credential.getQuota('balance') - 2.0)