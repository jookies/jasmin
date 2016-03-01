from twisted.trial.unittest import TestCase
import jasmin.vendor.txredisapi as redis
from twisted.internet import reactor, defer
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.redis.client import ConnectionWithConfiguration

@defer.inlineCallbacks
def waitFor(seconds):
    # Wait seconds
    waitDeferred = defer.Deferred()
    reactor.callLater(seconds, waitDeferred.callback, None)
    yield waitDeferred

class AuthenticationTestCase(TestCase):
    @defer.inlineCallbacks
    def setUp(self):
        # Connect to redis server
        self.RedisForJasminConfigInstance = RedisForJasminConfig()
        self.RedisForJasminConfigInstance.password = 'guest'
        self.redisClient = yield ConnectionWithConfiguration(self.RedisForJasminConfigInstance)
        yield self.redisClient._connected

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.redisClient.disconnect()

    @defer.inlineCallbacks
    def test_auth(self):
        try:
            # Authenticate and select db
            yield self.redisClient.auth(self.RedisForJasminConfigInstance.password)
            yield self.redisClient.select(self.RedisForJasminConfigInstance.dbid)
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

        self.redisClient = yield ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            yield self.redisClient.auth(RedisForJasminConfigInstance.password)
            yield self.redisClient.select(RedisForJasminConfigInstance.dbid)
        yield self.redisClient._connected

    @defer.inlineCallbacks
    def tearDown(self):
        yield self.redisClient.disconnect()

class DataTestCase(RedisTestCase):
    @defer.inlineCallbacks
    def test_set_get_string(self):
        yield self.redisClient.set('foo', 'bar')
        g = yield self.redisClient.get('foo')

        self.assertEqual(g, 'bar')

    @defer.inlineCallbacks
    def test_set_get_list(self):
        yield self.redisClient.set('foo:url', 'url of foo')
        yield self.redisClient.set('foo:level', 'level of foo')
        yield self.redisClient.set('bar:url', 'url of bar')
        yield self.redisClient.set('bar:level', 'level of bar')

        g = yield self.redisClient.get('foo:url')
        self.assertEqual(g, 'url of foo')
        g = yield self.redisClient.get('foo:level')
        self.assertEqual(g, 'level of foo')
        g = yield self.redisClient.get('bar:url')
        self.assertEqual(g, 'url of bar')
        g = yield self.redisClient.get('bar:level')
        self.assertEqual(g, 'level of bar')

    @defer.inlineCallbacks
    def test_hmset(self):
        yield self.redisClient.hmset('h_test', {'key_a': 'value_a', 'key_b': 'value_b'})

        # Get desired keys
        g = yield self.redisClient.hmget('h_test', ['key_a', 'key_b', 'anything'])
        self.assertEqual(g, [u'value_a', u'value_b', None])

        # Get all keys
        g = yield self.redisClient.hgetall('h_test')
        self.assertEqual(g, {u'key_a': u'value_a', u'key_b': u'value_b'})

        # Get incorrect redis key
        g = yield self.redisClient.hgetall('incorrect')
        self.assertEqual(g, {})

    @defer.inlineCallbacks
    def test_hmset_expiry(self):
        yield self.redisClient.hmset('h_test', {'key_a': 'value_a', 'key_b': 'value_b'})
        yield self.redisClient.expire('h_test', 5)
        #.addCallback(
        #    self.redisClient.expire, 5
        #)

        # Get desired keys
        g = yield self.redisClient.hgetall('h_test')
        self.assertEqual(g, {u'key_a': u'value_a', u'key_b': u'value_b'})

        # Wait 6 seconds
        yield waitFor(6)

        # Redis key must be expired
        g = yield self.redisClient.hgetall('h_test')
        self.assertEqual(g, {})
