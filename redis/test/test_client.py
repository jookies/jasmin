# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from twisted.trial.unittest import TestCase
import txredisapi as redis
from twisted.internet import defer
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.redis.client import ConnectionWithConfiguration

class AuthenticationTestCase(TestCase):
    @defer.inlineCallbacks
    def setUp(self):
        # Connect to redis server
        self.RedisForJasminConfigInstance = RedisForJasminConfig()
        self.RedisForJasminConfigInstance.password = 'guest'        
        self.rc = yield ConnectionWithConfiguration(self.RedisForJasminConfigInstance)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.rc.disconnect()
        
    @defer.inlineCallbacks
    def test_auth(self):
        try:
            # Authenticate and select db
            yield self.rc.auth(self.RedisForJasminConfigInstance.password)
            yield self.rc.select(self.RedisForJasminConfigInstance.dbid)
        except Exception, e:
            self.assertEqual(type(e), redis.ResponseError)
            self.assertEqual(str(e), 'ERR Client sent AUTH, but no password is set')
                
class RedisTestCase(TestCase):
    @defer.inlineCallbacks
    def setUp(self):

        # Connect to redis server
        RedisForJasminConfigInstance = RedisForJasminConfig()
        # No auth
        RedisForJasminConfigInstance.password = None
        
        self.rc = yield ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            self.rc.auth(RedisForJasminConfigInstance.password)
            self.rc.select(RedisForJasminConfigInstance.dbid)

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.rc.disconnect()

class DataTestCase(RedisTestCase):
    @defer.inlineCallbacks
    def test_set_get_string(self):
        yield self.rc.set('foo', 'bar')
        g = yield self.rc.get('foo')
        
        self.assertEqual(g, 'bar')
        
    @defer.inlineCallbacks
    def test_set_get_list(self):
        yield self.rc.set('foo:url', 'url of foo')
        yield self.rc.set('foo:level', 'level of foo')
        yield self.rc.set('bar:url', 'url of bar')
        yield self.rc.set('bar:level', 'level of bar')
        
        g = yield self.rc.get('foo:url')
        self.assertEqual(g, 'url of foo')
        g = yield self.rc.get('foo:level')
        self.assertEqual(g, 'level of foo')
        g = yield self.rc.get('bar:url')
        self.assertEqual(g, 'url of bar')
        g = yield self.rc.get('bar:level')
        self.assertEqual(g, 'level of bar')