"""This script will log all sent sms through Jasmin with user information.

Requirement:
- Activate publish_submit_sm_resp in jasmin.cfg
- submit_log table:
  CREATE TABLE submit_log (
    `msgid`            VARCHAR(45) PRIMARY KEY,
    `source_addr`      VARCHAR(40),
    `destination_addr` VARCHAR(40) NOT NULL CHECK (`destination_addr` <> ''),
    `pdu_count`        TINYINT(3) DEFAULT 1,
    `short_message`    BLOB,
    `binary_message`   BLOB,
    `status`           VARCHAR(15) NOT NULL CHECK (`status` <> ''),
    `uid`              VARCHAR(15) NOT NULL CHECK (`uid` <> ''),
    `created_at`       DATETIME NOT NULL,
    INDEX `sms_log_1` (`status`),
    INDEX `sms_log_2` (`uid`),
    INDEX `sms_log_3` (`created_at`),
    INDEX `sms_log_4` (`created_at`, `uid`),
    INDEX `sms_log_5` (`created_at`, `uid`, `status`)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8 COLLATE=utf8_unicode_ci;

TODO:
- Add billing info
"""

import pickle
import binascii
from twisted.internet.defer import inlineCallbacks
from twisted.internet import reactor
from twisted.internet.protocol import ClientCreator
from twisted.python import log

from txamqp.protocol import AMQClient
from txamqp.client import TwistedDelegate

import txamqp.spec

import MySQLdb as mdb

q = {}

@inlineCallbacks
def gotConnection(conn, username, password):
    print "Connected to broker."
    yield conn.authenticate(username, password)

    print "Authenticated. Ready to receive messages"
    chan = yield conn.channel(1)
    yield chan.channel_open()

    yield chan.queue_declare(queue="sms_logger_queue")

    # Bind to submit.sm.* and submit.sm.resp.* routes
    yield chan.queue_bind(queue="sms_logger_queue", exchange="messaging", routing_key='submit.sm.*')
    yield chan.queue_bind(queue="sms_logger_queue", exchange="messaging", routing_key='submit.sm.resp.*')

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
        pdu = pickle.loads(msg.content.body)

        if msg.routing_key[:15] == 'submit.sm.resp.':
            if props['message-id'] not in q:
                print 'Got resp of an unknown submit_sm: %s' % props['message-id']
                continue

            qmsg = q[props['message-id']]

            if qmsg['source_addr'] is None:
                qmsg['source_addr'] = ''

            cursor.execute("""INSERT INTO submit_log (msgid, source_addr, pdu_count,
                                                      destination_addr, short_message,
                                                      status, uid, created_at, binary_message)
                    VALUES (%s, %s, %s, %s, %s, %s, %s, %s);
                """, (
                        props['message-id'],
                        qmsg['source_addr'],
                        qmsg['pdu_count'],
                        qmsg['destination_addr'],
                        qmsg['short_message'],
                        pdu.status,
                        qmsg['bill'].user.uid,
                        props['headers']['created_at'],
                        qmsg['binary_message'],
                    )
                )
            db.commit()
        elif msg.routing_key[:10] == 'submit.sm.':
            pdu_count = 1
            short_message = pdu.params['short_message']

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
            if pdu.params['data_coding'] == 8:
                short_message = short_message.decode('utf_16_be')

            q[props['message-id']] = {
                'bill': pickle.loads(props['headers']['submit_sm_resp_bill']),
                'destination_addr': pdu.params['destination_addr'],
                'source_addr': pdu.params['source_addr'],
                'pdu_count': pdu_count,
                'short_message': short_message.encode('utf_8'),
                'binary_message': binary_message,
            }
        else:
            print 'unknown route'

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
