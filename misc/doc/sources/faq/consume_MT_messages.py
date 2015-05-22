# Gist from https://gist.github.com/zoufou/5701d71bf6e404d17cb4
import pickle
from twisted.internet.defer import inlineCallbacks
from twisted.internet import reactor
from twisted.internet.protocol import ClientCreator
from twisted.python import log

from txamqp.protocol import AMQClient
from txamqp.client import TwistedDelegate

import txamqp.spec

@inlineCallbacks
def gotConnection(conn, username, password):
    print "Connected to broker."
    yield conn.authenticate(username, password)

    print "Authenticated. Ready to receive messages"
    chan = yield conn.channel(1)
    yield chan.channel_open()

    yield chan.queue_declare(queue="someQueueName")

    # Bind to submit.sm.* and submit.sm.resp.* routes
    yield chan.queue_bind(queue="someQueueName", exchange="messaging", routing_key='submit.sm.*')
    yield chan.queue_bind(queue="someQueueName", exchange="messaging", routing_key='submit.sm.resp.*')

    yield chan.basic_consume(queue='someQueueName', no_ack=True, consumer_tag="someTag")
    queue = yield conn.queue("someTag")

    # Wait for messages
    # This can be done through a callback ...
    while True:
        msg = yield queue.get()
        props = msg.content.properties
        pdu = pickle.loads(msg.content.body)

    	if msg.routing_key[:15] == 'submit.sm.resp.':
    		print 'SubmitSMResp: status: %s, msgid: %s' % (pdu.status, 
    			props['message-id'])
        elif msg.routing_key[:10] == 'submit.sm.':
        	print 'SubmitSM: from %s to %s, content: %s, msgid: %s' % (pdu.params['source_addr'], 
        		pdu.params['destination_addr'],
        		pdu.params['short_message'],
        		props['message-id'])
    	else:
    		print 'unknown route'

    # A clean way to tear down and stop
    yield chan.basic_cancel("someTag")
    yield chan.channel_close()
    chan0 = yield conn.channel(0)
    yield chan0.connection_close()

    reactor.stop()


if __name__ == "__main__":
    """
    This example will connect to RabbitMQ broker and consume from two route keys:
      - submit.sm.*: All messages sent through SMPP Connectors
      - submit.sm.resp.*: More relevant than SubmitSM because it contains the sending status

    Note:
      - Messages consumed from submit.sm.resp.* are not verbose enough, they contain only message-id and status
      - Message content can be obtained from submit.sm.*, the message-id will be the same when consuming from submit.sm.resp.*,
        it is used for mapping.
      - Billing information is contained in messages consumed from submit.sm.*
      - This is a proof of concept, saying anyone can consume from any topic in Jasmin's exchange hack a
        third party business, more information here: http://docs.jasminsms.com/en/latest/messaging/index.html
    """

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