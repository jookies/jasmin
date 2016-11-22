"""
Test cases for amqp contents
"""

import cPickle as pickle
from datetime import datetime

from twisted.trial.unittest import TestCase

from jasmin.managers.content import (SubmitSmContent, SubmitSmRespContent,
                                     DeliverSmContent, SubmitSmRespBillContent,
                                     DLRContentForHttpapi, DLRContentForSmpps,
                                     DLR, InvalidParameterError)
from jasmin.routing.jasminApi import *
from jasmin.vendor.smpp.pdu.pdu_types import AddrTon, AddrNpi


class ContentTestCase(TestCase):
    body = 'TESTBODY'
    replyto = 'any.route'
    expiration = datetime.today().strftime('%Y-%m-%d %H:%M:%S')
    bill = User('foo', Group('test'), 'foo', 'bar')


class SubmitSmContentTestCase(ContentTestCase):
    def test_normal(self):
        c = SubmitSmContent(self.body, self.replyto, self.bill, 1, self.expiration)

        self.assertEquals(c.body, self.body)
        self.assertEquals(c['reply-to'], self.replyto)
        self.assertEquals(c['priority'], 1)
        self.assertEquals(c['headers']['expiration'], self.expiration)
        self.assertEquals(c['headers']['submit_sm_bill'], self.bill)
        self.assertEquals(c['headers']['source_connector'], 'httpapi')
        self.assertNotEquals(c['message-id'], None)
        self.assertTrue('created_at' in c['headers'])

    def test_minimal_arguments(self):
        c = SubmitSmContent(self.body, self.replyto, self.bill)

        self.assertEquals(c['priority'], 1)
        self.assertNotEquals(c['message-id'], None)

    def test_unique_messageid(self):
        counter = 0
        maxCounter = 10000
        msgIds = []
        while 1:
            if counter == maxCounter:
                break
            else:
                counter += 1

            c = SubmitSmContent(self.body, self.replyto, self.bill)
            self.assertEquals(msgIds.count(c['message-id']), 0,
                              "Collision detected at position %s/%s" % (counter, maxCounter))
            msgIds.append(c['message-id'])

    def test_set_incorrect_source_connector(self):
        self.assertRaises(InvalidParameterError, SubmitSmContent, self.body, self.replyto,
                          self.bill, source_connector='anythingelse')


class SubmitSmRespContentTestCase(ContentTestCase):
    def test_normal_nopickling(self):
        c = SubmitSmRespContent(self.body, 1, prePickle=False)

        self.assertEquals(c.body, self.body)
        self.assertEquals(c['message-id'], 1)
        self.assertTrue('created_at' in c['headers'])

    def test_normal_pickling(self):
        c = SubmitSmRespContent(self.body, 1)

        self.assertNotEquals(c.body, self.body)
        self.assertEquals(c.body, pickle.dumps(self.body, pickle.HIGHEST_PROTOCOL))
        self.assertEquals(c['message-id'], 1)
        self.assertTrue('created_at' in c['headers'])


class DLRTestCase(ContentTestCase):
    def test_deliversm_and_datasm(self):
        for pdu_type in ['deliver_sm', 'data_sm']:
            c = DLR(pdu_type=pdu_type, msgid=1, status='ESME_ROK', cid='test', dlr_details={'some': 'detail'})

            self.assertEqual('ESME_ROK', c.body)
            self.assertEqual('1', c.properties['message-id'])
            self.assertEqual(pdu_type, c.properties['headers']['type'])
            self.assertEqual('test', c.properties['headers']['cid'])
            self.assertEqual('detail', c.properties['headers']['dlr_some'])

            # Exceptions:
            self.assertRaises(InvalidParameterError, DLR, pdu_type=pdu_type, msgid=1, status='ESME_ROK')
            self.assertRaises(InvalidParameterError, DLR, pdu_type=pdu_type, msgid=1, status='ESME_ROK',
                              dlr_details={'some': 'detail'})
            self.assertRaises(InvalidParameterError, DLR, pdu_type=pdu_type, msgid=1, status='ESME_ROK', cid='test')

    def test_submitsmresp(self):
        # Successful submit_sm_resp
        c_success = DLR(pdu_type='submit_sm_resp', msgid=1, status='ESME_ROK', smpp_msgid=2)
        # Errored submit_sm_resp
        c_errored = DLR(pdu_type='submit_sm_resp', msgid=3, status='ESME_RINVPARLEN')

        self.assertEqual('ESME_ROK', c_success.body)
        self.assertEqual('1', c_success.properties['message-id'])
        self.assertEqual('submit_sm_resp', c_success.properties['headers']['type'])
        self.assertEqual('2', c_success.properties['headers']['smpp_msgid'])
        self.assertEqual('ESME_RINVPARLEN', c_errored.body)
        self.assertEqual('3', c_errored.properties['message-id'])
        self.assertEqual('submit_sm_resp', c_errored.properties['headers']['type'])

        # Exceptions:
        self.assertRaises(InvalidParameterError, DLR, pdu_type='submit_sm_resp', msgid=1, status='ESME_ROK')

    def test_invalid_pdu_type(self):
        self.assertRaises(InvalidParameterError, DLR, pdu_type='enquire_link', msgid=1, status='ESME_ROK')


