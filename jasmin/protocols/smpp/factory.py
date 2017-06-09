#pylint: disable=W0401,W0611,W0231
import cPickle as pickle
import logging
import re
from datetime import datetime, timedelta
from logging.handlers import TimedRotatingFileHandler

from OpenSSL import SSL
from twisted.internet import defer, reactor, ssl
from twisted.internet.protocol import ClientFactory

from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.vendor.smpp.twisted.protocol import DataHandlerResponse
from jasmin.vendor.smpp.twisted.server import SMPPBindManager as _SMPPBindManager
from jasmin.vendor.smpp.twisted.server import SMPPServerFactory as _SMPPServerFactory
from .error import *
from .protocol import SMPPClientProtocol, SMPPServerProtocol
from .stats import SMPPClientStatsCollector, SMPPServerStatsCollector
from .validation import SmppsCredentialValidator

LOG_CATEGORY_CLIENT_BASE = "smpp.client"
LOG_CATEGORY_SERVER_BASE = "smpp.server"


class SmppClientIsNotConnected(Exception):
    """
    An exception that is raised when a trying to use smpp object when
    it is still None (before callbacking bind())
    """


class SMPPClientFactory(ClientFactory):
    protocol = SMPPClientProtocol

    def __init__(self, config, msgHandler=None):
        self.reconnectTimer = None
        self.smpp = None
        self.connectionRetry = True
        self.config = config

        # Setup statistics collector
        self.stats = SMPPClientStatsCollector().get(cid=self.config.id)
        self.stats.set('created_at', datetime.now())

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY_CLIENT_BASE+".%s" % config.id)
        if len(self.log.handlers) != 1:
            self.log.setLevel(config.log_level)
            _when = self.config.log_rotate if hasattr(self.config, 'log_rotate') else 'midnight'
            handler = TimedRotatingFileHandler(filename=self.config.log_file, when=_when)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        if msgHandler is None:
            self.msgHandler = self.msgHandlerStub
        else:
            self.msgHandler = msgHandler

    def buildProtocol(self, addr):
        """Provision protocol
        """
        proto = ClientFactory.buildProtocol(self, addr)

        # Setup logger
        proto.log = self.log

        return proto

    def getConfig(self):
        return self.config

    def msgHandlerStub(self, smpp, pdu):
        self.log.warn("msgHandlerStub: Received an unhandled message %s ...", pdu)

    def startedConnecting(self, connector):
        self.log.info("Connecting to %s ...", connector.getDestination())

    def getExitDeferred(self):
        """Get a Deferred so you can be notified on disconnect and exited
        This deferred is called once disconnection occurs without a further
        reconnection retrys
        """
        return self.exitDeferred

    def clientConnectionFailed(self, connector, reason):
        """Connection failed
        """
        self.log.error("Connection failed. Reason: %s", str(reason))

        if self.config.reconnectOnConnectionFailure and self.connectionRetry:
            self.log.info("Reconnecting after %d seconds ...",
                          self.config.reconnectOnConnectionFailureDelay)
            self.reconnectTimer = reactor.callLater(
                self.config.reconnectOnConnectionFailureDelay, self.reConnect, connector)
        else:
            self.connectDeferred.errback(reason)
            self.exitDeferred.callback(None)
            self.log.info("Exiting.")

    def clientConnectionLost(self, connector, reason):
        """Connection lost
        """
        self.log.error("Connection lost. Reason: %s", str(reason))

        if self.config.reconnectOnConnectionLoss and self.connectionRetry:
            self.log.info("Reconnecting after %d seconds ...",
                          self.config.reconnectOnConnectionLossDelay)
            self.reconnectTimer = reactor.callLater(
                self.config.reconnectOnConnectionLossDelay, self.reConnect, connector)
        else:
            self.exitDeferred.callback(None)
            self.log.info("Exiting.")

    def reConnect(self, connector=None):
        if connector is None:
            self.log.error("No connector to retry !")
        else:
            # Reset deferred if it were called before
            if self.connectDeferred.called is True:
                self.connectDeferred = defer.Deferred()
                self.connectDeferred.addCallback(self.bind)

            # And try to connect again
            connector.connect()

    def _connect(self):
        self.connectionRetry = True

        if self.config.useSSL:
            self.log.info('Establishing SSL connection to %s:%d', self.config.host, self.config.port)
            reactor.connectSSL(self.config.host, self.config.port, self, CtxFactory(self.config))
        else:
            self.log.info('Establishing TCP connection to %s:%d', self.config.host, self.config.port)
            reactor.connectTCP(self.config.host, self.config.port, self)

        self.exitDeferred = defer.Deferred()
        self.connectDeferred = defer.Deferred()
        return self.connectDeferred

    def connectAndBind(self):
        self._connect()
        self.connectDeferred.addCallback(self.bind)

        return self.connectDeferred

    def disconnect(self):
        if self.smpp is not None:
            self.log.info('Disconnecting SMPP client')
            return self.smpp.unbindAndDisconnect()
        else:
            return None

    def stopConnectionRetrying(self):
        """This will stop the factory from reconnecting
        It is used whenever a service stop has been requested, the connectionRetry flag
        is reset to True upon connect() call
        """

        self.log.info('Stopped automatic connection retrying.')
        if self.reconnectTimer and self.reconnectTimer.active():
            self.reconnectTimer.cancel()
            self.reconnectTimer = None

        self.connectionRetry = False

    def disconnectAndDontRetryToConnect(self):
        self.log.info('Ordering a disconnect with no further reconnections.')
        self.stopConnectionRetrying()
        return self.disconnect()

    def bind(self, smpp):
        self.smpp = smpp

        if self.config.bindOperation == 'transceiver':
            return smpp.bindAsTransceiver()
        elif self.config.bindOperation == 'receiver':
            return smpp.bindAsReceiver()
        elif self.config.bindOperation == 'transmitter':
            return smpp.bindAsTransmitter()
        else:
            raise SMPPClientError("Invalid bind operation: %s" % self.config.bindOperation)

    def getSessionState(self):
        if self.smpp is None:
            return None
        else:
            return self.smpp.sessionState


