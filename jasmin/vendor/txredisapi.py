# coding: utf-8
# Copyright 2009 Alexandre Fiori
# https://github.com/fiorix/txredisapi
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
#
# Credits:
#   The Protocol class is an improvement of txRedis' protocol,
#   by Dorian Raymer and Ludovico Magnocavallo.
#
#   Sharding and Consistent Hashing implementation by Gleicon Moraes.
#

import bisect
import collections
import functools
import operator
import re
import types
import warnings
import zlib
import string
import hashlib

from twisted.internet import defer
from twisted.internet import protocol
from twisted.internet import reactor
from twisted.internet import task
from twisted.protocols import basic
from twisted.protocols import policies
from twisted.python import log
from twisted.python.failure import Failure


class RedisError(Exception):
    pass


class ConnectionError(RedisError):
    pass


class ResponseError(RedisError):
    pass


class ScriptDoesNotExist(ResponseError):
    pass


class NoScriptRunning(ResponseError):
    pass


class InvalidResponse(RedisError):
    pass


class InvalidData(RedisError):
    pass


class WatchError(RedisError):
    pass


def list_or_args(command, keys, args):
    oldapi = bool(args)
    try:
        iter(keys)
        if isinstance(keys, (str, unicode)):
            raise TypeError
    except TypeError:
        oldapi = True
        keys = [keys]

    if oldapi:
        warnings.warn(DeprecationWarning(
            "Passing *args to redis.%s is deprecated. "
            "Pass an iterable to ``keys`` instead" % command))
        keys.extend(args)
    return keys

# Possible first characters in a string containing an integer or a float.
_NUM_FIRST_CHARS = frozenset(string.digits + "+-.")


class MultiBulkStorage(object):
    def __init__(self, parent=None):
        self.items = None
        self.pending = None
        self.parent = parent

    def set_pending(self, pending):
        if self.pending is None:
            if pending < 0:
                self.items = None
                self.pending = 0
            else:
                self.items = []
                self.pending = pending
            return self
        else:
            m = MultiBulkStorage(self)
            m.set_pending(pending)
            return m

    def append(self, item):
        self.pending -= 1
        self.items.append(item)


class LineReceiver(protocol.Protocol, basic._PauseableMixin):
    line_mode = 1
    __buffer = ''
    delimiter = '\r\n'
    MAX_LENGTH = 16384

    def clearLineBuffer(self):
        b = self.__buffer
        self.__buffer = ""
        return b

    def dataReceived(self, data, unpause=False):
        if unpause is True:
            if self.__buffer:
                self.__buffer = data + self.__buffer
            else:
                self.__buffer += data

            self.resumeProducing()
        else:
            self.__buffer = self.__buffer + data

        while self.line_mode and not self.paused:
            try:
                line, self.__buffer = self.__buffer.split(self.delimiter, 1)
            except ValueError:
                if len(self.__buffer) > self.MAX_LENGTH:
                    line, self.__buffer = self.__buffer, ''
                    return self.lineLengthExceeded(line)
                break
            else:
                linelength = len(line)
                if linelength > self.MAX_LENGTH:
                    exceeded = line + self.__buffer
                    self.__buffer = ''
                    return self.lineLengthExceeded(exceeded)
                why = self.lineReceived(line)
                if why or self.transport and self.transport.disconnecting:
                    return why
        else:
            if not self.paused:
                data = self.__buffer
                self.__buffer = ''
                if data:
                    return self.rawDataReceived(data)

    def setLineMode(self, extra=''):
        self.line_mode = 1
        if extra:
            self.pauseProducing()
            reactor.callLater(0, self.dataReceived, extra, True)

    def setRawMode(self):
        self.line_mode = 0

    def rawDataReceived(self, data):
        raise NotImplementedError

    def lineReceived(self, line):
        raise NotImplementedError

    def sendLine(self, line):
        return self.transport.write(line + self.delimiter)

    def lineLengthExceeded(self, line):
        return self.transport.loseConnection()


