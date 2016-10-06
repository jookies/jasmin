"""This script will log all sent sms through Jasmin with user information.

Requirement:
- Activate publish_submit_sm_resp in jasmin.cfg
- submit_log table:
  CREATE TABLE submit_log (
    `msgid`            VARCHAR(45) PRIMARY KEY,
    `source_connector` VARCHAR(15),
    `routed_cid`       VARCHAR(30),
    `source_addr`      VARCHAR(40),
    `destination_addr` VARCHAR(40) NOT NULL CHECK (`destination_addr` <> ''),
    `rate`             DECIMAL(12, 7),
    `pdu_count`        TINYINT(3) DEFAULT 1,
    `short_message`    BLOB,
    `binary_message`   BLOB,
    `status`           VARCHAR(15) NOT NULL CHECK (`status` <> ''),
    `uid`              VARCHAR(15) NOT NULL CHECK (`uid` <> ''),
    `trials`           TINYINT(4) DEFAULT 1,
    `created_at`       DATETIME NOT NULL,
    `status_at`        DATETIME NOT NULL,
    INDEX `sms_log_1` (`status`),
    INDEX `sms_log_2` (`uid`),
    INDEX `sms_log_3` (`routed_cid`),
    INDEX `sms_log_4` (`created_at`),
    INDEX `sms_log_5` (`created_at`, `uid`),
    INDEX `sms_log_6` (`created_at`, `uid`, `status`)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8 COLLATE=utf8_unicode_ci;
"""

import cPickle as pickle
import binascii
from datetime import datetime
from twisted.internet.defer import inlineCallbacks
from twisted.internet import reactor
from twisted.internet.protocol import ClientCreator
from twisted.python import log
from txamqp.protocol import AMQClient
from txamqp.client import TwistedDelegate
import txamqp.spec

from jasmin.vendor.smpp.pdu.pdu_types import DataCoding

import MySQLdb as mdb

q = {}