class DLRContentForHttpapiTestCase(ContentTestCase):
    def test_normal_level1(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 1
        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, dlr_level)

        self.assertEquals(c.body, msgid)
        self.assertEquals(len(c['headers']), 12)
        self.assertEquals(c['headers']['try-count'], 0)
        self.assertEquals(c['headers']['url'], dlr_url)
        self.assertEquals(c['headers']['level'], dlr_level)
        self.assertEquals(c['headers']['message_status'], 'DELIVRD')
        self.assertEquals(c['headers']['id_smsc'], '')
        self.assertEquals(c['headers']['sub'], '')
        self.assertEquals(c['headers']['dlvrd'], '')
        self.assertEquals(c['headers']['subdate'], '')
        self.assertEquals(c['headers']['donedate'], '')
        self.assertEquals(c['headers']['err'], '')
        self.assertEquals(c['headers']['text'], '')
        self.assertEquals(c['headers']['method'], 'POST')

    def test_normal_level2(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 2
        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, dlr_level, id_smsc='abc', sub='3',
                                 dlvrd='3', subdate='anydate', donedate='anydate', err='', text='Any text')

        self.assertEquals(c.body, msgid)
        self.assertEquals(len(c['headers']), 12)
        self.assertEquals(c['headers']['try-count'], 0)
        self.assertEquals(c['headers']['url'], dlr_url)
        self.assertEquals(c['headers']['level'], dlr_level)
        self.assertEquals(c['headers']['message_status'], 'DELIVRD')
        self.assertEquals(c['headers']['id_smsc'], 'abc')
        self.assertEquals(c['headers']['sub'], '3')
        self.assertEquals(c['headers']['dlvrd'], '3')
        self.assertEquals(c['headers']['subdate'], 'anydate')
        self.assertEquals(c['headers']['donedate'], 'anydate')
        self.assertEquals(c['headers']['err'], '')
        self.assertEquals(c['headers']['text'], 'Any text')
        self.assertEquals(c['headers']['method'], 'POST')

    def test_normal_level3(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 3
        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, dlr_level, id_smsc='abc', sub='3',
                                 dlvrd='3', subdate='anydate', donedate='anydate', err='', text='Any text')

        self.assertEquals(c.body, msgid)
        self.assertEquals(len(c['headers']), 12)
        self.assertEquals(c['headers']['try-count'], 0)
        self.assertEquals(c['headers']['url'], dlr_url)
        self.assertEquals(c['headers']['level'], dlr_level)
        self.assertEquals(c['headers']['message_status'], 'DELIVRD')
        self.assertEquals(c['headers']['id_smsc'], 'abc')
        self.assertEquals(c['headers']['sub'], '3')
        self.assertEquals(c['headers']['dlvrd'], '3')
        self.assertEquals(c['headers']['subdate'], 'anydate')
        self.assertEquals(c['headers']['donedate'], 'anydate')
        self.assertEquals(c['headers']['err'], '')
        self.assertEquals(c['headers']['text'], 'Any text')
        self.assertEquals(c['headers']['method'], 'POST')

    def test_message_status(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 1

        validStatuses = ['DELIVRD', 'EXPIRED', 'DELETED',
                         'UNDELIV', 'ACCEPTD', 'UNKNOWN', 'REJECTD', 'ESME_ANYTHING']

        for status in validStatuses:
            c = DLRContentForHttpapi(status, msgid, dlr_url, dlr_level)

            self.assertEquals(c['headers']['message_status'], status)

        self.assertRaises(InvalidParameterError, DLRContentForHttpapi, 'anystatus', msgid, dlr_url, dlr_level)

    def test_level(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1)
        self.assertEquals(c['headers']['level'], 1)
        self.assertRaises(InvalidParameterError, DLRContentForHttpapi, 'DELIVRD', msgid, dlr_url, 45)

    def test_method(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1)
        self.assertEquals(c['headers']['method'], 'POST')

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1, method='GET')
        self.assertEquals(c['headers']['method'], 'GET')

        c = DLRContentForHttpapi('DELIVRD', msgid, dlr_url, 1, method='POST')
        self.assertEquals(c['headers']['method'], 'POST')

        self.assertRaises(InvalidParameterError, DLRContentForHttpapi, 'DELIVRD', msgid, dlr_url, 1,
                          method='ANY METHOD')


