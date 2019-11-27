"""
Multiple classes extending of txamqp.content.Content
"""

import cPickle as pickle
import datetime
import uuid

from txamqp.content import Content

from pkg_resources import iter_entry_points


class InvalidParameterError(Exception):
    """Raised when a parameter is invalid
    """


# msgid generator is a pluggable method, make the lookup to find an existant
# plugin or use the default one (randomUniqueId)
randomUniqueId = lambda pdu_type, uid, source_cid, destination_cid: str(uuid.uuid4())
for entry_point in iter_entry_points(group='jasmin.content', name='msgid'):
    print("Hooking randomUniqueId() from %s" % entry_point.dist)
    randomUniqueId = entry_point.load()
    # Takes the first one from the iteration
    break


class PDU(Content):
    """A generic SMPP PDU Content"""

    pickleProtocol = pickle.HIGHEST_PROTOCOL

    def __init__(self, body="", children=None, properties=None, pickleProtocol=2, prePickle=False):
        self.pickleProtocol = pickleProtocol

        if prePickle is True:
            body = pickle.dumps(body, self.pickleProtocol)

        # Add creation date in header
        if 'headers' not in properties:
            properties['headers'] = {}
        properties['headers']['created_at'] = str(datetime.datetime.now())

        Content.__init__(self, body, children, properties)


class DLR(Content):
    """A DLR is published to dlr.* routes for DLRLookup"""

    def __init__(self, pdu_type, msgid, status, smpp_msgid=None, cid=None, dlr_details=None):
        pdu_type_s = '%s' % pdu_type
        status_s = '%s' % status

        if pdu_type_s not in ['deliver_sm', 'data_sm', 'submit_sm_resp']:
            raise InvalidParameterError('Invalid pdu_type: %s' % pdu_type_s)

        if pdu_type_s == 'submit_sm_resp' and status_s == 'ESME_ROK' and smpp_msgid is None:
            raise InvalidParameterError('submit_sm_resp with ESME_ROK dlr must have smpp_msgid arg defined')
        elif pdu_type_s in ['deliver_sm', 'data_sm'] and (cid is None or dlr_details is None):
            raise InvalidParameterError('deliver_sm dlr must have cid and dlr_details args defined')

        properties = {'message-id': str(msgid), 'headers': {'type': pdu_type_s}}

        if pdu_type_s == 'submit_sm_resp' and smpp_msgid is not None:
            # smpp_msgid is used to define mapping between msgid and smpp_msgid (when receiving submit_sm_resp ESME_ROK)
            properties['headers']['smpp_msgid'] = str(smpp_msgid).upper().lstrip('0')
        elif pdu_type_s in ['deliver_sm', 'data_sm']:
            properties['headers']['cid'] = cid
            for k, v in dlr_details.iteritems():
                properties['headers']['dlr_%s' % k] = v

        Content.__init__(self, status_s, properties=properties)


class DLRContentForHttpapi(Content):
    """A DLR Content holding information about the origin SubmitSm sent from httpapi and
    receipt acknowledgment details"""

    def __init__(self, message_status, msgid, dlr_url, dlr_level, dlr_connector='unknown', id_smsc='', sub='',
                 dlvrd='', subdate='', donedate='', err='', text='', method='POST', trycount=0):

        # ESME_* statuses are returned from SubmitSmResp
        # Others are returned from DeliverSm, values must be the same as Table B-2
        if message_status[:5] != 'ESME_' and message_status not in ['DELIVRD', 'EXPIRED', 'DELETED',
                                                                    'UNDELIV', 'ACCEPTD', 'UNKNOWN', 'REJECTD']:
            raise InvalidParameterError("Invalid message_status: %s" % message_status)
        if dlr_level not in [1, 2, 3]:
            raise InvalidParameterError("Invalid dlr_level: %s" % dlr_level)
        if method not in ['POST', 'GET']:
            raise InvalidParameterError('Invalid method: %s' % method)

        properties = {'message-id': msgid, 'headers': {'try-count': 0,
                                                       'url': dlr_url,
                                                       'method': method,
                                                       'message_status': message_status,
                                                       'level': dlr_level,
                                                       'id_smsc': id_smsc,
                                                       'sub': sub,
                                                       'dlvrd': dlvrd,
                                                       'subdate': subdate,
                                                       'donedate': donedate,
                                                       'err': err,
                                                       'connector': dlr_connector,
                                                       'text': text}}

        Content.__init__(self, msgid, properties=properties)