class RedisProtocol(LineReceiver, policies.TimeoutMixin):
    """
    Redis client protocol.
    """

    def __init__(self, charset="utf-8", errors="strict"):
        self.charset = charset
        self.errors = errors

        self.bulk_length = 0
        self.bulk_buffer = []

        self.post_proc = []
        self.multi_bulk = MultiBulkStorage()

        self.replyQueue = defer.DeferredQueue()

        self.transactions = 0
        self.inTransaction = False
        self.unwatch_cc = lambda: ()

        self.script_hashes = set()

    @defer.inlineCallbacks
    def connectionMade(self):
        if self.factory.dbid is not None:
            try:
                response = yield self.select(self.factory.dbid)
                if isinstance(response, ResponseError):
                    raise response
            except Exception, e:
                self.factory.continueTrying = False
                self.transport.loseConnection()

                msg = "Redis error: could not set dbid=%s: %s" % \
                      (self.factory.dbid, str(e))
                self.factory.connectionError(msg)
                if self.factory.isLazy:
                    log.msg(msg)
                defer.returnValue(None)

        self.connected = 1
        self.factory.addConnection(self)

    def connectionLost(self, why):
        self.connected = 0
        self.script_hashes.clear()
        self.factory.delConnection(self)
        LineReceiver.connectionLost(self, why)
        while self.replyQueue.waiting:
            self.replyReceived(ConnectionError("Lost connection"))

    def lineReceived(self, line):
        """
        Reply types:
          "-" error message
          "+" single line status reply
          ":" integer number (protocol level only?)
          "$" bulk data
          "*" multi-bulk data
        """
        if line:
            self.resetTimeout()
            token, data = line[0], line[1:]
        else:
            return

        if token == "$":  # bulk data
            try:
                self.bulk_length = long(data)
            except ValueError:
                self.replyReceived(InvalidResponse("Cannot convert data "
                                                   "'%s' to integer" % data))
            else:
                if self.bulk_length == -1:
                    self.bulk_length = 0
                    self.bulkDataReceived(None)
                else:
                    self.bulk_length += 2  # 2 == \r\n
                    self.setRawMode()

        elif token == "*":  # multi-bulk data
            try:
                n = long(data)
            except (TypeError, ValueError):
                self.multi_bulk = MultiBulkStorage()
                self.replyReceived(InvalidResponse("Cannot convert "
                                                   "multi-response header "
                                                   "'%s' to integer" % data))
            else:
                self.multi_bulk = self.multi_bulk.set_pending(n)
                if n in (0, -1):
                    self.multiBulkDataReceived()

        elif token == "+":  # single line status
            if data == "QUEUED":
                self.transactions += 1
                self.replyReceived(data)
            else:
                if self.multi_bulk.pending:
                    self.handleMultiBulkElement(data)
                else:
                    self.replyReceived(data)

        elif token == "-":  # error
            reply = ResponseError(data[4:] if data[:4] == "ERR" else data)
            if self.multi_bulk.pending:
                self.handleMultiBulkElement(reply)
            else:
                self.replyReceived(reply)

        elif token == ":":  # integer
            try:
                reply = int(data)
            except ValueError:
                reply = InvalidResponse(
                    "Cannot convert data '%s' to integer" % data)

            if self.multi_bulk.pending:
                self.handleMultiBulkElement(reply)
            else:
                self.replyReceived(reply)

    def rawDataReceived(self, data):
        """
        Process and dispatch to bulkDataReceived.
        """
        if self.bulk_length:
            data, rest = data[:self.bulk_length], data[self.bulk_length:]
            self.bulk_length -= len(data)
        else:
            rest = ""

        self.bulk_buffer.append(data)
        if self.bulk_length == 0:
            bulk_buffer = "".join(self.bulk_buffer)[:-2]
            self.bulk_buffer = []
            self.bulkDataReceived(bulk_buffer)
            self.setLineMode(extra=rest)

    def bulkDataReceived(self, data):
        """
        Receipt of a bulk data element.
        """
        el = None
        if data is not None:
            if data and data[0] in _NUM_FIRST_CHARS:  # Most likely a number
                try:
                    el = int(data) if data.find('.') == -1 else float(data)
                except ValueError:
                    pass

            if el is None:
                try:
                    el = data.decode(self.charset)
                except UnicodeDecodeError:
                    el = data

        if self.multi_bulk.pending or self.multi_bulk.items:
            self.handleMultiBulkElement(el)
        else:
            self.replyReceived(el)

    def handleMultiBulkElement(self, element):
        self.multi_bulk.append(element)

        if not self.multi_bulk.pending:
            self.multiBulkDataReceived()

    def multiBulkDataReceived(self):
        """
        Receipt of list or set of bulk data elements.
        """
        while self.multi_bulk.parent and not self.multi_bulk.pending:
            p = self.multi_bulk.parent
            p.append(self.multi_bulk.items)
            self.multi_bulk = p

        if not self.multi_bulk.pending:
            reply = self.multi_bulk.items
            self.multi_bulk = MultiBulkStorage()

            if self.inTransaction and reply is not None:
                self.transactions -= len(reply)
                if not self.transactions:
                    self.inTransaction = False

                    tmp = []
                    for f, v in zip(self.post_proc[1:], reply):
                        if callable(f):
                            tmp.append(f(v))
                        else:
                            tmp.append(v)
                        reply = tmp
                self.post_proc = []

            self.replyReceived(reply)

    def replyReceived(self, reply):
        """
        Complete reply received and ready to be pushed to the requesting
        function.
        """
        self.replyQueue.put(reply)

    @staticmethod
    def handle_reply(r):
        if isinstance(r, Exception):
            raise r
        return r

    def execute_command(self, *args, **kwargs):
        if self.connected == 0:
            raise ConnectionError("Not connected")
        else:
            cmds = []
            cmd_template = "$%s\r\n%s\r\n"
            for s in args:
                if isinstance(s, str):
                    cmd = s
                elif isinstance(s, unicode):
                    try:
                        cmd = s.encode(self.charset, self.errors)
                    except UnicodeEncodeError, e:
                        raise InvalidData(
                            "Error encoding unicode value '%s': %s" %
                            (repr(s), e))
                elif isinstance(s, float):
                    try:
                        cmd = format(s, "f")
                    except NameError:
                        cmd = "%0.6f" % s
                else:
                    cmd = str(s)
                cmds.append(cmd_template % (len(cmd), cmd))
            self.transport.write("*%s\r\n%s" % (len(cmds), "".join(cmds)))
            r = self.replyQueue.get().addCallback(self.handle_reply)

            if self.inTransaction:
                self.post_proc.append(kwargs.get("post_proc"))
            else:
                if "post_proc" in kwargs:
                    f = kwargs["post_proc"]
                    if callable(f):
                        r.addCallback(f)

            return r

    ##
    # REDIS COMMANDS
    ##

    # Connection handling
    def quit(self):
        """
        Close the connection
        """
        self.factory.continueTrying = False
        return self.execute_command("QUIT")

    def auth(self, password):
        """
        Simple password authentication if enabled
        """
        return self.execute_command("AUTH", password)

    def ping(self):
        """
        Ping the server
        """
        return self.execute_command("PING")

    # Commands operating on all value types
    def exists(self, key):
        """
        Test if a key exists
        """
        return self.execute_command("EXISTS", key)

    def delete(self, keys, *args):
        """
        Delete one or more keys
        """
        keys = list_or_args("delete", keys, args)
        return self.execute_command("DEL", *keys)

    def type(self, key):
        """
        Return the type of the value stored at key
        """
        return self.execute_command("TYPE", key)

    def keys(self, pattern="*"):
        """
        Return all the keys matching a given pattern
        """
        return self.execute_command("KEYS", pattern)

    def randomkey(self):
        """
        Return a random key from the key space
        """
        return self.execute_command("RANDOMKEY")

    def rename(self, oldkey, newkey):
        """
        Rename the old key in the new one,
        destroying the newname key if it already exists
        """
        return self.execute_command("RENAME", oldkey, newkey)

    def renamenx(self, oldkey, newkey):
        """
        Rename the oldname key to newname,
        if the newname key does not already exist
        """
        return self.execute_command("RENAMENX", oldkey, newkey)

    def dbsize(self):
        """
        Return the number of keys in the current db
        """
        return self.execute_command("DBSIZE")

    def expire(self, key, time):
        """
        Set a time to live in seconds on a key
        """
        return self.execute_command("EXPIRE", key, time)

    def persist(self, key):
        """
        Remove the expire from a key
        """
        return self.execute_command("PERSIST", key)

    def ttl(self, key):
        """
        Get the time to live in seconds of a key
        """
        return self.execute_command("TTL", key)

    def select(self, index):
        """
        Select the DB with the specified index
        """
        return self.execute_command("SELECT", index)

    def move(self, key, dbindex):
        """
        Move the key from the currently selected DB to the dbindex DB
        """
        return self.execute_command("MOVE", key, dbindex)

    def flush(self, all_dbs=False):
        warnings.warn(DeprecationWarning(
            "redis.flush() has been deprecated, "
            "use redis.flush() or redis.flushall() instead"))
        return all_dbs and self.flushall() or self.flushdb()

    def flushdb(self):
        """
        Remove all the keys from the currently selected DB
        """
        return self.execute_command("FLUSHDB")

    def flushall(self):
        """
        Remove all the keys from all the databases
        """
        return self.execute_command("FLUSHALL")

    # Commands operating on string values
    def set(self, key, value, preserve=False, getset=False):
        """
        Set a key to a string value
        """
        if preserve:
            warnings.warn(DeprecationWarning(
                "preserve option to 'set' is deprecated, "
                "use redis.setnx() instead"))
            return self.setnx(key, value)

        if getset:
            warnings.warn(DeprecationWarning(
                "getset option to 'set' is deprecated, "
                "use redis.getset() instead"))
            return self.getset(key, value)

        return self.execute_command("SET", key, value)

    def get(self, key):
        """
        Return the string value of the key
        """
        return self.execute_command("GET", key)

    def getset(self, key, value):
        """
        Set a key to a string returning the old value of the key
        """
        return self.execute_command("GETSET", key, value)

    def mget(self, keys, *args):
        """
        Multi-get, return the strings values of the keys
        """
        keys = list_or_args("mget", keys, args)
        return self.execute_command("MGET", *keys)

    def setnx(self, key, value):
        """
        Set a key to a string value if the key does not exist
        """
        return self.execute_command("SETNX", key, value)

    def setex(self, key, time, value):
        """
        Set+Expire combo command
        """
        return self.execute_command("SETEX", key, time, value)

    def mset(self, mapping):
        """
        Set the respective fields to the respective values.
        HMSET replaces old values with new values.
        """
        items = []
        for pair in mapping.iteritems():
            items.extend(pair)
        return self.execute_command("MSET", *items)

    def msetnx(self, mapping):
        """
        Set multiple keys to multiple values in a single atomic
        operation if none of the keys already exist
        """
        items = []
        for pair in mapping.iteritems():
            items.extend(pair)
        return self.execute_command("MSETNX", *items)

    def incr(self, key, amount=1):
        """
        Increment the integer value of key
        """
        return self.execute_command("INCRBY", key, amount)

    def incrby(self, key, amount):
        """
        Increment the integer value of key by integer
        """
        return self.incr(key, amount)

    def decr(self, key, amount=1):
        """
        Decrement the integer value of key
        """
        return self.execute_command("DECRBY", key, amount)

    def decrby(self, key, amount):
        """
        Decrement the integer value of key by integer
        """
        return self.decr(key, amount)

    def append(self, key, value):
        """
        Append the specified string to the string stored at key
        """
        return self.execute_command("APPEND", key, value)

    def substr(self, key, start, end=-1):
        """
        Return a substring of a larger string
        """
        return self.execute_command("SUBSTR", key, start, end)

    # Commands operating on lists
    def push(self, key, value, tail=False):
        warnings.warn(DeprecationWarning(
            "redis.push() has been deprecated, "
            "use redis.lpush() or redis.rpush() instead"))

        return tail and self.rpush(key, value) or self.lpush(key, value)

    def rpush(self, key, value):
        """
        Append an element to the tail of the List value at key
        """
        return self.execute_command("RPUSH", key, value)

    def lpush(self, key, value):
        """
        Append an element to the head of the List value at key
        """
        return self.execute_command("LPUSH", key, value)

    def llen(self, key):
        """
        Return the length of the List value at key
        """
        return self.execute_command("LLEN", key)

    def lrange(self, key, start, end):
        """
        Return a range of elements from the List at key
        """
        return self.execute_command("LRANGE", key, start, end)

    def ltrim(self, key, start, end):
        """
        Trim the list at key to the specified range of elements
        """
        return self.execute_command("LTRIM", key, start, end)

    def lindex(self, key, index):
        """
        Return the element at index position from the List at key
        """
        return self.execute_command("LINDEX", key, index)

    def lset(self, key, index, value):
        """
        Set a new value as the element at index position of the List at key
        """
        return self.execute_command("LSET", key, index, value)

    def lrem(self, key, count, value):
        """
        Remove the first-N, last-N, or all the elements matching value
        from the List at key
        """
        return self.execute_command("LREM", key, count, value)

    def pop(self, key, tail=False):
        warnings.warn(DeprecationWarning(
            "redis.pop() has been deprecated, "
            "user redis.lpop() or redis.rpop() instead"))

        return tail and self.rpop(key) or self.lpop(key)

    def lpop(self, key):
        """
        Return and remove (atomically) the first element of the List at key
        """
        return self.execute_command("LPOP", key)

    def rpop(self, key):
        """
        Return and remove (atomically) the last element of the List at key
        """
        return self.execute_command("RPOP", key)

    def blpop(self, keys, timeout=0):
        """
        Blocking LPOP
        """
        if isinstance(keys, (str, unicode)):
            keys = [keys]
        else:
            keys = list(keys)

        keys.append(timeout)
        return self.execute_command("BLPOP", *keys)

    def brpop(self, keys, timeout=0):
        """
        Blocking RPOP
        """
        if isinstance(keys, (str, unicode)):
            keys = [keys]
        else:
            keys = list(keys)

        keys.append(timeout)
        return self.execute_command("BRPOP", *keys)

    def brpoplpush(self, source, destination):
        """
        Pop a value from a list, push it to another list and return
        it; or block until one is available.
        """
        return self.execute_command("BRPOPLPUSH", source, destination)

    def rpoplpush(self, srckey, dstkey):
        """
        Return and remove (atomically) the last element of the source
        List  stored at srckey and push the same element to the
        destination List stored at dstkey
        """
        return self.execute_command("RPOPLPUSH", srckey, dstkey)

    def _make_set(self, result):
        if isinstance(result, list):
            return set(result)
        return result

    # Commands operating on sets
    def sadd(self, key, members, *args):
        """
        Add the specified member to the Set value at key
        """
        members = list_or_args("sadd", members, args)
        return self.execute_command("SADD", key, *members)

    def srem(self, key, members, *args):
        """
        Remove the specified member from the Set value at key
        """
        members = list_or_args("srem", members, args)
        return self.execute_command("SREM", key, *members)

    def spop(self, key):
        """
        Remove and return (pop) a random element from the Set value at key
        """
        return self.execute_command("SPOP", key)

    def smove(self, srckey, dstkey, member):
        """
        Move the specified member from one Set to another atomically
        """
        return self.execute_command(
            "SMOVE", srckey, dstkey, member).addCallback(bool)

    def scard(self, key):
        """
        Return the number of elements (the cardinality) of the Set at key
        """
        return self.execute_command("SCARD", key)

    def sismember(self, key, value):
        """
        Test if the specified value is a member of the Set at key
        """
        return self.execute_command("SISMEMBER", key, value).addCallback(bool)

    def sinter(self, keys, *args):
        """
        Return the intersection between the Sets stored at key1, ..., keyN
        """
        keys = list_or_args("sinter", keys, args)
        return self.execute_command("SINTER", *keys).addCallback(
            self._make_set)

    def sinterstore(self, dstkey, keys, *args):
        """
        Compute the intersection between the Sets stored
        at key1, key2, ..., keyN, and store the resulting Set at dstkey
        """
        keys = list_or_args("sinterstore", keys, args)
        return self.execute_command("SINTERSTORE", dstkey, *keys)

    def sunion(self, keys, *args):
        """
        Return the union between the Sets stored at key1, key2, ..., keyN
        """
        keys = list_or_args("sunion", keys, args)
        return self.execute_command("SUNION", *keys).addCallback(
            self._make_set)

    def sunionstore(self, dstkey, keys, *args):
        """
        Compute the union between the Sets stored
        at key1, key2, ..., keyN, and store the resulting Set at dstkey
        """
        keys = list_or_args("sunionstore", keys, args)
        return self.execute_command("SUNIONSTORE", dstkey, *keys)

    def sdiff(self, keys, *args):
        """
        Return the difference between the Set stored at key1 and
        all the Sets key2, ..., keyN
        """
        keys = list_or_args("sdiff", keys, args)
        return self.execute_command("SDIFF", *keys).addCallback(
            self._make_set)

    def sdiffstore(self, dstkey, keys, *args):
        """
        Compute the difference between the Set key1 and all the
        Sets key2, ..., keyN, and store the resulting Set at dstkey
        """
        keys = list_or_args("sdiffstore", keys, args)
        return self.execute_command("SDIFFSTORE", dstkey, *keys)

    def smembers(self, key):
        """
        Return all the members of the Set value at key
        """
        return self.execute_command("SMEMBERS", key).addCallback(
            self._make_set)

    def srandmember(self, key):
        """
        Return a random member of the Set value at key
        """
        return self.execute_command("SRANDMEMBER", key)

    # Commands operating on sorted zsets (sorted sets)
    def zadd(self, key, score, member, *args):
        """
        Add the specified member to the Sorted Set value at key
        or update the score if it already exist
        """
        if args:
            # Args should be pairs (have even number of elements)
            if len(args) % 2:
                return defer.fail(InvalidData(
                    "Invalid number of arguments to ZADD"))
            else:
                l = [score, member]
                l.extend(args)
                args = l
        else:
            args = [score, member]
        return self.execute_command("ZADD", key, *args)

    def zrem(self, key, *args):
        """
        Remove the specified member from the Sorted Set value at key
        """
        return self.execute_command("ZREM", key, *args)

    def zincr(self, key, member):
        return self.zincrby(key, 1, member)

    def zdecr(self, key, member):
        return self.zincrby(key, -1, member)

    def zincrby(self, key, increment, member):
        """
        If the member already exists increment its score by increment,
        otherwise add the member setting increment as score
        """
        return self.execute_command("ZINCRBY", key, increment, member)

    def zrank(self, key, member):
        """
        Return the rank (or index) or member in the sorted set at key,
        with scores being ordered from low to high
        """
        return self.execute_command("ZRANK", key, member)

    def zrevrank(self, key, member):
        """
        Return the rank (or index) or member in the sorted set at key,
        with scores being ordered from high to low
        """
        return self.execute_command("ZREVRANK", key, member)

    def _handle_withscores(self, r):
        if isinstance(r, list):
            # Return a list tuples of form (value, score)
            return zip(r[::2], r[1::2])
        return r

    def _zrange(self, key, start, end, withscores, reverse):
        if reverse:
            cmd = "ZREVRANGE"
        else:
            cmd = "ZRANGE"
        if withscores:
            pieces = (cmd, key, start, end, "WITHSCORES")
        else:
            pieces = (cmd, key, start, end)
        r = self.execute_command(*pieces)
        if withscores:
            r.addCallback(self._handle_withscores)
        return r

    def zrange(self, key, start=0, end=-1, withscores=False):
        """
        Return a range of elements from the sorted set at key
        """
        return self._zrange(key, start, end, withscores, False)

    def zrevrange(self, key, start=0, end=-1, withscores=False):
        """
        Return a range of elements from the sorted set at key,
        exactly like ZRANGE, but the sorted set is ordered in
        traversed in reverse order, from the greatest to the smallest score
        """
        return self._zrange(key, start, end, withscores, True)

    def _zrangebyscore(self, key, min, max, withscores, offset, count, rev):
        if rev:
            cmd = "ZREVRANGEBYSCORE"
        else:
            cmd = "ZRANGEBYSCORE"
        if (offset is None) != (count is None):  # XNOR
            return defer.fail(InvalidData(
                "Invalid count and offset arguments to %s" % cmd))
        if withscores:
            pieces = [cmd, key, min, max, "WITHSCORES"]
        else:
            pieces = [cmd, key, min, max]
        if offset is not None and count is not None:
            pieces.extend(("LIMIT", offset, count))
        r = self.execute_command(*pieces)
        if withscores:
            r.addCallback(self._handle_withscores)
        return r

    def zrangebyscore(self, key, min='-inf', max='+inf', withscores=False,
                      offset=None, count=None):
        """
        Return all the elements with score >= min and score <= max
        (a range query) from the sorted set
        """
        return self._zrangebyscore(key, min, max, withscores, offset,
                                   count, False)

    def zrevrangebyscore(self, key, max='+inf', min='-inf', withscores=False,
                         offset=None, count=None):
        """
        ZRANGEBYSCORE in reverse order
        """
        # ZREVRANGEBYSCORE takes max before min
        return self._zrangebyscore(key, max, min, withscores, offset,
                                   count, True)

    def zcount(self, key, min='-inf', max='+inf'):
        """
        Return the number of elements with score >= min and score <= max
        in the sorted set
        """
        if min == '-inf' and max == '+inf':
            return self.zcard(key)
        return self.execute_command("ZCOUNT", key, min, max)

    def zcard(self, key):
        """
        Return the cardinality (number of elements) of the sorted set at key
        """
        return self.execute_command("ZCARD", key)

    def zscore(self, key, element):
        """
        Return the score associated with the specified element of the sorted
        set at key
        """
        return self.execute_command("ZSCORE", key, element)

    def zremrangebyrank(self, key, min=0, max=-1):
        """
        Remove all the elements with rank >= min and rank <= max from
        the sorted set
        """
        return self.execute_command("ZREMRANGEBYRANK", key, min, max)

    def zremrangebyscore(self, key, min='-inf', max='+inf'):
        """
        Remove all the elements with score >= min and score <= max from
        the sorted set
        """
        return self.execute_command("ZREMRANGEBYSCORE", key, min, max)

    def zunionstore(self, dstkey, keys, aggregate=None):
        """
        Perform a union over a number of sorted sets with optional
        weight and aggregate
        """
        return self._zaggregate("ZUNIONSTORE", dstkey, keys, aggregate)

    def zinterstore(self, dstkey, keys, aggregate=None):
        """
        Perform an intersection over a number of sorted sets with optional
        weight and aggregate
        """
        return self._zaggregate("ZINTERSTORE", dstkey, keys, aggregate)

    def _zaggregate(self, command, dstkey, keys, aggregate):
        pieces = [command, dstkey, len(keys)]
        if isinstance(keys, dict):
            keys, weights = zip(*keys.items())
        else:
            weights = None

        pieces.extend(keys)
        if weights:
            pieces.append("WEIGHTS")
            pieces.extend(weights)

        if aggregate:
            if aggregate is min:
                aggregate = 'MIN'
            elif aggregate is max:
                aggregate = 'MAX'
            elif aggregate is sum:
                aggregate = 'SUM'
            else:
                err_flag = True
                if isinstance(aggregate, (str, unicode)):
                    aggregate_u = aggregate.upper()
                    if aggregate_u in ('MIN', 'MAX', 'SUM'):
                        aggregate = aggregate_u
                        err_flag = False
                if err_flag:
                    return defer.fail(InvalidData(
                        "Invalid aggregate function: %s" % aggregate))
            pieces.extend(("AGGREGATE", aggregate))
        return self.execute_command(*pieces)

    # Commands operating on hashes
    def hset(self, key, field, value):
        """
        Set the hash field to the specified value. Creates the hash if needed
        """
        return self.execute_command("HSET", key, field, value)

    def hsetnx(self, key, field, value):
        """
        Set the hash field to the specified value if the field does not exist.
        Creates the hash if needed
        """
        return self.execute_command("HSETNX", key, field, value)

    def hget(self, key, field):
        """
        Retrieve the value of the specified hash field.
        """
        return self.execute_command("HGET", key, field)

    def hmget(self, key, fields):
        """
        Get the hash values associated to the specified fields.
        """
        return self.execute_command("HMGET", key, *fields)

    def hmset(self, key, mapping):
        """
        Set the hash fields to their respective values.
        """
        items = []
        for pair in mapping.iteritems():
            items.extend(pair)
        return self.execute_command("HMSET", key, *items)

    def hincr(self, key, field):
        return self.hincrby(key, field, 1)

    def hdecr(self, key, field):
        return self.hincrby(key, field, -1)

    def hincrby(self, key, field, integer):
        """
        Increment the integer value of the hash at key on field with integer.
        """
        return self.execute_command("HINCRBY", key, field, integer)

    def hexists(self, key, field):
        """
        Test for existence of a specified field in a hash
        """
        return self.execute_command("HEXISTS", key, field)

    def hdel(self, key, fields):
        """
        Remove the specified field or fields from a hash
        """
        if isinstance(fields, (str, unicode)):
            fields = [fields]
        else:
            fields = list(fields)
        return self.execute_command("HDEL", key, *fields)

    def hlen(self, key):
        """
        Return the number of items in a hash.
        """
        return self.execute_command("HLEN", key)

    def hkeys(self, key):
        """
        Return all the fields in a hash.
        """
        return self.execute_command("HKEYS", key)

    def hvals(self, key):
        """
        Return all the values in a hash.
        """
        return self.execute_command("HVALS", key)

    def hgetall(self, key):
        """
        Return all the fields and associated values in a hash.
        """
        f = lambda d: dict(zip(d[::2], d[1::2]))
        return self.execute_command("HGETALL", key, post_proc=f)

    # Sorting
    def sort(self, key, start=None, end=None, by=None, get=None,
             desc=None, alpha=False, store=None):
        if (start is not None and end is not None) or \
           (end is not None and start is None):
            raise RedisError("``start`` and ``end`` must both be specified")

        pieces = [key]
        if by is not None:
            pieces.append("BY")
            pieces.append(by)
        if start is not None and end is not None:
            pieces.append("LIMIT")
            pieces.append(start)
            pieces.append(end)
        if get is not None:
            pieces.append("GET")
            pieces.append(get)
        if desc:
            pieces.append("DESC")
        if alpha:
            pieces.append("ALPHA")
        if store is not None:
            pieces.append("STORE")
            pieces.append(store)

        return self.execute_command("SORT", *pieces)

    def _clear_txstate(self):
        self.inTransaction = False

    def watch(self, keys):
        if not self.inTransaction:
            self.inTransaction = True
            self.unwatch_cc = self._clear_txstate
        if isinstance(keys, (str, unicode)):
            keys = [keys]
        d = self.execute_command("WATCH", *keys).addCallback(self._tx_started)
        return d

    def unwatch(self):
        self.unwatch_cc()
        return self.execute_command("UNWATCH")

    # Transactions
    # multi() will return a deferred with a "connection" object
    # That object must be used for further interactions within
    # the transaction. At the end, either exec() or discard()
    # must be executed.
    def multi(self, keys=None):
        self.inTransaction = True
        self.unwatch_cc = lambda: ()
        if keys is not None:
            d = self.watch(keys)
            d.addCallback(lambda _: self.execute_command("MULTI"))
        else:
            d = self.execute_command("MULTI")
        d.addCallback(self._tx_started)
        return d

    def _tx_started(self, response):
        if response != 'OK':
            raise RedisError('Invalid response: %s' % response)
        return self

    def _commit_check(self, response):
        if response is None:
            self.transactions = 0
            self.inTransaction = False
            raise WatchError("Transaction failed")
        else:
            return response

    def commit(self):
        if self.inTransaction is False:
            raise RedisError("Not in transaction")
        return self.execute_command("EXEC").addCallback(self._commit_check)

    def discard(self):
        if self.inTransaction is False:
            raise RedisError("Not in transaction")
        self.post_proc = []
        self.transactions = 0
        self.inTransaction = False
        return self.execute_command("DISCARD")

    # Publish/Subscribe
    # see the SubscriberProtocol for subscribing to channels
    def publish(self, channel, message):
        """
        Publish message to a channel
        """
        return self.execute_command("PUBLISH", channel, message)

    # Persistence control commands
    def save(self):
        """
        Synchronously save the DB on disk
        """
        return self.execute_command("SAVE")

    def bgsave(self):
        """
        Asynchronously save the DB on disk
        """
        return self.execute_command("BGSAVE")

    def lastsave(self):
        """
        Return the UNIX time stamp of the last successfully saving of the
        dataset on disk
        """
        return self.execute_command("LASTSAVE")

    def shutdown(self):
        """
        Synchronously save the DB on disk, then shutdown the server
        """
        self.factory.continueTrying = False
        return self.execute_command("SHUTDOWN")

    def bgrewriteaof(self):
        """
        Rewrite the append only file in background when it gets too big
        """
        return self.execute_command("BGREWRITEAOF")

    def _process_info(self, r):
        keypairs = [x for x in r.split('\r\n') if
                    u':' in x and not x.startswith(u'#')]
        d = {}
        for kv in keypairs:
            k, v = kv.split(u':')
            d[k] = v
        return d

    # Remote server control commands
    def info(self, type=None):
        """
        Provide information and statistics about the server
        """
        if type is None:
            return self.execute_command("INFO")
        else:
            r = self.execute_command("INFO", type)
            return r.addCallback(self._process_info)

    # slaveof is missing

    # Redis 2.6 scripting commands
    def _eval(self, script, script_hash, **kwargs):
        n = len(kwargs)
        if n == 0:
            args = ()
        else:
            keys, values = zip(*kwargs.items())
            args = keys + values
        r = self.execute_command("EVAL", script, n, *args)
        if script_hash in self.script_hashes:
            return r
        return r.addCallback(self._eval_success, script_hash)

    def _eval_success(self, r, script_hash):
        self.script_hashes.add(script_hash)
        return r

    def _evalsha_failed(self, err, script, script_hash, **kwargs):
        if err.check(ScriptDoesNotExist):
            return self._eval(script, script_hash, **kwargs)
        return err

    def eval(self, script, **kwargs):
        h = hashlib.sha1(script).hexdigest()
        if h in self.script_hashes:
            return self.evalsha(h, **kwargs).addErrback(
                self._evalsha_failed, script, h, **kwargs)
        return self._eval(script, h, **kwargs)

    def _evalsha_errback(self, err, script_hash):
        if err.check(ResponseError):
            if err.value.args[0].startswith(u'NOSCRIPT'):
                if script_hash in self.script_hashes:
                    self.script_hashes.remove(script_hash)
                raise ScriptDoesNotExist("No script matching hash: %s found" %
                                         script_hash)
        return err

    def evalsha(self, sha1_hash, **kwargs):
        n = len(kwargs)
        if n == 0:
            args = ()
        else:
            keys, values = zip(*kwargs.items())
            args = keys + values
        r = self.execute_command("EVALSHA",
                                 sha1_hash, n,
                                 *args).addErrback(self._evalsha_errback,
                                                   sha1_hash)
        if sha1_hash not in self.script_hashes:
            r.addCallback(self._eval_success, sha1_hash)
        return r

    def _script_exists_success(self, r):
        l = [bool(x) for x in r]
        if len(l) == 1:
            return l[0]
        else:
            return l

    def script_exists(self, *hashes):
        return self.execute_command("SCRIPT", "EXISTS",
                                    post_proc=self._script_exists_success,
                                    *hashes)

    def _script_flush_success(self, r):
        self.script_hashes.clear()
        return r

    def script_flush(self):
        return self.execute_command("SCRIPT", "FLUSH").addCallback(
            self._script_flush_success)

    def _handle_script_kill(self, r):
        if isinstance(r, Failure):
            if r.check(ResponseError):
                if r.value.args[0].startswith(u'NOTBUSY'):
                    raise NoScriptRunning("No script running")
        else:
            pass
        return r

    def script_kill(self):
        return self.execute_command("SCRIPT",
                                    "KILL").addBoth(self._handle_script_kill)

    def script_load(self, script):
        return self.execute_command("SCRIPT",  "LOAD", script)


