"""
Test cases for amqp contents
"""

import pickle
from datetime import datetime

from twisted.trial.unittest import TestCase

from jasmin.managers.content import (SubmitSmContent, SubmitSmRespContent,
                                     DeliverSmContent, SubmitSmRespBillContent,
                                     DLRContentForHttpapi, DLRContentForSmpps,
                                     DLR, InvalidParameterError)
from jasmin.routing.jasminApi import *
from smpp.pdu.pdu_types import AddrTon, AddrNpi, CommandId, CommandStatus


class ContentTestCase(TestCase):
    body = 'TESTBODY'
    replyto = 'any.route'
    expiration = datetime.today().strftime('%Y-%m-%d %H:%M:%S')
    bill = User('foo', Group('test'), 'foo', 'bar')


class SubmitSmContentTestCase(ContentTestCase):
    def test_normal(self):
        c = SubmitSmContent(1, self.body, self.replyto, self.bill, 1, self.expiration)

        self.assertEqual(c.body, self.body)
        self.assertEqual(c['reply-to'], self.replyto)
        self.assertEqual(c['priority'], 1)
        self.assertEqual(c['headers']['expiration'], self.expiration)
        self.assertEqual(c['headers']['submit_sm_bill'], self.bill)
        self.assertEqual(c['headers']['source_connector'], 'httpapi')
        self.assertNotEqual(c['message-id'], None)
        self.assertTrue('created_at' in c['headers'])

    def test_minimal_arguments(self):
        c = SubmitSmContent(1, self.body, self.replyto, self.bill)

        self.assertEqual(c['priority'], 1)
        self.assertNotEqual(c['message-id'], None)

    def test_unique_messageid(self):
        counter = 0
        maxCounter = 10000
        msgIds = []
        while 1:
            if counter == maxCounter:
                break
            else:
                counter += 1

            c = SubmitSmContent(1, self.body, self.replyto, self.bill)
            self.assertEqual(msgIds.count(c['message-id']), 0,
                              "Collision detected at position %s/%s" % (counter, maxCounter))
            msgIds.append(c['message-id'])

    def test_set_incorrect_source_connector(self):
        self.assertRaises(InvalidParameterError, SubmitSmContent, 1, self.body, self.replyto,
                          self.bill, source_connector='anythingelse')


class SubmitSmRespContentTestCase(ContentTestCase):
    def test_normal_nopickling(self):
        c = SubmitSmRespContent(self.body, 1, prePickle=False)

        self.assertEqual(c.body, self.body)
        self.assertEqual(c['message-id'], 1)
        self.assertTrue('created_at' in c['headers'])

    def test_normal_pickling(self):
        c = SubmitSmRespContent(self.body, 1)

        self.assertNotEqual(c.body, self.body)
        self.assertEqual(c.body, pickle.dumps(self.body, pickle.HIGHEST_PROTOCOL))
        self.assertEqual(c['message-id'], 1)
        self.assertTrue('created_at' in c['headers'])