class DLRContentForSmpps(Content):
    """A DLR Content holding information about the origin SubmitSm sent from smpps and
    receipt acknowledgment details"""

    def __init__(self, message_status, msgid, system_id, source_addr, destination_addr, sub_date,
                 source_addr_ton, source_addr_npi, dest_addr_ton, dest_addr_npi, err=99):
        # ESME_* statuses are returned from SubmitSmResp
        # Others are returned from DeliverSm, values must be the same as Table B-2
        if message_status[:5] != 'ESME_' and message_status not in ['DELIVRD', 'EXPIRED', 'DELETED',
                                                                    'UNDELIV', 'ACCEPTD', 'UNKNOWN', 'REJECTD']:
            raise InvalidParameterError("Invalid message_status: %s" % message_status)

        properties = {'message-id': msgid, 'headers': {'try-count': 0,
                                                       'message_status': message_status,
                                                       'err': err,
                                                       'system_id': system_id,
                                                       'source_addr': source_addr,
                                                       'destination_addr': destination_addr,
                                                       'sub_date': str(sub_date),
                                                       'source_addr_ton': source_addr_ton,
                                                       'source_addr_npi': source_addr_npi,
                                                       'dest_addr_ton': dest_addr_ton,
                                                       'dest_addr_npi': dest_addr_npi}}

        Content.__init__(self, msgid, properties=properties)


class SubmitSmContent(PDU):
    """A SMPP SubmitSm Content"""

    def __init__(self, uid, body, replyto, submit_sm_bill, priority=1, expiration=None, msgid=None,
                 source_connector='httpapi', destination_cid=None):
        props = {}

        # RabbitMQ does not support priority (yet), anyway, we may use any other amqp broker that supports it
        if not isinstance(priority, int):
            raise InvalidParameterError("Invalid priority argument: %s" % priority)
        if priority < 0 or priority > 3:
            raise InvalidParameterError("Priority must be set from 0 to 3, it is actually set to %s" %
                                        priority)
        if source_connector not in ['httpapi', 'smppsapi']:
            raise InvalidParameterError('Invalid source_connector value: %s.' % source_connector)
        if msgid is None:
            msgid = randomUniqueId('submit_sm', uid, source_connector, destination_cid)

        props['priority'] = priority
        props['message-id'] = msgid
        props['reply-to'] = replyto

        props['headers'] = {'source_connector': source_connector,
                            'submit_sm_bill': submit_sm_bill}
        if expiration is not None:
            props['headers']['expiration'] = expiration

        PDU.__init__(self, body, properties=props)


class SubmitSmRespContent(PDU):
    """A SMPP SubmitSmResp Content"""

    def __init__(self, body, msgid, pickleProtocol=2, prePickle=True):
        props = {'message-id': msgid}

        PDU.__init__(self, body, properties=props, pickleProtocol=pickleProtocol, prePickle=prePickle)


class DeliverSmContent(PDU):
    """A SMPP DeliverSm Content"""

    def __init__(self, body, sourceCid, pickleProtocol=2, prePickle=True,
                 concatenated=False, will_be_concatenated=False):
        props = {}

        props['message-id'] = randomUniqueId('deliver_sm', None, sourceCid, None)

        # For routing purpose, connector-id indicates the source connector of the PDU
        props['headers'] = {'try-count': 0,
                            'connector-id': sourceCid,
                            'concatenated': concatenated,
                            'will_be_concatenated': will_be_concatenated}

        PDU.__init__(self, body, properties=props, pickleProtocol=pickleProtocol, prePickle=prePickle)


class SubmitSmRespBillContent(Content):
    """A Bill Content holding amount to be charged to user (uid)"""

    def __init__(self, bid, uid, amount):
        if not isinstance(amount, float) and not isinstance(amount, int):
            raise InvalidParameterError('Amount is not float or int: %s' % amount)
        if amount < 0:
            raise InvalidParameterError('Amount cannot be a negative value: %s' % amount)

        properties = {'message-id': bid, 'headers': {'user-id': uid, 'amount': str(amount)}}

        Content.__init__(self, bid, properties=properties)