class MonitorProtocol(RedisProtocol):
    """
    monitor has the same behavior as subscribe: hold the connection until
    something happens.

    take care with the performance impact: http://redis.io/commands/monitor
    """

    def messageReceived(self, message):
        pass

    def replyReceived(self, reply):
        self.messageReceived(reply)

    def monitor(self):
        return self.execute_command("MONITOR")

    def stop(self):
        self.transport.loseConnection()


class SubscriberProtocol(RedisProtocol):
    def messageReceived(self, pattern, channel, message):
        pass

    def replyReceived(self, reply):
        if isinstance(reply, list):
            if reply[-3] == u"message":
                self.messageReceived(None, *reply[-2:])
            else:
                self.replyQueue.put(reply[-3:])
                self.messageReceived(*reply[-3:])
        elif isinstance(reply, Exception):
            self.replyQueue.put(reply)

    def subscribe(self, channels):
        if isinstance(channels, (str, unicode)):
            channels = [channels]
        return self.execute_command("SUBSCRIBE", *channels)

    def unsubscribe(self, channels):
        if isinstance(channels, (str, unicode)):
            channels = [channels]
        return self.execute_command("UNSUBSCRIBE", *channels)

    def psubscribe(self, patterns):
        if isinstance(patterns, (str, unicode)):
            patterns = [patterns]
        return self.execute_command("PSUBSCRIBE", *patterns)

    def punsubscribe(self, patterns):
        if isinstance(patterns, (str, unicode)):
            patterns = [patterns]
        return self.execute_command("PUNSUBSCRIBE", *patterns)


