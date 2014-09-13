"""
Test cases for amqp contents
"""

import pickle
from datetime import datetime
from twisted.trial.unittest import TestCase
from jasmin.managers.content import SubmitSmContent, SubmitSmRespContent
from jasmin.managers.content import DLRContent, InvalidParameterError, UndefinedParameterError

class ContentTestCase(TestCase):
    body = 'TESTBODY'
    replyto = 'any.route'
    expiration = datetime.today().strftime('%Y-%m-%d %H:%M:%S')

class SubmitSmContentTestCase(ContentTestCase):
    def test_normal(self):
        c = SubmitSmContent(self.body, self.replyto, 1, self.expiration)
        
        self.assertEquals(c.body, self.body)
        self.assertEquals(c['reply-to'], self.replyto)
        self.assertEquals(c['priority'], 1)
        self.assertEquals(c['headers']['expiration'], self.expiration)
        self.assertNotEquals(c['message-id'], None)
        
    def test_minimal_arguments(self):
        c = SubmitSmContent(self.body, self.replyto)
        
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
            
            c = SubmitSmContent(self.body, self.replyto)
            self.assertEquals(msgIds.count(c['message-id']), 0, "Collision detected at position %s/%s" % (counter, maxCounter))
            msgIds.append(c['message-id'])
            
class SubmitSmRespContentTestCase(ContentTestCase):
    def test_normal_nopickling(self):
        c = SubmitSmRespContent(self.body, 1, prePickle=False)
        
        self.assertEquals(c.body, self.body)
        self.assertEquals(c['message-id'], 1)
        
    def test_normal_pickling(self):
        c = SubmitSmRespContent(self.body, 1)
        
        self.assertNotEquals(c.body, self.body)
        self.assertEquals(c.body, pickle.dumps(self.body, 2))
        self.assertEquals(c['message-id'], 1)
        
class DLRContentTestCase(ContentTestCase):
    def test_normal_level1(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        dlr_level = 1
        c = DLRContent('DELIVRD', msgid, dlr_url, dlr_level)
        
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
        c = DLRContent('DELIVRD', msgid, dlr_url, dlr_level, id_smsc = 'abc', sub = '3', 
                 dlvrd = '3', subdate = 'anydate', donedate = 'anydate', err = '', text = 'Any text')
        
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
        c = DLRContent('DELIVRD', msgid, dlr_url, dlr_level, id_smsc = 'abc', sub = '3', 
                 dlvrd = '3', subdate = 'anydate', donedate = 'anydate', err = '', text = 'Any text')
        
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
                                  'UNDELIV', 'ACCEPTED', 'UNKNOWN', 'REJECTD', 'ESME_ANYTHING']
        
        for status in validStatuses:
            c = DLRContent(status, msgid, dlr_url, dlr_level)
            
            self.assertEquals(c['headers']['message_status'], status)
        
        self.assertRaises(InvalidParameterError, DLRContent, 'anystatus', msgid, dlr_url, dlr_level)
        
    def test_level(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        
        c = DLRContent('DELIVRD', msgid, dlr_url, 1)
        self.assertEquals(c['headers']['level'], 1)
        self.assertRaises(InvalidParameterError, DLRContent, 'DELIVRD', msgid, dlr_url, 45)
        
    def test_method(self):
        msgid = 'msgid'
        dlr_url = 'http://dlr_url'
        
        c = DLRContent('DELIVRD', msgid, dlr_url, 1)
        self.assertEquals(c['headers']['method'], 'POST')

        c = DLRContent('DELIVRD', msgid, dlr_url, 1, method = 'GET')
        self.assertEquals(c['headers']['method'], 'GET')
        
        c = DLRContent('DELIVRD', msgid, dlr_url, 1, method = 'POST')
        self.assertEquals(c['headers']['method'], 'POST')

        self.assertRaises(InvalidParameterError, DLRContent, 'DELIVRD', msgid, dlr_url, 1, method = 'ANY METHOD')