@inlineCallbacks
def gotConnection(conn, username, password):
    print "Connected to broker, authenticating: %s" % username
    yield conn.start({"LOGIN": username, "PASSWORD": password})

    print "Authenticated. Ready to receive messages"
    chan = yield conn.channel(1)
    yield chan.channel_open()

    yield chan.queue_declare(queue="sms_logger_queue")

    # Bind to submit.sm.* and submit.sm.resp.* routes to track sent messages
    yield chan.queue_bind(queue="sms_logger_queue", exchange="messaging", routing_key='submit.sm.*')
    yield chan.queue_bind(queue="sms_logger_queue", exchange="messaging", routing_key='submit.sm.resp.*')
    # Bind to dlr_thrower.* to track DLRs
    yield chan.queue_bind(queue="sms_logger_queue", exchange="messaging", routing_key='dlr_thrower.*')

    yield chan.basic_consume(queue='sms_logger_queue', no_ack=False, consumer_tag="sms_logger")
    queue = yield conn.queue("sms_logger")

    #Connection parameters - Fill this info with your MySQL server connection parameters
    db = mdb.connect(
        user='jasmin',
        passwd='jadmin',
        host='127.0.0.1',
        db='jasmin')

    print "Connected to MySQL"
    cursor = db.cursor()

    # Wait for messages
    # This can be done through a callback ...
    while True:
        msg = yield queue.get()
        props = msg.content.properties

        if msg.routing_key[:10] == 'submit.sm.' and msg.routing_key[:15] != 'submit.sm.resp.':
            pdu = pickle.loads(msg.content.body)
            pdu_count = 1
            short_message = pdu.params['short_message']
            billing = props['headers']
            billing_pickle = billing.get('submit_sm_resp_bill')
            if not billing_pickle:
                billing_pickle = billing.get('submit_sm_bill')
            submit_sm_bill = pickle.loads(billing_pickle)
            source_connector = props['headers']['source_connector']
            routed_cid = msg.routing_key[10:]

            # Is it a multipart message ?
            while hasattr(pdu, 'nextPdu'):
                # Remove UDH from first part
                if pdu_count == 1:
                    short_message = short_message[6:]

                pdu = pdu.nextPdu

                # Update values:
                pdu_count += 1
                short_message += pdu.params['short_message'][6:]

            # Save short_message bytes
            binary_message = binascii.hexlify(short_message)

            # If it's a binary message, assume it's utf_16_be encoded
            if pdu.params['data_coding'] is not None:
                dc = pdu.params['data_coding']
                if (isinstance(dc, int) and dc == 8) or (isinstance(dc, DataCoding) and str(dc.schemeData) == 'UCS2'):
                    short_message = short_message.decode('utf_16_be', 'ignore').encode('utf_8')

            q[props['message-id']] = {
                'source_connector': source_connector,
                'routed_cid': routed_cid,
                'rate': submit_sm_bill.getTotalAmounts() * pdu_count,
                'uid': submit_sm_bill.user.uid,
                'destination_addr': pdu.params['destination_addr'],
                'source_addr': pdu.params['source_addr'],
                'pdu_count': pdu_count,
                'short_message': short_message,
                'binary_message': binary_message,
            }
        elif msg.routing_key[:15] == 'submit.sm.resp.':
            # It's a submit_sm_resp

            pdu = pickle.loads(msg.content.body)
            if props['message-id'] not in q:
                print 'Got resp of an unknown submit_sm: %s' % props['message-id']
                chan.basic_ack(delivery_tag=msg.delivery_tag)
                continue

            qmsg = q[props['message-id']]

            if qmsg['source_addr'] is None:
                qmsg['source_addr'] = ''

            cursor.execute("""INSERT INTO submit_log (msgid, source_addr, rate, pdu_count,
                                                      destination_addr, short_message,
                                                      status, uid, created_at, binary_message,
                                                      routed_cid, source_connector, status_at)
                    VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
                    ON DUPLICATE KEY UPDATE trials = trials + 1;""", (
                props['message-id'],
                qmsg['source_addr'],
                qmsg['rate'],
                qmsg['pdu_count'],
                qmsg['destination_addr'],
                qmsg['short_message'],
                pdu.status,
                qmsg['uid'],
                props['headers']['created_at'],
                qmsg['binary_message'],
                qmsg['routed_cid'],
                qmsg['source_connector'],
                props['headers']['created_at'],))
            db.commit()
        elif msg.routing_key[:12] == 'dlr_thrower.':
            if props['headers']['message_status'][:5] == 'ESME_':
                # Ignore dlr from submit_sm_resp
                chan.basic_ack(delivery_tag=msg.delivery_tag)
                continue

            # It's a dlr
            if props['message-id'] not in q:
                print 'Got dlr of an unknown submit_sm: %s' % props['message-id']
                chan.basic_ack(delivery_tag=msg.delivery_tag)
                continue

            # Update message status
            qmsg = q[props['message-id']]
            cursor.execute("UPDATE submit_log SET status = %s, status_at = %s WHERE msgid = %s;", (
                props['headers']['message_status'],
                datetime.now(),
                props['message-id'],))
            db.commit()
        else:
            print 'unknown route: %s' % msg.routing_key

        chan.basic_ack(delivery_tag=msg.delivery_tag)

    # A clean way to tear down and stop
    yield chan.basic_cancel("sms_logger")
    yield chan.channel_close()
    chan0 = yield conn.channel(0)
    yield chan0.connection_close()

    reactor.stop()


if __name__ == "__main__":
    host = '127.0.0.1'
    port = 5672
    vhost = '/'
    username = 'guest'
    password = 'guest'
    spec_file = '/etc/jasmin/resource/amqp0-9-1.xml'

    spec = txamqp.spec.load(spec_file)

    # Connect and authenticate
    d = ClientCreator(reactor,
        AMQClient,
        delegate=TwistedDelegate(),
        vhost=vhost,
        spec=spec).connectTCP(host, port)
    d.addCallback(gotConnection, username, password)

    def whoops(err):
        if reactor.running:
            log.err(err)
            reactor.stop()

    d.addErrback(whoops)

    reactor.run()