class ConnectionHandler(object):
    def __init__(self, factory):
        self._factory = factory
        self._connected = factory.deferred

    def _wait_pool_cleanup(self, deferred):
        if self._factory.size == 0:
            deferred.callback(True)

    def disconnect(self):
        self._factory.continueTrying = 0
        for conn in self._factory.pool:
            try:
                conn.transport.loseConnection()
            except:
                pass

        d = defer.Deferred()
        t = task.LoopingCall(self._wait_pool_cleanup, d)
        d.addCallback(lambda ign: t.stop())
        t.start(.5)
        return d

    def __getattr__(self, method):
        try:
            return getattr(self._factory.getConnection, method)
        except Exception, e:
            d = defer.Deferred()
            d.errback(e)
            return lambda *ign: d

    def __repr__(self):
        try:
            cli = self._factory.pool[0].transport.getPeer()
        except:
            return "<Redis Connection: Not connected>"
        else:
            return "<Redis Connection: %s:%s - %d connection(s)>" % \
                   (cli.host, cli.port, self._factory.size)


class UnixConnectionHandler(ConnectionHandler):
    def __repr__(self):
        try:
            cli = self._factory.pool[0].transport.getPeer()
        except:
            return "<Redis Connection: Not connected>"
        else:
            return "<Redis Unix Connection: %s - %d connection(s)>" % \
                   (cli.name, self._factory.size)


