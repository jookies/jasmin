"""This script will log all sent sms through Jasmin with user information.

Requirement:
- Activate publish_submit_sm_resp in jasmin.cfg
- submit_log table:
  CREATE TABLE submit_log (
    `msgid`            VARCHAR(45) PRIMARY KEY,
    `source_addr`      VARCHAR(40),
    `destination_addr` VARCHAR(40) NOT NULL CHECK (`destination_addr` <> ''),
    `short_message`    BLOB,
    `status`           VARCHAR(15) NOT NULL CHECK (`status` <> ''),
    `uid`              VARCHAR(15) NOT NULL CHECK (`uid` <> ''),
    `created_at`       DATETIME NOT NULL,
    INDEX `sms_log_1` (`status`),
    INDEX `sms_log_2` (`uid`),
    INDEX `sms_log_3` (`created_at`),
    INDEX `sms_log_4` (`created_at`, `uid`),
    INDEX `sms_log_5` (`created_at`, `uid`, `status`)
  );

TODO:
- Add billing info
"""

import pickle
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

    yield chan.basic_consume(queue='sms_logger_queue', no_ack=True, consumer_tag="sms_logger")
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

        if msg.routing_key[:15] == 'submit.sm.resp.' and props['message-id'] in q:
            msg = q[props['message-id']]

            if msg['source_addr'] is None:
                msg['source_addr'] = ''

            cursor.execute("""INSERT INTO submit_log (msgid, source_addr, destination_addr, short_message, status, uid, created_at) 
                    VALUES (%s, %s, %s, %s, %s, %s, %s);
                """, (
                        props['message-id'],
                        msg['source_addr'],
                        msg['destination_addr'],
                        msg['short_message'],
                        pdu.status,
                        msg['bill'].user.uid,
                        props['headers']['created_at'],
                    )
                )
            db.commit()
        elif msg.routing_key[:10] == 'submit.sm.':
            q[props['message-id']] = {
                'bill': pickle.loads(props['headers']['submit_sm_resp_bill']),
                'destination_addr': pdu.params['destination_addr'],
                'source_addr': pdu.params['source_addr'],
                'short_message': pdu.params['short_message'],
            }
        else:
            print 'unknown route'

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