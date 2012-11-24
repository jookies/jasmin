# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

"""
Test cases for amqp contents
"""

import pickle
from datetime import datetime
from twisted.trial.unittest import TestCase
from jasmin.managers.content import SubmitSmContent, SubmitSmRespContent

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
        self.assertEquals(c['expiration'], self.expiration)
        self.assertNotEquals(c['message-id'], None)
        
    def test_minimal_arguments(self):
        c = SubmitSmContent(self.body, self.replyto)
        
        self.assertEquals(c['priority'], 1)
        self.assertEquals(c['expiration'], None)
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