ShardedMethods = frozenset([
    "decr",
    "delete",
    "exists",
    "expire",
    "get",
    "get_type",
    "getset",
    "hdel",
    "hexists",
    "hget",
    "hgetall",
    "hincrby",
    "hkeys",
    "hlen",
    "hmget",
    "hmset",
    "hset",
    "hvals",
    "incr",
    "lindex",
    "llen",
    "lrange",
    "lrem",
    "lset",
    "ltrim",
    "pop",
    "publish",
    "push",
    "rename",
    "sadd",
    "set",
    "setex",
    "setnx",
    "sismember",
    "smembers",
    "srem",
    "ttl",
    "zadd",
    "zcard",
    "zcount",
    "zdecr",
    "zincr",
    "zincrby",
    "zrange",
    "zrangebyscore",
    "zrevrangebyscore",
    "zrevrank",
    "zrank",
    "zrem",
    "zremrangebyscore",
    "zremrangebyrank",
    "zrevrange",
    "zscore",
])

_findhash = re.compile(r'.+\{(.*)\}.*')


class HashRing(object):
    """Consistent hash for redis API"""
    def __init__(self, nodes=[], replicas=160):
        self.nodes = []
        self.replicas = replicas
        self.ring = {}
        self.sorted_keys = []

        for n in nodes:
            self.add_node(n)

    def add_node(self, node):
        self.nodes.append(node)
        for x in xrange(self.replicas):
            crckey = zlib.crc32("%s:%d" % (node._factory.uuid, x))
            self.ring[crckey] = node
            self.sorted_keys.append(crckey)

        self.sorted_keys.sort()

    def remove_node(self, node):
        self.nodes.remove(node)
        for x in xrange(self.replicas):
            crckey = zlib.crc32("%s:%d" % (node, x))
            self.ring.remove(crckey)
            self.sorted_keys.remove(crckey)

    def get_node(self, key):
        n, i = self.get_node_pos(key)
        return n
    #self.get_node_pos(key)[0]

    def get_node_pos(self, key):
        if len(self.ring) == 0:
            return [None, None]
        crc = zlib.crc32(key)
        idx = bisect.bisect(self.sorted_keys, crc)
        # prevents out of range index
        idx = min(idx, (self.replicas * len(self.nodes)) - 1)
        return [self.ring[self.sorted_keys[idx]], idx]

    def iter_nodes(self, key):
        if len(self.ring) == 0:
            yield None, None
        node, pos = self.get_node_pos(key)
        for k in self.sorted_keys[pos:]:
            yield k, self.ring[k]

    def __call__(self, key):
        return self.get_node(key)