class CtxFactory(ssl.ClientContextFactory):

    def __init__(self, config):
        self.smppConfig = config

    def getContext(self):
        self.method = SSL.SSLv23_METHOD
        ctx = ssl.ClientContextFactory.getContext(self)
        if self.smppConfig.SSLCertificateFile:
            ctx.use_certificate_file(self.smppConfig.SSLCertificateFile)
        return ctx


class SMPPServerFactory(_SMPPServerFactory):
    protocol = SMPPServerProtocol

    def __init__(self, config, auth_portal, RouterPB=None, SMPPClientManagerPB=None,
                 interceptorpb_client=None):
        self.config = config
        # A dict of protocol instances for each of the current connections,
        # indexed by system_id
        self.bound_connections = {}
        self._auth_portal = auth_portal
        self.RouterPB = RouterPB
        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.interceptorpb_client = interceptorpb_client

        # Setup statistics collector
        self.stats = SMPPServerStatsCollector().get(cid=self.config.id)
        self.stats.set('created_at', datetime.now())

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY_SERVER_BASE+".%s" % config.id)
        if len(self.log.handlers) != 1:
            self.log.setLevel(config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, when=self.config.log_rotate)
            formatter = logging.Formatter(config.log_format, config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        self.msgHandler = self.submit_sm_event_interceptor

    def addInterceptorPBClient(self, interceptorpb_client):
        self.interceptorpb_client = interceptorpb_client

        self.log.info('Added Interceptor to SMPPServerFactory')

    def submit_sm_event_interceptor(self, system_id, *args):
        "Intercept submit_sm befor handing it to self.submit_sm_event"
        self.log.debug('Intercepting submit_sm event for system_id: %s', system_id)

        # Args validation
        if len(args) != 2:
            self.log.error('(submit_sm_event/%s) Invalid args: %s', system_id, args)
            raise SubmitSmInvalidArgsError()
        if not isinstance(args[1], pdu_types.PDURequest):
            self.log.error(
                '(submit_sm_event/%s) Received an unknown object when waiting for a PDURequest: %s',
                system_id,
                args[1])
            raise SubmitSmInvalidArgsError()
        if args[1].id != pdu_types.CommandId.submit_sm:
            self.log.error('(submit_sm_event/%s) Received a non submit_sm command id: %s',
                           system_id, args[1].id)
            raise SubmitSmInvalidArgsError()
        if not isinstance(args[0], SMPPServerProtocol):
            self.log.error(
                '(submit_sm_event/%s) Received an unknown object when waiting for a SMPPServerProtocol: %s',
                system_id,
                args[0])
            raise SubmitSmInvalidArgsError()

        proto = args[0]
        user = proto.user
        SubmitSmPDU = args[1]

        # Update CnxStatus
        user.getCnxStatus().smpps['submit_sm_request_count'] += 1

        # Basic validation
        if len(SubmitSmPDU.params['destination_addr']) < 1 or SubmitSmPDU.params['destination_addr'] is None:
            self.log.error('(submit_sm_event/%s) SubmitSmPDU have no defined destination_addr', system_id)
            raise SubmitSmWithoutDestinationAddrError()

        # Make Credential validation
        v = SmppsCredentialValidator('Send', user, SubmitSmPDU)
        v.validate()

        # Update SubmitSmPDU by default values from user MtMessagingCredential
        SubmitSmPDU = v.updatePDUWithUserDefaults(SubmitSmPDU)

        if self.RouterPB is None:
            self.log.error('(submit_sm_event_interceptor/%s) RouterPB not set: submit_sm will not be routed',
                           system_id)
            return

        # Prepare for interception then routing
        routable = RoutableSubmitSm(SubmitSmPDU, user)

        # Interception inline
        # @TODO: make Interception in a thread, just like httpapi interception
        interceptor = self.RouterPB.getMTInterceptionTable().getInterceptorFor(routable)
        if interceptor is not None:
            self.log.debug("RouterPB selected %s interceptor for this SubmitSmPDU", interceptor)
            if self.interceptorpb_client is None:
                self.stats.inc('interceptor_error_count')
                self.log.error("InterceptorPB not set !")
                raise InterceptorNotSetError('InterceptorPB not set !')
            if not self.interceptorpb_client.isConnected:
                self.stats.inc('interceptor_error_count')
                self.log.error("InterceptorPB not connected !")
                raise InterceptorNotConnectedError('InterceptorPB not connected !')

            script = interceptor.getScript()
            self.log.debug("Interceptor script loaded: %s", script)

            # Run !
            d = self.interceptorpb_client.run_script(script, routable)
            d.addCallback(self.submit_sm_post_interception, system_id=system_id, proto=proto)
            d.addErrback(self.submit_sm_post_interception)
            return d
        else:
            return self.submit_sm_post_interception(routable=routable, system_id=system_id, proto=proto)

    def submit_sm_post_interception(self, *args, **kw):
        """This event handler will deliver the submit_sm to the right smppc connector.
        Note that Jasmin deliver submit_sm messages like this:
        - from httpapi to smppc (handled in jasmin.protocols.http.server)
        - from smpps to smppc (this event handler)

        Note: This event handler MUST behave exactly like jasmin.protocols.http.server.Send.render
        """

        try:
            # Init message id & status
            message_id = None
            status = None

            # Post interception:
            if len(args) == 1:
                if isinstance(args[0], bool) and not args[0]:
                    self.stats.inc('interceptor_error_count')
                    self.log.error('Failed running interception script, got a False return.')
                    raise InterceptorRunError('Failed running interception script, check log for details')
                elif isinstance(args[0], dict) and args[0]['smpp_status'] > 0:
                    self.stats.inc('interceptor_error_count')
                    self.log.error('Interceptor script returned %s smpp_status error.', args[0]['smpp_status'])
                    raise SubmitSmInterceptionError(code=args[0]['smpp_status'])
                elif isinstance(args[0], dict) and args[0]['smpp_status'] == 0:
                    self.stats.inc('interceptor_count')
                    self.log.error('Interceptor script returned %s success smpp_status.', args[0]['smpp_status'])
                    raise SubmitSmInterceptionSuccess()
                elif isinstance(args[0], str):
                    self.stats.inc('interceptor_count')
                    routable = pickle.loads(args[0])
                else:
                    self.stats.inc('interceptor_error_count')
                    self.log.error('Failed running interception script, got the following return: %s',
                                   args[0])
                    raise InterceptorRunError(
                        'Failed running interception script, got the following return: %s' % args[0])
            else:
                routable = kw['routable']

            system_id = kw['system_id']
            proto = kw['proto']

            self.log.debug('Handling submit_sm_post_interception event for system_id: %s', system_id)

            # Get the route
            route = self.RouterPB.getMTRoutingTable().getRouteFor(routable)
            if route is None:
                self.log.error("No route matched from user %s for SubmitSmPDU: %s",
                               routable.user, routable.pdu)
                raise SubmitSmRouteNotFoundError()

            # Get connector from selected route
            self.log.debug("RouterPB selected %s route for this SubmitSmPDU", route)
            routedConnector = route.getConnector()
            # Is it a failover route ? then check for a bound connector, otherwise don't route
            # The failover route requires at least one connector to be up, no message enqueuing will
            # occur otherwise.
            if repr(route) == 'FailoverMTRoute':
                self.log.debug('Selected route is a failover, will ensure connector is bound:')
                while True:
                    c = self.SMPPClientManagerPB.perspective_connector_details(routedConnector.cid)
                    if c:
                        self.log.debug('Connector [%s] is: %s', routedConnector.cid, c['session_state'])
                    else:
                        self.log.debug('Connector [%s] is not found', routedConnector.cid)

                    if c and c['session_state'][:6] == 'BOUND_':
                        # Choose this connector
                        break
                    else:
                        # Check next connector, None if no more connectors are available
                        routedConnector = route.getConnector()
                        if routedConnector is None:
                            break

            if routedConnector is None:
                self.log.error("Failover route has no bound connector to handle SubmitSmPDU: %s",
                               routable.pdu)
                raise SubmitSmRoutingError()

            # QoS throttling
            if (routable.user.mt_credential.getQuota('smpps_throughput') >= 0
                    and routable.user.getCnxStatus().smpps['qos_last_submit_sm_at'] != 0):
                qos_throughput_second = 1 / float(routable.user.mt_credential.getQuota('smpps_throughput'))
                qos_throughput_ysecond_td = timedelta(microseconds=qos_throughput_second * 1000000)
                qos_delay = datetime.now() - routable.user.getCnxStatus().smpps['qos_last_submit_sm_at']
                if qos_delay < qos_throughput_ysecond_td:
                    self.log.error(
                        "QoS: submit_sm_event is faster (%s) than fixed throughput (%s) for user (%s), rejecting message.",
                        qos_delay,
                        qos_throughput_ysecond_td,
                        routable.user)

                    raise SubmitSmThroughputExceededError()
            routable.user.getCnxStatus().smpps['qos_last_submit_sm_at'] = datetime.now()

            # Pre-sending submit_sm: Billing processing
            bill = route.getBillFor(routable.user)
            self.log.debug("SubmitSmBill [bid:%s] [ttlamounts:%s] generated for this SubmitSmPDU",
                           bill.bid, bill.getTotalAmounts())
            charging_requirements = []
            u_balance = routable.user.mt_credential.getQuota('balance')
            u_subsm_count = routable.user.mt_credential.getQuota('submit_sm_count')
            if u_balance is not None and bill.getTotalAmounts() > 0:
                # Ensure user have enough balance to pay submit_sm and submit_sm_resp
                charging_requirements.append({
                    'condition': bill.getTotalAmounts() <= u_balance,
                    'error_message': 'Not enough balance (%s) for charging: %s' % (
                        u_balance, bill.getTotalAmounts())})
            if u_subsm_count is not None:
                # Ensure user have enough submit_sm_count to to cover the bill action (decrement_submit_sm_count)
                charging_requirements.append({
                    'condition': bill.getAction('decrement_submit_sm_count') <= u_subsm_count,
                    'error_message': 'Not enough submit_sm_count (%s) for charging: %s' % (
                        u_subsm_count, bill.getAction('decrement_submit_sm_count'))})

            if self.RouterPB.chargeUserForSubmitSms(routable.user, bill, requirements=charging_requirements) is None:
                self.log.error('Charging user %s failed, [bid:%s] [ttlamounts:%s] (check router log)',
                               routable.user, bill.bid, bill.getTotalAmounts())
                raise SubmitSmChargingError()

            # Get priority value from SubmitSmPDU to pass to SMPPClientManagerPB.perspective_submit_sm()
            priority = 0
            if routable.pdu.params['priority_flag'] is not None:
                priority = routable.pdu.params['priority_flag'].index

            if self.SMPPClientManagerPB is None:
                self.log.error(
                    '(submit_sm_event/%s) SMPPClientManagerPB not set: submit_sm will not be submitted',
                    system_id)
                return

            ########################################################
            # Send SubmitSmPDU through smpp client manager PB server
            self.log.debug("Connector '%s' is set to be a route for this SubmitSmPDU", routedConnector.cid)
            c = self.SMPPClientManagerPB.perspective_submit_sm(
                cid=routedConnector.cid,
                SubmitSmPDU=routable.pdu,
                submit_sm_bill=bill,
                priority=priority,
                pickled=False,
                source_connector=proto)

            if not hasattr(c, 'result'):
                self.log.error('Failed to send SubmitSmPDU to [cid:%s], got: %s', routedConnector.cid, c)
                raise SubmitSmRoutingError()

            # Build final response
            if not c.result:
                self.log.error('Failed to send SubmitSmPDU to [cid:%s]', routedConnector.cid)
                raise SubmitSmRoutingError()

            # Otherwise, message_id is defined on ESME_ROK
            message_id = c.result
        except (SubmitSmInterceptionError, SubmitSmInterceptionSuccess, InterceptorRunError,
                SubmitSmRouteNotFoundError, SubmitSmThroughputExceededError, SubmitSmChargingError,
                SubmitSmRoutingError) as e:
            # Known exception handling
            status = e.status
        except Exception, e:
            # Unknown exception handling
            self.log.critical('Got an unknown exception: %s', e)
            status = pdu_types.CommandStatus.ESME_RUNKNOWNERR
        else:
            self.log.debug('SubmitSmPDU sent to [cid:%s], result = %s', routedConnector.cid, c.result)
            self.log.info(
                'SMS-MT [uid:%s] [cid:%s] [msgid:%s] [prio:%s] [from:%s] [to:%s] [content:%s]',
                routable.user.uid,
                routedConnector.cid,
                c.result,
                priority,
                routable.pdu.params['source_addr'],
                routable.pdu.params['destination_addr'],
                re.sub(r'[^\x20-\x7E]+', '.', routable.pdu.params['short_message']))
            status = pdu_types.CommandStatus.ESME_ROK
        finally:
            if message_id is not None:
                return DataHandlerResponse(status=status, message_id=c.result)
            elif status is not None:
                return DataHandlerResponse(status=status)

    def buildProtocol(self, addr):
        """Provision protocol with the dedicated logger
        """
        proto = _SMPPServerFactory.buildProtocol(self, addr)

        # Setup logger
        proto.log = self.log

        return proto

    def addBoundConnection(self, connection, user):
        """
        Overloading _SMPPServerFactory to remove dependency with config.systems
        Jasmin removed systems from config as everything about credentials is
        managed through User object
        """
        system_id = connection.system_id
        self.log.debug('Adding SMPP binding for %s', system_id)
        if system_id not in self.bound_connections:
            self.bound_connections[system_id] = SMPPBindManager(user)
        self.bound_connections[system_id].addBinding(connection)
        bind_type = connection.bind_type
        self.log.info("Added %s bind for '%s'. Active binds: %s.",
                      bind_type, system_id, self.getBoundConnectionCountsStr(system_id))

    def removeConnection(self, connection):
        """
        Overloading _SMPPServerFactory to remove dependency with config.systems
        Jasmin removed systems from config as everything about credentials is
        managed through User object
        """
        if connection.system_id is None:
            self.log.debug("SMPP connection attempt failed without binding.")
        else:
            system_id = connection.system_id
            bind_type = connection.bind_type
            self.bound_connections[system_id].removeBinding(connection)
            self.log.info("Dropped %s bind for '%s'. Active binds: %s.",
                          bind_type, system_id, self.getBoundConnectionCountsStr(system_id))
            # If this is the last binding for this service then remove the BindManager
            if self.bound_connections[system_id].getBindingCount() == 0:
                self.bound_connections.pop(system_id)

    def canOpenNewConnection(self, user, bind_type):
        """
        Overloading _SMPPServerFactory to remove dependency with config.systems
        Jasmin removed systems from config as everything about credentials is
        managed through User object
        This method will check for authorization and quotas before allowing a new
        connection
        """
        # Can bind ?
        if not user.smpps_credential.getAuthorization('bind'):
            self.log.warning(
                'New bind rejected for username: "%s", reason: authorization failure.', user.username)
            return False
        # Still didnt reach max_bindings ?
        elif user.smpps_credential.getQuota('max_bindings') is not None:
            bind_count = user.getCnxStatus().smpps['bound_connections_count']['bind_transmitter']
            bind_count += user.getCnxStatus().smpps['bound_connections_count']['bind_receiver']
            bind_count += user.getCnxStatus().smpps['bound_connections_count']['bind_transceiver']
            if bind_count >= user.smpps_credential.getQuota('max_bindings'):
                self.log.warning('New bind rejected for username: "%s", reason: max_bindings limit reached.',
                                 user.username)
                return False

        return True

    def unbindAndRemoveGateway(self, user, ban=True):
        """
        Overloading _SMPPServerFactory to remove dependency with config.systems
        Jasmin removed systems from config as everything about credentials is
        managed through User object.
        It's also adding a 'ban' parameter to optionally remove binding authorization
        for user.
        """
        if ban:
            user.smpps_credential.setAuthorization('bind', False)

        d = self.unbindGateway(user.username)
        return d


class SMPPBindManager(_SMPPBindManager):
    "Overloads _SMPPBindManager to add user tracking"

    def __init__(self, user):
        _SMPPBindManager.__init__(self, system_id=user.username)

        self.user = user

    def addBinding(self, connection):
        _SMPPBindManager.addBinding(self, connection)

        # Update CnxStatus
        self.user.getCnxStatus().smpps['bind_count'] += 1
        self.user.getCnxStatus().smpps['bound_connections_count'][str(connection.bind_type)] += 1

    def removeBinding(self, connection):
        _SMPPBindManager.removeBinding(self, connection)

        # Update CnxStatus
        self.user.getCnxStatus().smpps['unbind_count'] += 1
        self.user.getCnxStatus().smpps['bound_connections_count'][str(connection.bind_type)] -= 1