class DLRTestCase(ContentTestCase):
    def test_deliversm_and_datasm(self):
        for pdu_type in [CommandId.deliver_sm, CommandId.data_sm]:
            c = DLR(pdu_type=pdu_type, msgid=1, status=CommandStatus.ESME_ROK, cid='test', dlr_details={'some': 'detail'})

            self.assertEqual(CommandStatus.ESME_ROK.name, c.body)
            self.assertEqual('1', c.properties['message-id'])
            self.assertEqual(pdu_type.name, c.properties['headers']['type'])
            self.assertEqual('test', c.properties['headers']['cid'])
            self.assertEqual('detail', c.properties['headers']['dlr_some'])

            # Exceptions:
            self.assertRaises(InvalidParameterError, DLR, pdu_type=pdu_type, msgid=1, status=CommandStatus.ESME_ROK)
            self.assertRaises(InvalidParameterError, DLR, pdu_type=pdu_type, msgid=1, status=CommandStatus.ESME_ROK,
                              dlr_details={'some': 'detail'})
            self.assertRaises(InvalidParameterError, DLR, pdu_type=pdu_type, msgid=1, status=CommandStatus.ESME_ROK, cid='test')

    def test_submitsmresp(self):
        # Successful submit_sm_resp
        c_success = DLR(pdu_type=CommandId.submit_sm_resp, msgid=1, status=CommandStatus.ESME_ROK, smpp_msgid=b'2')
        # Errored submit_sm_resp
        c_errored = DLR(pdu_type=CommandId.submit_sm_resp, msgid=3, status=CommandStatus.ESME_RINVPARLEN)

        self.assertEqual('ESME_ROK', c_success.body)
        self.assertEqual('1', c_success.properties['message-id'])
        self.assertEqual(CommandId.submit_sm_resp.name, c_success.properties['headers']['type'])
        self.assertEqual('2', c_success.properties['headers']['smpp_msgid'])
        self.assertEqual('ESME_RINVPARLEN', c_errored.body)
        self.assertEqual('3', c_errored.properties['message-id'])
        self.assertEqual(CommandId.submit_sm_resp.name, c_errored.properties['headers']['type'])

        # Exceptions:
        self.assertRaises(InvalidParameterError, DLR, pdu_type=CommandId.submit_sm_resp, msgid=1, status=CommandStatus.ESME_ROK)

    def test_invalid_pdu_type(self):
        self.assertRaises(InvalidParameterError, DLR, pdu_type=CommandId.enquire_link, msgid=1, status=CommandStatus.ESME_ROK)