class ShardedConnectionHandler(object):
    def __init__(self, connections):
        if isinstance(connections, defer.DeferredList):
            self._ring = None
            connections.addCallback(self._makeRing)
        else:
            self._ring = HashRing(connections)

    def _makeRing(self, connections):
        connections = map(operator.itemgetter(1), connections)
        self._ring = HashRing(connections)
        return self

    @defer.inlineCallbacks
    def disconnect(self):
        if not self._ring:
            raise ConnectionError("Not connected")

        for conn in self._ring.nodes:
            yield conn.disconnect()
        defer.returnValue(True)

    def _wrap(self, method, *args, **kwargs):
        try:
            key = args[0]
            assert isinstance(key, (str, unicode))
        except:
            raise ValueError(
                "Method '%s' requires a key as the first argument" % method)

        m = _findhash.match(key)
        if m is not None and len(m.groups()) >= 1:
            node = self._ring(m.groups()[0])
        else:
            node = self._ring(key)

        return getattr(node, method)(*args, **kwargs)

    def __getattr__(self, method):
        if method in ShardedMethods:
            return functools.partial(self._wrap, method)
        else:
            raise NotImplementedError("Method '%s' cannot be sharded" % method)

    @defer.inlineCallbacks
    def mget(self, keys, *args):
        """
        high-level mget, required because of the sharding support
        """

        keys = list_or_args("mget", keys, args)
        group = collections.defaultdict(lambda: [])
        for k in keys:
            node = self._ring(k)
            group[node].append(k)

        deferreds = []
        for node, keys in group.items():
            nd = node.mget(keys)
            deferreds.append(nd)

        result = []
        response = yield defer.DeferredList(deferreds)
        for (success, values) in response:
            if success:
                result += values

        defer.returnValue(result)

    def __repr__(self):
        nodes = []
        for conn in self._ring.nodes:
            try:
                cli = conn._factory.pool[0].transport.getPeer()
            except:
                pass
            else:
                nodes.append("%s:%s/%d" %
                             (cli.host, cli.port, conn._factory.size))
        return "<Redis Sharded Connection: %s>" % ", ".join(nodes)


