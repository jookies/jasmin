# -*- coding: utf-8 -*-

import cPickle as pickle
import copy
import time
import urllib
import mock
import json
from datetime import datetime
from twisted.internet import reactor
from twisted.web.client import getPage
from twisted.internet import defer
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router import HappySMSCTestCase
from jasmin.routing.jasminApi import User, Group, SmppClientConnector
from jasmin.routing.Routes import DefaultRoute
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.test.smsc_simulator import *

@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred

class CredentialsTestCases(RouterPBProxy, HappySMSCTestCase):
    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)

        # Init
        self.group1 = Group('g1')
        self.user1 = User('1', self.group1, 'u1', 'password')
        self.c1 = SmppClientConnector('smpp_c1')

    @defer.inlineCallbacks
    def prepareRoutingsAndStartConnector(self, user = None, default_route = None, side_effect = None):
        # Routing stuff
        yield self.group_add(self.group1)

        if user is None:
            user = self.user1
        yield self.user_add(user)
        if default_route is None:
            yield self.mtroute_add(DefaultRoute(self.c1), 0)
        else:
            yield self.mtroute_add(default_route, 0)

        # Now we'll create the connecter
        yield self.SMPPClientManagerPBProxy.connect('127.0.0.1', self.CManagerPort)
        c1Config = SMPPClientConfig(id=self.c1.cid,
            port = self.SMSCPort.getHost().port,
            bindOperation = 'transceiver',
            submit_sm_throughput = 0,
            )
        yield self.SMPPClientManagerPBProxy.add(c1Config)

        # Start the connector
        yield self.SMPPClientManagerPBProxy.start(self.c1.cid)
        # Wait for 'BOUND_TRX' state
        while True:
            ssRet = yield self.SMPPClientManagerPBProxy.session_state(self.c1.cid)
            if ssRet[:6] == 'BOUND_':
                break;
            else:
                yield waitFor(0.2)

        # Install mock
        self.SMSCPort.factory.lastClient.sendSubmitSmResponse = mock.Mock(wraps = self.SMSCPort.factory.lastClient.sendSubmitSmResponse,
                                                                          side_effect = side_effect)

        # Configuration
        self.method = 'GET'
        self.postdata = None
        self.params = {'to': '06155423',
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
                yield waitFor(0.2)

    @defer.inlineCallbacks
    def run_send_test(self, user = None, content = 'anycontent',
                 dlr_level = None, dlr_method = None, source_address = None,
                 priority = None, validity_period = None, destination_address = None,
                 default_route = None, side_effect = None):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(user, default_route, side_effect)

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
        if validity_period is not None:
            self.params['validity-period'] = validity_period
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

        # Wait 5 seconds before stopping SmppClientConnectors
        yield waitFor(5)
        yield self.stopSmppClientConnectors()

        defer.returnValue((response_text, response_code))

    @defer.inlineCallbacks
    def run_balance_test(self, user = None, default_route = None, side_effect = None):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(user, default_route, side_effect)

        # Set baseurl
        params = {'username': self.params['username'],
                  'password': self.params['password'],}
        baseurl = 'http://127.0.0.1:%s/balance?%s' % (1401, urllib.urlencode(params))

        # Send a balance check request
        try:
            response_text = yield getPage(baseurl)
            response_code = 'Success'
        except Exception, error:
            response_text = error.response
            response_code = str(error)

        # Wait 5 seconds before stopping SmppClientConnectors
        yield waitFor(5)
        yield self.stopSmppClientConnectors()

        defer.returnValue((response_text, response_code))

    @defer.inlineCallbacks
    def run_rate_test(self, user = None, content = None,
                 source_address = None, destination_address = None,
                 default_route = None, side_effect = None):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(user, default_route, side_effect)

        # Set content
        if content is not None:
            self.params['content'] = content
        else:
            del self.params['content']
        if source_address is not None:
            self.params['from'] = source_address
        if destination_address is not None:
            self.params['to'] = destination_address
        baseurl = 'http://127.0.0.1:%s/rate?%s' % (1401, urllib.urlencode(self.params))

        # Send a MT
        # We should receive a msg id
        try:
            response_text = yield getPage(baseurl)
            response_code = 'Success'
        except Exception, error:
            response_text = error.response
            response_code = str(error)

        # Wait 5 seconds before stopping SmppClientConnectors
        yield waitFor(5)
        yield self.stopSmppClientConnectors()

        defer.returnValue((response_text, response_code))

class AuthorizationsTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_send(self):
        # User have default authorization to send an sms
        response_text, response_code = yield self.run_send_test()
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_user_send(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_send', False)

        # User unauthorized
        response_text, response_code = yield self.run_send_test()
        self.assertEqual(response_text, 'Error "Authorization failed for user [u1] (Cannot send MT messages)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_send(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_send', True)

        # User authorized
        response_text, response_code = yield self.run_send_test()
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_balance(self):
        # User have default authorization to check balance
        response_text, response_code = yield self.run_balance_test()
        self.assertEqual(json.loads(response_text), {"balance": "ND", "sms_count": "ND"})
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_user_balance(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_balance', False)

        # User unauthorized
        response_text, response_code = yield self.run_balance_test()
        self.assertEqual(response_text, '"Authorization failed for user [u1] (Cannot check balance)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_balance(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_balance', True)

        # User authorized
        response_text, response_code = yield self.run_balance_test()
        self.assertEqual(json.loads(response_text), {"balance": "ND", "sms_count": "ND"})
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_rate(self):
        # User have default authorization to check route rate
        response_text, response_code = yield self.run_rate_test()
        self.assertEqual(json.loads(response_text), {u'submit_sm_count': 1, u'unit_rate': 0.0})
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_user_rate(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_rate', False)

        # User unauthorized
        response_text, response_code = yield self.run_rate_test()
        self.assertEqual(response_text, '"Authorization failed for user [u1] (Cannot check rate)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_rate(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_rate', True)

        # User authorized
        response_text, response_code = yield self.run_rate_test()
        self.assertEqual(json.loads(response_text), {u'submit_sm_count': 1, u'unit_rate': 0.0})
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_long_content(self):
        # User have default authorization to send long content
        response_text, response_code = yield self.run_send_test(content = 'X' * 300)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_user_long_content(self):
        user = copy.copy(self.user1)

        # User unauthorized
        user.mt_credential.setAuthorization('http_long_content', False)
        response_text, response_code = yield self.run_send_test(content = 'X' * 300)
        self.assertEqual(response_text, 'Error "Authorization failed for user [u1] (Long content not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_long_content(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_long_content', True)

        # User authorized
        response_text, response_code = yield self.run_send_test(content = 'X' * 300)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_set_dlr_level(self):
        # User have default authorization to set dlr level
        response_text, response_code = yield self.run_send_test(dlr_level = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_set_dlr_level(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_dlr_level', False)

        # User unauthorized
        response_text, response_code = yield self.run_send_test(dlr_level = 3)
        self.assertEqual(response_text, 'Error "Authorization failed for user [u1] (Setting dlr level not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_set_dlr_level(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_dlr_level', True)

        # User authorized
        response_text, response_code = yield self.run_send_test(dlr_level = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_set_dlr_method(self):
        # User have default authorization to set dlr method
        response_text, response_code = yield self.run_send_test(dlr_method = 'post')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_set_dlr_method(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_set_dlr_method', False)

        # User unauthorized
        response_text, response_code = yield self.run_send_test(dlr_method = 'post')
        self.assertEqual(response_text, 'Error "Authorization failed for user [u1] (Setting dlr method not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_set_dlr_method(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('http_set_dlr_method', True)

        # User authorized
        response_text, response_code = yield self.run_send_test(dlr_method = 'post')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_set_source_address(self):
        # User have default authorization to set source address
        response_text, response_code = yield self.run_send_test(source_address = 'JASMINTEST')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_set_source_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_source_address', False)

        # User unauthorized
        response_text, response_code = yield self.run_send_test(source_address = 'JASMINTEST')
        self.assertEqual(response_text, 'Error "Authorization failed for user [u1] (Setting source address not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_set_source_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_source_address', True)

        # User authorized
        response_text, response_code = yield self.run_send_test(user = user, source_address = 'JASMINTEST')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_set_priority(self):
        # User have default authorization to set message priority
        response_text, response_code = yield self.run_send_test(priority = 2)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_set_priority(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_priority', False)

        # User unauthorized
        response_text, response_code = yield self.run_send_test(user = user, priority = 2)
        self.assertEqual(response_text, 'Error "Authorization failed for user [u1] (Setting priority not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_set_priority(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_priority', True)

        # User authorized
        response_text, response_code = yield self.run_send_test(user = user, priority = 2)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_set_validity_period(self):
        # User have default authorization to set message validity_period
        response_text, response_code = yield self.run_send_test(validity_period = 2)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_unauthorized_set_validity_period(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_validity_period', False)

        # User unauthorized
        response_text, response_code = yield self.run_send_test(user = user, validity_period = 2)
        self.assertEqual(response_text, 'Error "Authorization failed for user [u1] (Setting validity period not authorized)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_authorized_user_set_validity_period(self):
        user = copy.copy(self.user1)
        user.mt_credential.setAuthorization('set_validity_period', True)

        # User authorized
        response_text, response_code = yield self.run_send_test(user = user, validity_period = 2)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

class ValueFiltersTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_destination_address(self):
        # User have default value filter
        response_text, response_code = yield self.run_send_test()
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_invalid_destination_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('destination_address', r'^2.*')

        # Invalid filter (user-level)
        response_text, response_code = yield self.run_send_test(user = user, destination_address = '1200')
        self.assertEqual(response_text, 'Error "Value filter failed for user [u1] (destination_address filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_valid_user_destination_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('destination_address', r'^1200$')

        # Valid filter (user-level) with user-level invalid filter
        response_text, response_code = yield self.run_send_test(user = user, destination_address = '1200')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_source_address(self):
        # User have default value filter
        response_text, response_code = yield self.run_send_test(source_address = 'JasminTest')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_invalid_source_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('source_address', r'^2.*')

        # Invalid filter (user-level)
        response_text, response_code = yield self.run_send_test(user = user, source_address = 'JasminTest')
        self.assertEqual(response_text, 'Error "Value filter failed for user [u1] (source_address filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_valid_user_source_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('source_address', r'^JasminTest')

        # Valid filter (user-level) with user-level invalid filter
        response_text, response_code = yield self.run_send_test(user = user, source_address = 'JasminTest')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_priority_success(self):
        # User have default value filter
        response_text, response_code = yield self.run_send_test(priority = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_priority_failure(self):
        # User have default value filter
        response_text, response_code = yield self.run_send_test(priority = 5)
        self.assertEqual(response_text, 'Error "Argument [priority] has an invalid value: [5]."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_invalid_priority(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('priority', r'^[0-2]$')

        # Invalid filter (user-level)
        response_text, response_code = yield self.run_send_test(user = user, priority = 3)
        self.assertEqual(response_text, 'Error "Value filter failed for user [u1] (priority filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_valid_priority(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('priority', r'^[0-3]$')

        # Valid filter (user-level) with user-level invalid filter
        response_text, response_code = yield self.run_send_test(user = user, priority = 3)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_invalid_validity_period(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('validity_period', r'^[0-1]?[0-9]$') # 0 .. 19

        # Invalid filter (user-level)
        response_text, response_code = yield self.run_send_test(user = user, validity_period = 21)
        self.assertEqual(response_text, 'Error "Value filter failed for user [u1] (validity_period filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_valid_validity_period(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('validity_period', r'^[0-2]?[0-9]$') # 0 .. 29

        # Valid filter (user-level) with user-level invalid filter
        response_text, response_code = yield self.run_send_test(user = user, validity_period = 21)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_default_content(self):
        # User have default value filter
        response_text, response_code = yield self.run_send_test()
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

    @defer.inlineCallbacks
    def test_invalid_content(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('content', r'^fixed_content$')

        # Invalid filter (user-level)
        response_text, response_code = yield self.run_send_test(user = user, content = 'any content')
        self.assertEqual(response_text, 'Error "Value filter failed for user [u1] (content filter mismatch)."')
        self.assertEqual(response_code, '400 Bad Request')

    @defer.inlineCallbacks
    def test_valid_user_content(self):
        user = copy.copy(self.user1)
        user.mt_credential.setValueFilter('content', r'.*')

        # Valid filter (user-level) with user-level invalid filter
        response_text, response_code = yield self.run_send_test(user = user, content = 'any content')
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

class DefaultValuesTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_source_address(self):
        # User have no default source address
        response_text, response_code = yield self.run_send_test()
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        self.assertEqual(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr'], '')

    @defer.inlineCallbacks
    def test_undefined_source_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setDefaultValue('source_address', None)

        # Default value undefined
        response_text, response_code = yield self.run_send_test(user = user)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        self.assertEqual(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr'], '')

    @defer.inlineCallbacks
    def test_defined_source_address(self):
        user = copy.copy(self.user1)
        user.mt_credential.setDefaultValue('source_address', 'JASMINTEST')

        # Defining default value in user-level
        response_text, response_code = yield self.run_send_test(user = user)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        self.assertEqual(self.SMSCPort.factory.lastClient.submitRecords[0].params['source_addr'], 'JASMINTEST')

class QuotasTestCases(CredentialsTestCases):
    @defer.inlineCallbacks
    def test_default_unrated_route(self):
        """
        Default quotas, everything is unlimited
        """

        # Send default SMS
        response_text, response_code = yield self.run_send_test()
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        # User quotas still unlimited
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), None)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), None)

    @defer.inlineCallbacks
    def test_unrated_route_limited_quotas(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 10)
        user.mt_credential.setQuota('submit_sm_count', 10)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), 9)

    @defer.inlineCallbacks
    def test_unrated_route_long_message(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 10)
        user.mt_credential.setQuota('submit_sm_count', 10)

        # Send long SMS
        response_text, response_code = yield self.run_send_test(user = user, content = 'X' * 400)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert quotas after long SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), 7)

    @defer.inlineCallbacks
    def test_unrated_route_unlimited_quotas(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', None)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), None)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), None)

    @defer.inlineCallbacks
    def test_rated_route_limited_quotas(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 10.0)
        user.mt_credential.setQuota('submit_sm_count', 10)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 8.8)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), 9)

    @defer.inlineCallbacks
    def test_rated_route_long_message(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 10.0)
        user.mt_credential.setQuota('submit_sm_count', 10)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send long SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route,
                                                           content = 'X' * 400)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert quotas after long SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 6.4)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), 7)

    @defer.inlineCallbacks
    def test_rated_route_unlimited_quotas(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', None)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert quotas after SMS is sent
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), None)
        self.assertEqual(remote_user.mt_credential.getQuota('submit_sm_count'), None)

    @defer.inlineCallbacks
    def test_rated_route_insufficient_balance(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 1.1)
        user.mt_credential.setQuota('submit_sm_count', None)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_rated_route_insufficient_balance_long_message(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 5.9)
        user.mt_credential.setQuota('submit_sm_count', None)
        route = DefaultRoute(self.c1, rate = 2.0)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route,
                                                           content = 'X' * 400)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_unrated_route_insufficient_submit_sm_count(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', 0)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_rated_route_insufficient_submit_sm_count(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', 0)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_unrated_route_insufficient_submit_sm_count_long_message(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', 2)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, content = 'X' * 400)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_rated_route_insufficient_submit_sm_count_long_message(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', None)
        user.mt_credential.setQuota('submit_sm_count', 2)
        route = DefaultRoute(self.c1, rate = 1.2)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route,
                                                           content = 'X' * 400)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_rated_route_early_decrement_balance_percent_insufficient_balance(self):
        '''Balance is greater than the early_decrement_balance_percent but lower than the final rate,
        user must not be charged in this case, he have to get a balance covering the total rate'''

        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 1.0)
        user.mt_credential.setQuota('early_decrement_balance_percent', 25)
        route = DefaultRoute(self.c1, rate = 2.0)

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route)
        self.assertEqual(response_text, 'Error "Cannot charge submit_sm, check RouterPB log file for details"')
        self.assertEqual(response_code, '403 Forbidden')

    @defer.inlineCallbacks
    def test_rated_route_early_decrement_balance_percent(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 10.0)
        user.mt_credential.setQuota('early_decrement_balance_percent', 25)
        route = DefaultRoute(self.c1, rate = 2.0)

        _QuotasTestCases = self
        @defer.inlineCallbacks
        def pre_submit_sm_resp(reqPDU):
            """
            Will get the user balance before sending back a submit_sm_resp
            """
            t = yield _QuotasTestCases.user_get_all()
            remote_user = pickle.loads(t)[0]
            # Before submit_sm_resp, user must be charged 25% of the route rate
            self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10 - (2.0*25/100))

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route,
                                                           side_effect = pre_submit_sm_resp)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert balance after receiving submit_sm_resp
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        # After submit_sm_resp, user must be charged 100% of the route rate
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10 - 2.0)

    @defer.inlineCallbacks
    def test_rated_route_early_decrement_balance_100_percent(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 10.0)
        user.mt_credential.setQuota('early_decrement_balance_percent', 100.0)
        route = DefaultRoute(self.c1, rate = 2.0)

        _QuotasTestCases = self
        @defer.inlineCallbacks
        def pre_submit_sm_resp(reqPDU):
            """
            Will get the user balance before sending back a submit_sm_resp
            """
            t = yield _QuotasTestCases.user_get_all()
            remote_user = pickle.loads(t)[0]
            # Before submit_sm_resp, user must be charged 100% of the route rate
            self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10 - (2.0))

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route,
                                                           side_effect = pre_submit_sm_resp)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert balance after receiving submit_sm_resp
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        # After submit_sm_resp, user must be charged 100% of the route rate
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10 - 2.0)

    @defer.inlineCallbacks
    def test_rated_route_early_decrement_balance_percent_long_message(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('balance', 10.0)
        user.mt_credential.setQuota('early_decrement_balance_percent', 25)
        route = DefaultRoute(self.c1, rate = 2.0)

        _QuotasTestCases = self
        @defer.inlineCallbacks
        def pre_submit_sm_resp(reqPDU):
            """
            Will get the user balance before sending back a submit_sm_resp
            """
            t = yield _QuotasTestCases.user_get_all()
            remote_user = pickle.loads(t)[0]
            # Before submit_sm_resp, user must be charged 25% of the route rate (x number of submit_sm parts)
            self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10 - ((2.0*25/100) * 3))

        # Send default SMS
        response_text, response_code = yield self.run_send_test(user = user, default_route = route,
                                                           side_effect = pre_submit_sm_resp,
                                                           content = 'X' * 400)
        self.assertEqual(response_text[:7], 'Success')
        self.assertEqual(response_code, 'Success')

        # Assert balance after receiving submit_sm_resp
        t = yield self.user_get_all()
        remote_user = pickle.loads(t)[0]
        # After submit_sm_resp, user must be charged 100% of the route rate (x number of submit_sm parts)
        self.assertEqual(remote_user.mt_credential.getQuota('balance'), 10 - (2.0 * 3))

    @defer.inlineCallbacks
    def test_throughput_limit_rejection(self):
        user = copy.copy(self.user1)
        user.mt_credential.setQuota('http_throughput', 2)
        route = DefaultRoute(self.c1, rate = 0.0)

        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector(user, route)

        # Set content
        self.params['content'] = 'Any Content'
        baseurl = 'http://127.0.0.1:%s/send?%s' % (1401, urllib.urlencode(self.params))

        # Send a bunch of MT messages
        # We should receive a msg id for success and error when throughput is exceeded
        start_time = datetime.now()
        throughput_exceeded_errors = 0
        request_counter = 0
        for x in range(5000):
            try:
                response_text = yield getPage(baseurl, method = self.method, postdata = self.postdata)
                response_code = 'Success'
            except Exception, error:
                response_text = error.response
                response_code = str(error)

            request_counter+= 1
            if response_code == '403 Forbidden' and response_text == 'Error "User throughput exceeded"':
                throughput_exceeded_errors+= 1
        end_time = datetime.now()

        # Wait 2 seconds before stopping SmppClientConnectors
        yield waitFor(2)
        yield self.stopSmppClientConnectors()

        # Asserts (tolerance of -/+ 3 messages)
        throughput = 1 / float(user.mt_credential.getQuota('http_throughput'))
        dt = end_time - start_time
        max_unsuccessfull_requests = request_counter - (dt.seconds / throughput)
        unsuccessfull_requests = throughput_exceeded_errors

        self.assertGreaterEqual(unsuccessfull_requests, max_unsuccessfull_requests - 3)
        self.assertLessEqual(unsuccessfull_requests, max_unsuccessfull_requests + 3)