class DLRContentForHttpapiTestCase(ContentTestCase):
    def test_normal_level1(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 1
        dlr_connector = 'test_cid'
        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, dlr_level, dlr_connector)

        self.assertEqual(c.body, msgid)
        self.assertEqual(len(c['headers']), 13)
        self.assertEqual(c['headers']['try-count'], 0)
        self.assertEqual(c['headers']['url'], dlr_url)
        self.assertEqual(c['headers']['level'], dlr_level)
        self.assertEqual(c['headers']['message_status'], 'DELIVRD')
        self.assertEqual(c['headers']['id_smsc'], '')
        self.assertEqual(c['headers']['sub'], '')
        self.assertEqual(c['headers']['dlvrd'], '')
        self.assertEqual(c['headers']['subdate'], '')
        self.assertEqual(c['headers']['donedate'], '')
        self.assertEqual(c['headers']['err'], '')
        self.assertEqual(c['headers']['text'], '')
        self.assertEqual(c['headers']['method'], 'POST')
        self.assertEqual(c['headers']['connector'], dlr_connector)

    def test_normal_level2(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 2
        dlr_connector = 'test_cid'
        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, dlr_level, dlr_connector, id_smsc='abc', sub='3',
                                 dlvrd='3', subdate='anydate', donedate='anydate', err='', text='Any text')

        self.assertEqual(c.body, msgid)
        self.assertEqual(len(c['headers']), 13)
        self.assertEqual(c['headers']['try-count'], 0)
        self.assertEqual(c['headers']['url'], dlr_url)
        self.assertEqual(c['headers']['level'], dlr_level)
        self.assertEqual(c['headers']['message_status'], 'DELIVRD')
        self.assertEqual(c['headers']['id_smsc'], 'abc')
        self.assertEqual(c['headers']['sub'], '3')
        self.assertEqual(c['headers']['dlvrd'], '3')
        self.assertEqual(c['headers']['subdate'], 'anydate')
        self.assertEqual(c['headers']['donedate'], 'anydate')
        self.assertEqual(c['headers']['err'], '')
        self.assertEqual(c['headers']['text'], 'Any text')
        self.assertEqual(c['headers']['method'], 'POST')
        self.assertEqual(c['headers']['connector'], dlr_connector)

    def test_normal_level3(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 3
        dlr_connector = 'test_cid'
        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, dlr_level, dlr_connector, id_smsc='abc', sub='3',
                                 dlvrd='3', subdate='anydate', donedate='anydate', err='', text='Any text')

        self.assertEqual(c.body, msgid)
        self.assertEqual(len(c['headers']), 13)
        self.assertEqual(c['headers']['try-count'], 0)
        self.assertEqual(c['headers']['url'], dlr_url)
        self.assertEqual(c['headers']['level'], dlr_level)
        self.assertEqual(c['headers']['message_status'], 'DELIVRD')
        self.assertEqual(c['headers']['id_smsc'], 'abc')
        self.assertEqual(c['headers']['sub'], '3')
        self.assertEqual(c['headers']['dlvrd'], '3')
        self.assertEqual(c['headers']['subdate'], 'anydate')
        self.assertEqual(c['headers']['donedate'], 'anydate')
        self.assertEqual(c['headers']['err'], '')
        self.assertEqual(c['headers']['text'], 'Any text')
        self.assertEqual(c['headers']['method'], 'POST')
        self.assertEqual(c['headers']['connector'], dlr_connector)

    def test_message_status(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 1
        dlr_connector = 'test_cid'

        validStatuses = ['DELIVRD', 'EXPIRED', 'DELETED',
                         'UNDELIV', 'ACCEPTD', 'UNKNOWN', 'REJECTD', 'ESME_ANYTHING']

        for status in validStatuses:
            c = DLRContentForHttpapi(status, msgid, dlr_url, dlr_level, dlr_connector)

            self.assertEqual(c['headers']['message_status'], status)

        self.assertRaises(InvalidParameterError, DLRContentForHttpapi, 'anystatus', msgid, dlr_url, dlr_level, dlr_connector)

    def test_level(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_connector = 'test_cid'

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1)
        self.assertEqual(c['headers']['level'], 1)
        self.assertRaises(InvalidParameterError, DLRContentForHttpapi, 'DELIVRD', msgid, dlr_url, 45, dlr_connector)

    def test_method(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_connector = 'test_cid'

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1, dlr_connector)
        self.assertEqual(c['headers']['method'], 'POST')

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1, dlr_connector, method='GET')
        self.assertEqual(c['headers']['method'], 'GET')

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1, dlr_connector, method='POST')
        self.assertEqual(c['headers']['method'], 'POST')

        self.assertRaises(InvalidParameterError, DLRContentForHttpapi, 'DELIVRD', msgid, dlr_url, 1, dlr_connector,
                          method='ANY METHOD')