class ShardedUnixConnectionHandler(ShardedConnectionHandler):
    def __repr__(self):
        nodes = []
        for conn in self._ring.nodes:
            try:
                cli = conn._factory.pool[0].transport.getPeer()
            except:
                pass
            else:
                nodes.append("%s/%d" %
                             (cli.name, conn._factory.size))
        return "<Redis Sharded Connection: %s>" % ", ".join(nodes)


class RedisFactory(protocol.ReconnectingClientFactory):
    maxDelay = 10
    protocol = RedisProtocol

    def __init__(self, uuid, dbid, poolsize, isLazy=False,
                 handler=ConnectionHandler):
        if not isinstance(poolsize, int):
            raise ValueError("Redis poolsize must be an integer, not %s" %
                             repr(poolsize))

        if not isinstance(dbid, (int, types.NoneType)):
            raise ValueError("Redis dbid must be an integer, not %s" %
                             repr(dbid))

        self.uuid = uuid
        self.dbid = dbid
        self.poolsize = poolsize
        self.isLazy = isLazy

        self.idx = 0
        self.size = 0
        self.pool = []
        self.deferred = defer.Deferred()
        self.handler = handler(self)

    def addConnection(self, conn):
        self.pool.append(conn)
        self.size = len(self.pool)
        if self.deferred:
            if self.size == self.poolsize:
                self.deferred.callback(self.handler)
                self.deferred = None

    def delConnection(self, conn):
        try:
            self.pool.remove(conn)
        except Exception, e:
            log.msg("Could not remove connection from pool: %s" % str(e))

        self.size = len(self.pool)

    def connectionError(self, why):
        if self.deferred:
            self.deferred.errback(ValueError(why))
            self.deferred = None

    @property
    def getConnection(self):
        if not self.size:
            raise ConnectionError("Not connected")

        n = self.size
        while n:
            conn = self.pool[self.idx % self.size]
            self.idx += 1
            if conn.inTransaction is False:
                return conn
            n -= 1

        raise RedisError("In transaction")


