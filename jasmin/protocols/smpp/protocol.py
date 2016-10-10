#pylint: disable=W0401,W0611
import logging
import re
import struct
import uuid
from datetime import datetime

from twisted.cred import error
from twisted.internet import defer, reactor

from jasmin.vendor.smpp.pdu.constants import data_coding_default_value_map
from jasmin.vendor.smpp.pdu.operations import *
from jasmin.vendor.smpp.pdu.pdu_types import CommandStatus, DataCoding, DataCodingDefault
from jasmin.vendor.smpp.twisted.protocol import SMPPClientProtocol as twistedSMPPClientProtocol
from jasmin.vendor.smpp.twisted.protocol import SMPPServerProtocol as twistedSMPPServerProtocol
from jasmin.vendor.smpp.twisted.protocol import (SMPPSessionStates, SMPPOutboundTxn,
                                                 SMPPOutboundTxnResult)
from .error import *

#@todo: LOG_CATEGORY seems to be unused, check before removing it
LOG_CATEGORY = "smpp.twisted.protocol"

class SMPPClientProtocol(twistedSMPPClientProtocol):
    def __init__(self):
        twistedSMPPClientProtocol.__init__(self)

        self.longSubmitSmTxns = {}

    def PDUReceived(self, pdu):
        self.log.debug("SMPP Client received PDU [command: %s, seq_number: %s, command_status: %s]",
                       pdu.commandId, pdu.seqNum, pdu.status)
        self.log.debug("Complete PDU dump: %s", pdu)
        self.factory.stats.set('last_received_pdu_at', datetime.now())

        #A better version than vendor's PDUReceived method:
        #- Dont re-encode pdu !
        #if self.log.isEnabledFor(logging.DEBUG):
        #    encoded = self.encoder.encode(pdu)
        #    self.log.debug("Receiving data [%s]" % _safelylogOutPdu(encoded))

        #Signal SMPP operation
        self.onSMPPOperation()

        if isinstance(pdu, PDURequest):
            self.PDURequestReceived(pdu)
        elif isinstance(pdu, PDUResponse):
            self.PDUResponseReceived(pdu)
        else:
            getattr(self, "onPDU_%s" % str(pdu.commandId))(pdu)

    def connectionMade(self):
        twistedSMPPClientProtocol.connectionMade(self)
        self.factory.stats.set('connected_at', datetime.now())
        self.factory.stats.inc('connected_count')

        self.log.info("Connection made to %s:%s", self.config().host, self.config().port)

        self.factory.connectDeferred.callback(self)

    def connectionLost(self, reason):
        twistedSMPPClientProtocol.connectionLost(self, reason)

        self.factory.stats.set('disconnected_at', datetime.now())
        self.factory.stats.inc('disconnected_count')

    def doPDURequest(self, reqPDU, handler):
        twistedSMPPClientProtocol.doPDURequest(self, reqPDU, handler)

        # Stats
        if reqPDU.commandId == CommandId.enquire_link:
            self.factory.stats.set('last_received_elink_at', datetime.now())
        elif reqPDU.commandId == CommandId.deliver_sm:
            self.factory.stats.inc('deliver_sm_count')
        elif reqPDU.commandId == CommandId.data_sm:
            self.factory.stats.inc('data_sm_count')

    def PDUResponseReceived(self, pdu):
        twistedSMPPClientProtocol.PDUResponseReceived(self, pdu)

        if pdu.commandId == CommandId.submit_sm_resp:
            if pdu.status == CommandStatus.ESME_RTHROTTLED:
                self.factory.stats.inc('throttling_error_count')
            elif pdu.status != CommandStatus.ESME_ROK:
                self.factory.stats.inc('other_submit_error_count')
            else:
                # We got a ESME_ROK
                self.factory.stats.inc('submit_sm_count')

    def sendPDU(self, pdu):
        twistedSMPPClientProtocol.sendPDU(self, pdu)

        # Stats:
        self.factory.stats.set('last_sent_pdu_at', datetime.now())
        if pdu.commandId == CommandId.enquire_link:
            self.factory.stats.set('last_sent_elink_at', datetime.now())
            self.factory.stats.inc('elink_count')
        elif pdu.commandId == CommandId.submit_sm:
            self.factory.stats.inc('submit_sm_request_count')

    def claimSeqNum(self):
        seqNum = twistedSMPPClientProtocol.claimSeqNum(self)

        self.factory.stats.set('last_seqNum_at', datetime.now())
        self.factory.stats.set('last_seqNum', seqNum)

        return seqNum

    def bindSucceeded(self, result, nextState):
        self.factory.stats.set('bound_at', datetime.now())
        self.factory.stats.inc('bound_count')

        return twistedSMPPClientProtocol.bindSucceeded(self, result, nextState)

    def bindAsReceiver(self):
        """This is a different signature where msgHandler is taken from factory
        """
        return twistedSMPPClientProtocol.bindAsReceiver(self, self.factory.msgHandler)

    def bindAsTransceiver(self):
        """This is a different signature where msgHandler is taken from factory
        """
        return twistedSMPPClientProtocol.bindAsTransceiver(self, self.factory.msgHandler)

    def bindFailed(self, reason):
        self.log.error("Bind failed [%s]. Disconnecting...", reason)
        self.disconnect()
        if reason.check(SMPPRequestTimoutError):
            raise SMPPSessionInitTimoutError(str(reason))

    def endOutboundTransaction(self, respPDU):
        txn = self.closeOutboundTransaction(respPDU.seqNum)

        # Any status of a SubmitSMResp must be handled as a normal status
        if isinstance(txn.request, SubmitSM) or respPDU.status == CommandStatus.ESME_ROK:
            if not isinstance(respPDU, txn.request.requireAck):
                txn.ackDeferred.errback(
                    SMPPProtocolError, "Invalid PDU response type [%s] returned for request type [%s]" % (
                        type(respPDU), type(txn.request)))
                return
            #Do callback
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))
            return

        if isinstance(respPDU, GenericNack):
            txn.ackDeferred.errback(SMPPGenericNackTransactionError(respPDU, txn.request))
            return

        txn.ackDeferred.errback(SMPPTransactionError(respPDU, txn.request))

    def cancelOutboundTransactions(self, err):
        """Cancels LongSubmitSmTransactions when cancelling OutboundTransactions
        """
        twistedSMPPClientProtocol.cancelOutboundTransactions(self, err)
        self.cancelLongSubmitSmTransactions(err)

    def cancelLongSubmitSmTransactions(self, err):
        for item in self.longSubmitSmTxns.values():
            reqPDU = item['txn'].request

            self.log.exception(err)
            txn = self.closeLongSubmitSmTransaction(reqPDU.LongSubmitSm['msg_ref_num'])
            #Do errback
            txn.ackDeferred.errback(err)

    def startLongSubmitSmTransaction(self, reqPDU, timeout):
        if reqPDU.LongSubmitSm['msg_ref_num'] in self.longSubmitSmTxns:
            self.log.error(
                'Transaction with msg_ref_num [%s] is already in progress, open longSubmitSmTxns count: %s',
                reqPDU.LongSubmitSm['msg_ref_num'],
                len(self.longSubmitSmTxns))
            raise LongSubmitSmTransactionError(
                'Transaction with msg_ref_num [%s] already in progress.' % reqPDU.LongSubmitSm['msg_ref_num'])

        #Create callback deferred
        ackDeferred = defer.Deferred()
        #Create response timer
        timer = reactor.callLater(timeout, self.onResponseTimeout, reqPDU, timeout)
        #Save transaction
        self.longSubmitSmTxns[reqPDU.LongSubmitSm['msg_ref_num']] = {
            'txn' : SMPPOutboundTxn(reqPDU, timer, ackDeferred),
            'nack_count' : reqPDU.LongSubmitSm['total_segments']}
        self.log.debug("Long submit_sm transaction started with msg_ref_num %s",
                       reqPDU.LongSubmitSm['msg_ref_num'])
        return ackDeferred

    def closeLongSubmitSmTransaction(self, msg_ref_num):
        self.log.debug("Long submit_sm transaction finished with msg_ref_num %s", msg_ref_num)

        txn = self.longSubmitSmTxns[msg_ref_num]['txn']
        # Remove txn
        del self.longSubmitSmTxns[msg_ref_num]
        # Cancel response timer
        if txn.timer.active():
            txn.timer.cancel()

        return txn

    def endLongSubmitSmTransaction(self, _SMPPOutboundTxnResult):
        reqPDU = _SMPPOutboundTxnResult.request
        respPDU = _SMPPOutboundTxnResult.response

        # Do we have txn with the given ref ?
        if reqPDU.LongSubmitSm['msg_ref_num'] not in self.longSubmitSmTxns:
            self.log.error(
                'Received a submit_sm_resp in a unknown transaction with msg_ref_num [%s], open longSubmitSmTxns count: %s',
                reqPDU.LongSubmitSm['msg_ref_num'],
                len(self.longSubmitSmTxns)
            )
            raise LongSubmitSmTransactionError(
                'Received a submit_sm_resp in a unknown transaction with msg_ref_num [%s].' % reqPDU.LongSubmitSm['msg_ref_num'])

        # Decrement pending ACKs
        if self.longSubmitSmTxns[reqPDU.LongSubmitSm['msg_ref_num']]['nack_count'] > 0:
            self.longSubmitSmTxns[reqPDU.LongSubmitSm['msg_ref_num']]['nack_count'] -= 1
            self.log.debug(
                "Long submit_sm transaction with msg_ref_num %s has been updated, nack_count: %s",
                reqPDU.LongSubmitSm['msg_ref_num'],
                self.longSubmitSmTxns[reqPDU.LongSubmitSm['msg_ref_num']]['nack_count'])

        # End the transaction if no more pending ACKs
        if self.longSubmitSmTxns[reqPDU.LongSubmitSm['msg_ref_num']]['nack_count'] == 0:
            txn = self.closeLongSubmitSmTransaction(reqPDU.LongSubmitSm['msg_ref_num'])

            #Do callback
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))

    def endLongSubmitSmTransactionErr(self, failure):
        # Return on generic NACK
        try:
            failure.raiseException()
        except SMPPClientConnectionCorruptedError as _:
            return

    def preSubmitSm(self, pdu):
        """Will:
        - Make validation steps
        - Transform unparseable data (because SubmitSm may come from http-api through PB)
        """
        # Convert data_coding from int to DataCoding object
        if 'data_coding' in pdu.params and isinstance(pdu.params['data_coding'], int):
            intVal = pdu.params['data_coding']
            if intVal in data_coding_default_value_map:
                name = data_coding_default_value_map[intVal]
                pdu.params['data_coding'] = DataCoding(schemeData=getattr(DataCodingDefault, name))
            else:
                pdu.params['data_coding'] = None

        # Set default source_addr if not defined
        if pdu.params['source_addr'] is None and self.config().source_addr is not None:
            pdu.params['source_addr'] = self.config().source_addr

    def doSendRequest(self, pdu, timeout):
        if self.connectionCorrupted:
            raise SMPPClientConnectionCorruptedError()
        if not isinstance(pdu, PDURequest) or pdu.requireAck is None:
            raise SMPPClientError("Invalid PDU to send: %s" % pdu)

        if pdu.commandId == CommandId.submit_sm:
            # Start a LongSubmitSmTransaction if pdu is a long submit_sm and send multiple
            # pdus, each with an OutboundTransaction
            # - Every OutboundTransaction is closed upon receiving the correct submit_sm_resp
            # - Every LongSubmitSmTransaction is closed upong closing all included OutboundTransactions
            #
            # Update 20150709 #234:
            # If the pdu has no nextPdu attribute then it may be a part of a long message not managed
            # by Jasmin: it may come from SMPPs already parted, in this case Jasmin must pass the
            # message as is without starting LongSubmitSmTransaction.
            # The downside of this behaviour is that each part of the message will be logged in a single
            # line in messages.log

            # UDH is set ?
            UDHI_INDICATOR_SET = False
            if hasattr(pdu.params['esm_class'], 'gsmFeatures'):
                for gsmFeature in pdu.params['esm_class'].gsmFeatures:
                    if str(gsmFeature) == 'UDHI_INDICATOR_SET':
                        UDHI_INDICATOR_SET = True
                        break

            # Discover any splitting method, otherwise, it is a single SubmitSm
            if 'sar_msg_ref_num' in pdu.params:
                splitMethod = 'sar'
            elif UDHI_INDICATOR_SET and pdu.params['short_message'][:3] == '\x05\x00\x03':
                splitMethod = 'udh'
            else:
                splitMethod = None

            if splitMethod is not None and hasattr(pdu, 'nextPdu'):
                partedSmPdu = pdu
                first = True

                # Iterate through parted PDUs
                while True:
                    partedSmPdu.seqNum = self.claimSeqNum()

                    # Set LongSubmitSm tracking flags in pdu:
                    partedSmPdu.LongSubmitSm = {'msg_ref_num': None, 'total_segments': None,
                                                'segment_seqnum': None}
                    if splitMethod == 'sar':
                        # Using SAR options:
                        partedSmPdu.LongSubmitSm['msg_ref_num'] = partedSmPdu.params['sar_msg_ref_num']
                        partedSmPdu.LongSubmitSm['total_segments'] = partedSmPdu.params['sar_total_segments']
                        partedSmPdu.LongSubmitSm['segment_seqnum'] = partedSmPdu.params['sar_segment_seqnum']
                    elif splitMethod == 'udh':
                        # Using UDH options:
                        partedSmPdu.LongSubmitSm['msg_ref_num'] = struct.unpack('!B', pdu.params['short_message'][3])[0]
                        partedSmPdu.LongSubmitSm['total_segments'] = struct.unpack('!B', pdu.params['short_message'][4])[0]
                        partedSmPdu.LongSubmitSm['segment_seqnum'] = struct.unpack('!B', pdu.params['short_message'][5])[0]

                    self.preSubmitSm(partedSmPdu)
                    self.sendPDU(partedSmPdu)
                    # Unlike parent protocol's sendPDU, we don't return per pdu
                    # deferred, we'll return per transaction deferred instead
                    self.startOutboundTransaction(
                        partedSmPdu, timeout).addCallbacks(self.endLongSubmitSmTransaction,
                                                           self.endLongSubmitSmTransactionErr)

                    # Start a transaction using the first parted PDU
                    if first:
                        first = False
                        txn = self.startLongSubmitSmTransaction(partedSmPdu, timeout)

                    try:
                        # There still another PDU to go for
                        partedSmPdu = partedSmPdu.nextPdu
                    except AttributeError:
                        break

                return txn
            else:
                self.preSubmitSm(pdu)

        return twistedSMPPClientProtocol.doSendRequest(self, pdu, timeout)

    def sendDataRequest(self, pdu):
        """If pdu has a 'vendor_specific_bypass' tag, it will be deleted before sending it

        This is a workaround to let Jasmin accepts messages with vendor TLVs but not forwarding them
        to upstream connectors.

        Related to #325
        """
        if pdu.commandId == CommandId.submit_sm and 'vendor_specific_bypass' in pdu.params:
            del pdu.params['vendor_specific_bypass']

        return twistedSMPPClientProtocol.sendDataRequest(self, pdu)