class DLRContentForSmppsTestCase(ContentTestCase):
    def test_normal_without_err(self):
        message_status = 'DELIVRD'
        msgid = 'msgid'
        system_id = 'username'
        source_addr = '999'
        destination_addr = '111'
        sub_date = datetime.now()
        source_addr_ton = AddrTon.NATIONAL
        source_addr_npi = AddrNpi.ISDN
        dest_addr_ton = AddrTon.NATIONAL
        dest_addr_npi = AddrNpi.ISDN

        c = DLRContentForSmpps(message_status, msgid, system_id, source_addr, destination_addr, sub_date,
                               source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi)

        self.assertEqual(c.body, msgid)
        self.assertEqual(len(c['headers']), 11)
        self.assertEqual(c['headers']['try-count'], 0)
        self.assertEqual(c['headers']['message_status'], message_status)
        self.assertEqual(c['headers']['system_id'], system_id)
        self.assertEqual(c['headers']['source_addr'], source_addr)
        self.assertEqual(c['headers']['destination_addr'], destination_addr)
        self.assertEqual(c['headers']['sub_date'], str(sub_date))
        self.assertEqual(c['headers']['source_addr_ton'], str(AddrTon.NATIONAL))
        self.assertEqual(c['headers']['source_addr_npi'], str(AddrNpi.ISDN))
        self.assertEqual(c['headers']['dest_addr_ton'], str(AddrTon.NATIONAL))
        self.assertEqual(c['headers']['dest_addr_npi'], str(AddrNpi.ISDN))
        # Default value of err is 99
        self.assertEqual(c['headers']['err'], 99)

    def test_normal_with_err(self):
        message_status = 'DELIVRD'
        msgid = 'msgid'
        system_id = 'username'
        source_addr = '999'
        destination_addr = '111'
        sub_date = datetime.now()
        source_addr_ton = AddrTon.NATIONAL
        source_addr_npi = AddrNpi.ISDN
        dest_addr_ton = AddrTon.NATIONAL
        dest_addr_npi = AddrNpi.ISDN
        err = 56

        c = DLRContentForSmpps(message_status, msgid, system_id, source_addr, destination_addr, sub_date,
                               source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi, err=err)

        self.assertEqual(len(c['headers']), 11)
        self.assertEqual(c['headers']['err'], err)

    def test_message_status(self):
        msgid = 'msgid'
        system_id = 'username'
        source_addr = '999'
        destination_addr = '111'
        sub_date = datetime.now()
        source_addr_ton = AddrTon.NATIONAL
        source_addr_npi = AddrNpi.ISDN
        dest_addr_ton = AddrTon.NATIONAL
        dest_addr_npi = AddrNpi.ISDN

        validStatuses = ['ESME_ROK', 'DELIVRD', 'EXPIRED', 'DELETED',
                         'UNDELIV', 'ACCEPTD', 'UNKNOWN', 'REJECTD', 'ESME_ANYTHING']

        for status in validStatuses:
            c = DLRContentForSmpps(status, msgid, system_id, source_addr, destination_addr, sub_date,
                                   source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi)

            self.assertEqual(c['headers']['message_status'], status)

        self.assertRaises(InvalidParameterError, DLRContentForSmpps,
                          'anystatus', msgid, system_id, source_addr, destination_addr, sub_date,
                          source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi)


class DeliverSmContentTestCase(ContentTestCase):
    def test_normal_nopickling(self):
        c = DeliverSmContent(self.body, 'connector1', prePickle=False)

        self.assertEqual(c.body, self.body)
        self.assertEqual(c['headers']['connector-id'], 'connector1')
        self.assertEqual(c['headers']['concatenated'], False)
        self.assertFalse(c['message-id'] == None)
        self.assertTrue('created_at' in c['headers'])

    def test_normal_pickling(self):
        c = DeliverSmContent(self.body, 'connector1')

        self.assertNotEqual(c.body, self.body)
        self.assertEqual(c.body, pickle.dumps(self.body, pickle.HIGHEST_PROTOCOL))
        self.assertEqual(c['headers']['connector-id'], 'connector1')
        self.assertEqual(c['headers']['concatenated'], False)
        self.assertFalse(c['message-id'] == None)
        self.assertTrue('created_at' in c['headers'])

    def test_headers_concatenated(self):
        c = DeliverSmContent(self.body, 'connector1', prePickle=False, concatenated=True)

        self.assertEqual(c['headers']['concatenated'], True)
        self.assertFalse(c['message-id'] == None)
        self.assertTrue('created_at' in c['headers'])


class SubmitSmRespBillContentTestCase(ContentTestCase):
    def test_normal(self):
        c = SubmitSmRespBillContent('bid', 'uid', 1.2)

        self.assertEqual(c['headers']['user-id'], 'uid')
        self.assertEqual(c['headers']['amount'], str(1.2))
        self.assertEqual(c['message-id'], 'bid')

    def test_amount_type(self):
        self.assertRaises(InvalidParameterError, SubmitSmRespBillContent, 'bid', 'uid', 'a')
        self.assertRaises(InvalidParameterError, SubmitSmRespBillContent, 'bid', 'uid', '1')
        self.assertRaises(InvalidParameterError, SubmitSmRespBillContent, 'bid', 'uid', -1)