class SubscriberFactory(RedisFactory):
    protocol = SubscriberProtocol

    def __init__(self, isLazy=False, handler=ConnectionHandler):
        RedisFactory.__init__(self, None, None, 1, isLazy=isLazy,
                              handler=handler)


class MonitorFactory(RedisFactory):
    protocol = MonitorProtocol

    def __init__(self, isLazy=False, handler=ConnectionHandler):
        RedisFactory.__init__(self, None, None, 1, isLazy=isLazy,
                              handler=handler)


def makeConnection(host, port, dbid, poolsize, reconnect, isLazy):
    uuid = "%s:%s" % (host, port)
    factory = RedisFactory(uuid, dbid, poolsize, isLazy, ConnectionHandler)
    factory.continueTrying = reconnect
    for x in xrange(poolsize):
        reactor.connectTCP(host, port, factory)

    if isLazy:
        return factory.handler
    else:
        return factory.deferred


def makeShardedConnection(hosts, dbid, poolsize, reconnect, isLazy):
    err = "Please use a list or tuple of host:port for sharded connections"
    if not isinstance(hosts, (list, tuple)):
        raise ValueError(err)

    connections = []
    for item in hosts:
        try:
            host, port = item.split(":")
            port = int(port)
        except:
            raise ValueError(err)

        c = makeConnection(host, port, dbid, poolsize, reconnect, isLazy)
        connections.append(c)

    if isLazy:
        return ShardedConnectionHandler(connections)
    else:
        deferred = defer.DeferredList(connections)
        ShardedConnectionHandler(deferred)
        return deferred


def Connection(host="localhost", port=6379, dbid=None, reconnect=True):
    return makeConnection(host, port, dbid, 1, reconnect, False)


def lazyConnection(host="localhost", port=6379, dbid=None, reconnect=True):
    return makeConnection(host, port, dbid, 1, reconnect, True)


def ConnectionPool(host="localhost", port=6379, dbid=None,
                   poolsize=10, reconnect=True):
    return makeConnection(host, port, dbid, poolsize, reconnect, False)


def lazyConnectionPool(host="localhost", port=6379, dbid=None,
                       poolsize=10, reconnect=True):
    return makeConnection(host, port, dbid, poolsize, reconnect, True)


def ShardedConnection(hosts, dbid=None, reconnect=True):
    return makeShardedConnection(hosts, dbid, 1, reconnect, False)


def lazyShardedConnection(hosts, dbid=None, reconnect=True):
    return makeShardedConnection(hosts, dbid, 1, reconnect, True)


def ShardedConnectionPool(hosts, dbid=None, poolsize=10, reconnect=True):
    return makeShardedConnection(hosts, dbid, poolsize, reconnect, False)


def lazyShardedConnectionPool(hosts, dbid=None, poolsize=10, reconnect=True):
    return makeShardedConnection(hosts, dbid, poolsize, reconnect, True)


def makeUnixConnection(path, dbid, poolsize, reconnect, isLazy):
    factory = RedisFactory(path, dbid, poolsize, isLazy, UnixConnectionHandler)
    factory.continueTrying = reconnect
    for x in xrange(poolsize):
        reactor.connectUNIX(path, factory)

    if isLazy:
        return factory.handler
    else:
        return factory.deferred


def makeShardedUnixConnection(paths, dbid, poolsize, reconnect, isLazy):
    err = "Please use a list or tuple of paths for sharded unix connections"
    if not isinstance(paths, (list, tuple)):
        raise ValueError(err)

    connections = []
    for path in paths:
        c = makeUnixConnection(path, dbid, poolsize, reconnect, isLazy)
        connections.append(c)

    if isLazy:
        return ShardedUnixConnectionHandler(connections)
    else:
        deferred = defer.DeferredList(connections)
        ShardedUnixConnectionHandler(deferred)
        return deferred


def UnixConnection(path="/tmp/redis.sock", dbid=None, reconnect=True):
    return makeUnixConnection(path, dbid, 1, reconnect, False)


def lazyUnixConnection(path="/tmp/redis.sock", dbid=None, reconnect=True):
    return makeUnixConnection(path, dbid, 1, reconnect, True)


def UnixConnectionPool(path="/tmp/redis.sock", dbid=None, poolsize=10,
                       reconnect=True):
    return makeUnixConnection(path, dbid, poolsize, reconnect, False)


def lazyUnixConnectionPool(path="/tmp/redis.sock", dbid=None, poolsize=10,
                           reconnect=True):
    return makeUnixConnection(path, dbid, poolsize, reconnect, True)


def ShardedUnixConnection(paths, dbid=None, reconnect=True):
    return makeShardedUnixConnection(paths, dbid, 1, reconnect, False)


def lazyShardedUnixConnection(paths, dbid=None, reconnect=True):
    return makeShardedUnixConnection(paths, dbid, 1, reconnect, True)


def ShardedUnixConnectionPool(paths, dbid=None, poolsize=10, reconnect=True):
    return makeShardedUnixConnection(paths, dbid, poolsize, reconnect, False)


def lazyShardedUnixConnectionPool(paths, dbid=None, poolsize=10,
                                  reconnect=True):
    return makeShardedUnixConnection(paths, dbid, poolsize, reconnect, True)


__all__ = [
    Connection, lazyConnection,
    ConnectionPool, lazyConnectionPool,
    ShardedConnection, lazyShardedConnection,
    ShardedConnectionPool, lazyShardedConnectionPool,
    UnixConnection, lazyUnixConnection,
    UnixConnectionPool, lazyUnixConnectionPool,
    ShardedUnixConnection, lazyShardedUnixConnection,
    ShardedUnixConnectionPool, lazyShardedUnixConnectionPool,
]

__author__ = "Alexandre Fiori"
__version__ = version = "1.0"