class SMPPServerProtocol(twistedSMPPServerProtocol):
    def __init__(self):
        twistedSMPPServerProtocol.__init__(self)

        # Divert received messages to the handler defined in the config
        # Note:
        # twistedSMPPServerProtocol is using a msgHandler from self.config(), this
        # SMPPServerProtocol is using self.factory's msgHandler just like SMPPClientProtocol
        self.dataRequestHandler = lambda *args: self.factory.msgHandler(self.system_id, *args)
        self.system_id = None
        self.user = None
        self.bind_type = None
        self.session_id = str(uuid.uuid4())
        self.log = logging.getLogger(LOG_CATEGORY)

    def PDUReceived(self, pdu):
        self.log.debug(
            "SMPP Server received PDU from system '%s' [command: %s, seq_number: %s, command_status: %s]",
            self.system_id, pdu.commandId, pdu.seqNum, pdu.status)
        self.log.debug("Complete PDU dump: %s", pdu)
        self.factory.stats.set('last_received_pdu_at', datetime.now())

        #A better version than vendor's PDUReceived method:
        #- Dont re-encode pdu !
        #if self.log.isEnabledFor(logging.DEBUG):
        #    encoded = self.encoder.encode(pdu)
        #    self.log.debug("Receiving data [%s]" % _safelylogOutPdu(encoded))

        #Signal SMPP operation
        self.onSMPPOperation()

        if isinstance(pdu, PDURequest):
            self.PDURequestReceived(pdu)
        elif isinstance(pdu, PDUResponse):
            self.PDUResponseReceived(pdu)
        else:
            getattr(self, "onPDU_%s" % str(pdu.commandId))(pdu)

    def connectionMade(self):
        twistedSMPPServerProtocol.connectionMade(self)
        self.factory.stats.inc('connect_count')
        self.factory.stats.inc('connected_count')

    def connectionLost(self, reason):
        twistedSMPPServerProtocol.connectionLost(self, reason)

        self.factory.stats.inc('disconnect_count')
        self.factory.stats.dec('connected_count')
        if self.sessionState in [SMPPSessionStates.BOUND_RX,
                                 SMPPSessionStates.BOUND_TX,
                                 SMPPSessionStates.BOUND_TRX]:
            if str(self.bind_type) == 'bind_transceiver':
                self.factory.stats.dec('bound_trx_count')
            elif str(self.bind_type) == 'bind_receiver':
                self.factory.stats.dec('bound_rx_count')
            elif str(self.bind_type) == 'bind_transmitter':
                self.factory.stats.dec('bound_tx_count')

    def onPDURequest_enquire_link(self, reqPDU):
        twistedSMPPServerProtocol.onPDURequest_enquire_link(self, reqPDU)

        self.factory.stats.set('last_received_elink_at', datetime.now())
        self.factory.stats.inc('elink_count')
        if self.user is not None:
            self.user.getCnxStatus().smpps['elink_count'] += 1

    def doPDURequest(self, reqPDU, handler):
        twistedSMPPServerProtocol.doPDURequest(self, reqPDU, handler)

        # Stats
        if reqPDU.commandId == CommandId.enquire_link:
            self.factory.stats.set('last_received_elink_at', datetime.now())
        elif reqPDU.commandId == CommandId.submit_sm:
            self.factory.stats.inc('submit_sm_request_count')

    def sendPDU(self, pdu):
        twistedSMPPServerProtocol.sendPDU(self, pdu)

        # Prepare for logging
        if pdu.commandId in [CommandId.deliver_sm, CommandId.data_sm]:
            message_content = pdu.params.get('short_message', None)
            if message_content is None:
                message_content = pdu.params.get('message_payload', '')

        # Stats:
        self.factory.stats.set('last_sent_pdu_at', datetime.now())
        if pdu.commandId == CommandId.deliver_sm:
            self.factory.stats.inc('deliver_sm_count')
            if self.user is not None:
                self.log.info(
                    'DELIVER_SM [uid:%s] [from:%s] [to:%s] [content:%s]',
                    self.user.uid,
                    pdu.params['source_addr'],
                    pdu.params['destination_addr'],
                    re.sub(r'[^\x20-\x7E]+', '.', message_content))
                self.user.getCnxStatus().smpps['deliver_sm_count'] += 1
        elif pdu.commandId == CommandId.data_sm:
            self.factory.stats.inc('data_sm_count')
            if self.user is not None:
                self.log.info('DATA_SM [uid:%s] [from:%s] [to:%s] [content:%s]',
                              self.user.uid,
                              pdu.params['source_addr'],
                              pdu.params['destination_addr'],
                              re.sub(r'[^\x20-\x7E]+', '.', message_content))
                self.user.getCnxStatus().smpps['data_sm_count'] += 1
        elif pdu.commandId == CommandId.submit_sm_resp:
            if pdu.status == CommandStatus.ESME_RTHROTTLED:
                self.factory.stats.inc('throttling_error_count')
                if self.user is not None:
                    self.user.getCnxStatus().smpps['throttling_error_count'] += 1
            elif pdu.status != CommandStatus.ESME_ROK:
                self.factory.stats.inc('other_submit_error_count')
                if self.user is not None:
                    self.user.getCnxStatus().smpps['other_submit_error_count'] += 1
            else:
                # We got a ESME_ROK
                self.factory.stats.inc('submit_sm_count')
                if self.user is not None:
                    self.user.getCnxStatus().smpps['submit_sm_count'] += 1


    def onPDURequest_unbind(self, reqPDU):
        twistedSMPPServerProtocol.onPDURequest_unbind(self, reqPDU)

        self.factory.stats.inc('unbind_count')
        if str(self.bind_type) == 'bind_transceiver':
            self.factory.stats.dec('bound_trx_count')
        elif str(self.bind_type) == 'bind_receiver':
            self.factory.stats.dec('bound_rx_count')
        elif str(self.bind_type) == 'bind_transmitter':
            self.factory.stats.dec('bound_tx_count')

    def PDUDataRequestReceived(self, reqPDU):
        if self.sessionState == SMPPSessionStates.BOUND_RX:
            # Don't accept submit_sm PDUs when BOUND_RX
            errMsg = 'Received submit_sm when BOUND_RX %s' % reqPDU
            self.cancelOutboundTransactions(SessionStateError(errMsg, CommandStatus.ESME_RINVBNDSTS))
            return self.fatalErrorOnRequest(reqPDU, errMsg, CommandStatus.ESME_RINVBNDSTS)

        return twistedSMPPServerProtocol.PDUDataRequestReceived(self, reqPDU)

    def PDURequestReceived(self, reqPDU):
        # Handle only accepted command ids
        acceptedPDUs = [CommandId.submit_sm, CommandId.bind_transmitter,
                        CommandId.bind_receiver, CommandId.bind_transceiver,
                        CommandId.unbind, CommandId.unbind_resp,
                        CommandId.enquire_link, CommandId.data_sm]
        if reqPDU.commandId not in acceptedPDUs:
            errMsg = 'Received unsupported pdu type: %s' % reqPDU.commandId
            self.cancelOutboundTransactions(SessionStateError(errMsg, CommandStatus.ESME_RSYSERR))
            return self.fatalErrorOnRequest(reqPDU, errMsg, CommandStatus.ESME_RSYSERR)

        twistedSMPPServerProtocol.PDURequestReceived(self, reqPDU)

        # Update CnxStatus
        if self.user is not None:
            self.user.getCnxStatus().smpps['last_activity_at'] = datetime.now()

    @defer.inlineCallbacks
    def doBindRequest(self, reqPDU, sessionState):
        bind_type = reqPDU.commandId

        # Update stats
        if str(bind_type) == 'bind_transceiver':
            self.factory.stats.inc('bind_trx_count')
        elif str(bind_type) == 'bind_receiver':
            self.factory.stats.inc('bind_rx_count')
        elif str(bind_type) == 'bind_transmitter':
            self.factory.stats.inc('bind_tx_count')

        # Check the authentication
        username, password = reqPDU.params['system_id'], reqPDU.params['password']

        # Authenticate username and password
        try:
            iface, auth_avatar, logout = yield self.factory.login(
                username,
                password,
                self.transport.getPeer().host)
        except error.UnauthorizedLogin, e:
            self.log.debug('From host %s and using password: %s', self.transport.getPeer().host, password)
            self.log.warning('SMPP Bind request failed for username: "%s", reason: %s', username, str(e))
            self.sendErrorResponse(reqPDU, CommandStatus.ESME_RINVPASWD, username)
            return

        # Check we're not already bound, and are open to being bound
        if self.sessionState != SMPPSessionStates.OPEN:
            self.log.warning('Duplicate SMPP bind request received from: %s', username)
            self.sendErrorResponse(reqPDU, CommandStatus.ESME_RALYBND, username)
            return

        # Check that username hasn't exceeded number of allowed binds
        if not self.factory.canOpenNewConnection(auth_avatar, bind_type):
            self.log.warning('SMPP System %s has exceeded maximum number of %s bindings',
                             username, bind_type)
            self.sendErrorResponse(reqPDU, CommandStatus.ESME_RBINDFAIL, username)
            return

        # If we get to here, bind successfully
        self.user = auth_avatar
        self.system_id = username
        self.sessionState = sessionState
        self.bind_type = bind_type

        self.factory.addBoundConnection(self, self.user)
        bound_cnxns = self.factory.getBoundConnections(self.system_id)
        self.log.debug('Bind request succeeded for %s in session [%s]. %d active binds',
                       username, self.session_id, bound_cnxns.getBindingCount() if bound_cnxns else 0)
        self.sendResponse(reqPDU, system_id=self.system_id)

        # Update stats
        if str(bind_type) == 'bind_transceiver':
            self.factory.stats.inc('bound_trx_count')
        elif str(bind_type) == 'bind_receiver':
            self.factory.stats.inc('bound_rx_count')
        elif str(bind_type) == 'bind_transmitter':
            self.factory.stats.inc('bound_tx_count')

    def sendDataRequest(self, pdu):
        """If pdu has a 'vendor_specific_bypass' tag, it will be deleted before sending it

        This is a workaround to let Jasmin accepts messages with vendor TLVs but not forwarding them
        to downstream users.

        Related to #325
        """
        if pdu.commandId == CommandId.deliver_sm and 'vendor_specific_bypass' in pdu.params:
            del pdu.params['vendor_specific_bypass']

        return twistedSMPPServerProtocol.sendDataRequest(self, pdu)
