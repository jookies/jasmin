import cPickle as pickle
import logging
from twisted.internet import defer, reactor
from twisted.spread.pb import RemoteReference
from twisted.cred.credentials import UsernamePassword, Anonymous
from jasmin.tools.pb import ReconnectingPBClientFactory
from twisted.spread import pb

class ConnectError(Exception):
    'Raised when PB connection can not be established'
    pass

class InvalidConnectResponseError(Exception):
    'Raised when an invalid response is received when trying to establish PB connection'
    pass

def ConnectedPB(fCallback):
    '''
    Used as a decorator to check for PB connection, it will raise an exception
    if connection is not established
    '''
    def check_cnx_and_call(self, *args, **kwargs):
        if self.isConnected is False:
            raise Exception("PB proxy is not connected !")

        return fCallback(self, *args, **kwargs)
    return check_cnx_and_call

class JasminPBProxy(object):
    '''This is a factorised PBProxy to be used by all proxies in Jasmin

    It's holding connection related methods as well as picklings
    '''

    pb = None
    isConnected = False
    pickleProtocol = 2

    @defer.inlineCallbacks
    def connect(self, host, port, username=None, password=None, retry=False):
        if retry:
            # Launch a client
            self.pbClientFactory = ReconnectingPBClientFactory()
            self.pbClientFactory.gotPerspective = self._connected
            self.pbClientFactory.disconnected = self._disconnected

            # Start login
            if username is None and password is None:
                self.pbClientFactory.startLogin(
                    Anonymous())
            else:
                self.pbClientFactory.startLogin(
                    UsernamePassword(
                        username,
                        password))

            reactor.connectTCP(host, port, self.pbClientFactory)
        else:
            # Launch a client
            self.pbClientFactory = pb.PBClientFactory()
            reactor.connectTCP(host, port, self.pbClientFactory)

            yield self.pbClientFactory.getRootObject()

            if username is None and password is None:
                yield self.pbClientFactory.login(
                    Anonymous()).addCallback(self._connected)
            else:
                yield self.pbClientFactory.login(
                    UsernamePassword(
                        username,
                        password)).addCallback(self._connected)

    def disconnect(self):
        self.isConnected = False

        # .connect has been called ?
        if hasattr(self, 'pbClientFactory'):
            return self.pbClientFactory.disconnect()

    def _disconnected(self, connector, reason):
        self.isConnected = False

    def _connected(self, perspective):
        if isinstance(perspective, RemoteReference):
            self.isConnected = True
            self.pb = perspective
        elif (isinstance(perspective, tuple) and isinstance(perspective[0], bool) and
            perspective[0] is False and isinstance(perspective[1], str)):
            raise ConnectError(perspective[1])
        else:
            raise InvalidConnectResponseError(perspective)

    def pickle(self, obj):
        return pickle.dumps(obj, self.pickleProtocol)

    def unpickle(self, obj):
        return pickle.loads(obj)