class DLRContentForSmppsTestCase(ContentTestCase):
    def test_normal(self):
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

        self.assertEquals(c.body, msgid)
        self.assertEquals(len(c['headers']), 10)
        self.assertEquals(c['headers']['try-count'], 0)
        self.assertEquals(c['headers']['message_status'], message_status)
        self.assertEquals(c['headers']['system_id'], system_id)
        self.assertEquals(c['headers']['source_addr'], source_addr)
        self.assertEquals(c['headers']['destination_addr'], destination_addr)
        self.assertEquals(c['headers']['sub_date'], str(sub_date))
        self.assertEquals(c['headers']['source_addr_ton'], AddrTon.NATIONAL)
        self.assertEquals(c['headers']['source_addr_npi'], AddrNpi.ISDN)
        self.assertEquals(c['headers']['dest_addr_ton'], AddrTon.NATIONAL)
        self.assertEquals(c['headers']['dest_addr_npi'], AddrNpi.ISDN)

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

            self.assertEquals(c['headers']['message_status'], status)

        self.assertRaises(InvalidParameterError, DLRContentForSmpps,
                          'anystatus', msgid, system_id, source_addr, destination_addr, sub_date,
                          source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi)


class DeliverSmContentTestCase(ContentTestCase):
    def test_normal_nopickling(self):
        c = DeliverSmContent(self.body, 'connector1', prePickle=False)

        self.assertEquals(c.body, self.body)
        self.assertEquals(c['headers']['connector-id'], 'connector1')
        self.assertEquals(c['headers']['concatenated'], False)
        self.assertFalse(c['message-id'] == None)
        self.assertTrue('created_at' in c['headers'])

    def test_normal_pickling(self):
        c = DeliverSmContent(self.body, 'connector1')

        self.assertNotEquals(c.body, self.body)
        self.assertEquals(c.body, pickle.dumps(self.body, pickle.HIGHEST_PROTOCOL))
        self.assertEquals(c['headers']['connector-id'], 'connector1')
        self.assertEquals(c['headers']['concatenated'], False)
        self.assertFalse(c['message-id'] == None)
        self.assertTrue('created_at' in c['headers'])

    def test_headers_concatenated(self):
        c = DeliverSmContent(self.body, 'connector1', prePickle=False, concatenated=True)

        self.assertEquals(c['headers']['concatenated'], True)
        self.assertFalse(c['message-id'] == None)
        self.assertTrue('created_at' in c['headers'])


class SubmitSmRespBillContentTestCase(ContentTestCase):
    def test_normal(self):
        c = SubmitSmRespBillContent('bid', 'uid', 1.2)

        self.assertEquals(c['headers']['user-id'], 'uid')
        self.assertEquals(c['headers']['amount'], str(1.2))
        self.assertEquals(c['message-id'], 'bid')

    def test_amount_type(self):
        self.assertRaises(InvalidParameterError, SubmitSmRespBillContent, 'bid', 'uid', 'a')
        self.assertRaises(InvalidParameterError, SubmitSmRespBillContent, 'bid', 'uid', '1')
        self.assertRaises(InvalidParameterError, SubmitSmRespBillContent, 'bid', 'uid', -1)
