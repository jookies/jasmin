#!/usr/bin/env python
"""This script will log all sent sms through Jasmin with user information.

Requirement:
- Activate publish_submit_sm_resp in jasmin.cfg
- Install psycopg2:             # Used for PostgreSQL connection
    +   pip install psycopg2
- Install mysql.connector:      # Used for MySQL connection
    +   pip install mysql-connector-python

Optional:
- SET ENVIRONMENT ENV:
    + DB_TYPE_MYSQL     # Default: 1            # 1 for MySQL, 0 for PostgreSQL
    + DB_HOST           # Default: 127.0.0.1    # IP or Docker container name
    + DB_DATABASE       # Default: jasmin       # should Exist
    + DB_TABLE          # Default: submit_log   # the script will create it if it doesn't Exist
    + DB_USER           # Default: jasmin       # for the Database connection.
    + DB_PASS           # Default: jadmin       # for the Database connection
    + AMQP_BROKER_HOST  # Default: 127.0.0.1    # RabbitMQ host used by Jasmin SMS Gateway. IP or Docker container name
    + AMQP_BROKER_PORT  # Default: 5672         # RabbitMQ port used by Jasmin SMS Gateway. IP or Docker container name

Database Scheme:
- MySQL table:
    CREATE TABLE ${DB_TABLE}  (
        `msgid`            VARCHAR(45) PRIMARY KEY,
        `source_connector` VARCHAR(15),
        `routed_cid`       VARCHAR(30),
        `source_addr`      VARCHAR(40),
        `destination_addr` VARCHAR(40) NOT NULL CHECK (`destination_addr` <> ''),
        `rate`             DECIMAL(12, 7),
        `charge`             DECIMAL(12, 7),
        `pdu_count`        TINYINT(3) DEFAULT 1,
        `short_message`    BLOB,
        `binary_message`   BLOB,
        `status`           VARCHAR(15) NOT NULL CHECK (`status` <> ''),
        `uid`              VARCHAR(15) NOT NULL CHECK (`uid` <> ''),
        `trials`           TINYINT(4) DEFAULT 1,
        `created_at`       DATETIME NOT NULL,
        `status_at`        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
        INDEX (`source_connector`),
        INDEX (`routed_cid`),
        INDEX (`source_addr`),
        INDEX (`destination_addr`),
        INDEX (`status`),
        INDEX (`uid`),
        INDEX (`created_at`),
        INDEX (`created_at`, `uid`),
        INDEX (`created_at`, `uid`, `status`),
        INDEX (`created_at`, `routed_cid`),
        INDEX (`created_at`, `routed_cid`, `status`),
        INDEX (`created_at`, `source_connector`),
        INDEX (`created_at`, `source_connector`, `status`)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8 COLLATE=utf8_unicode_ci;
- PostgreSQL table:
    CREATE TABLE IF NOT EXISTS ${DB_TABLE}  (
        msgid VARCHAR(45) NOT NULL PRIMARY KEY,
        source_connector VARCHAR(15) NULL DEFAULT NULL,
        routed_cid VARCHAR(30) NULL DEFAULT NULL,
        source_addr VARCHAR(40) NULL DEFAULT NULL,
        destination_addr VARCHAR(40) NOT NULL CHECK (destination_addr <> ''),
        rate DECIMAL(12,7) NULL DEFAULT NULL,
        charge DECIMAL(12,7) NULL DEFAULT NULL,
        pdu_count SMALLINT NULL DEFAULT '1',
        short_message BYTEA NULL DEFAULT NULL,
        binary_message BYTEA NULL DEFAULT NULL,
        status VARCHAR(15) NOT NULL CHECK (status <> ''),
        uid VARCHAR(15) NOT NULL CHECK (uid <> ''),
        trials SMALLINT NULL DEFAULT '1',
        created_at TIMESTAMP(0) NOT NULL,
        status_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
    );
    CREATE INDEX ON ${DB_TABLE} (source_connector);
    CREATE INDEX ON ${DB_TABLE} (routed_cid);
    CREATE INDEX ON ${DB_TABLE} (source_addr);
    CREATE INDEX ON ${DB_TABLE} (destination_addr);
    CREATE INDEX ON ${DB_TABLE} (status);
    CREATE INDEX ON ${DB_TABLE} (uid);
    CREATE INDEX ON ${DB_TABLE} (created_at);
    CREATE INDEX ON ${DB_TABLE} (created_at, uid);
    CREATE INDEX ON ${DB_TABLE} (created_at, uid, status);
    CREATE INDEX ON ${DB_TABLE} (created_at, routed_cid);
    CREATE INDEX ON ${DB_TABLE} (created_at, routed_cid, status);
    CREATE INDEX ON ${DB_TABLE} (created_at, source_connector);
    CREATE INDEX ON ${DB_TABLE} (created_at, source_connector, status);
"""

import os
from time import sleep
import pickle as pickle
import binascii
from datetime import datetime
from twisted.internet.defer import inlineCallbacks
from twisted.internet import reactor
from twisted.internet.protocol import ClientCreator
from twisted.python import log
from txamqp.protocol import AMQClient
from txamqp.client import TwistedDelegate
import txamqp.spec

from smpp.pdu.pdu_types import DataCoding

from mysql.connector import connect as _mysql_connect
from psycopg2 import pool as _postgres_pool
from psycopg2 import Error as _postgres_error

q = {}

# Database connection parameters
db_type_mysql = int(os.getenv('DB_TYPE_MYSQL', '1')) == 1
db_host = os.getenv('DB_HOST', '127.0.0.1')
db_database = os.getenv('DB_DATABASE', 'jasmin')
db_table = os.getenv('DB_TABLE', 'submit_log')
db_user = os.getenv('DB_USER', 'jasmin')
db_pass = os.getenv('DB_PASS', 'jadmin')
# AMQB broker connection parameters
amqp_broker_host = os.getenv('AMQP_BROKER_HOST', '127.0.0.1')
amqp_broker_port = int(os.getenv('AMQP_BROKER_PORT', '5672'))

def get_psql_conn():
    psql_pool = _postgres_pool.SimpleConnectionPool(
        1,
        20,
        user=db_user, 
        password=db_pass, 
        host=db_host, 
        database=db_database)
    return psql_pool.getconn()

def get_mysql_conn():
    return _mysql_connect(
            user=db_user, 
            password=db_pass,
            host=db_host,
            database=db_database,
            pool_name = "mypool",
            pool_size = 20)

@inlineCallbacks
def gotConnection(conn, username, password):
    print("*** Connected to broker, authenticating: %s" % username, flush=True)
    yield conn.start({"LOGIN": username, "PASSWORD": password})

    print("*** Authenticated. Ready to receive messages", flush=True)
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
    
    if db_type_mysql:
        db_conn = get_mysql_conn()
        if db_conn:
            print("*** Pooling 20 connections", flush=True)
            print("*** Connected to MySQL", flush=True)
    else:
        db_conn = get_psql_conn()
        if db_conn:
            print ("*** Pooling 20 connections", flush=True)
            print ("*** Connected to psql", flush=True)
        

    cursor = db_conn.cursor()
    
    if db_type_mysql:
        create_table = ("""CREATE TABLE IF NOT EXISTS {}  (
                `msgid`            VARCHAR(45) PRIMARY KEY,
                `source_connector` VARCHAR(15),
                `routed_cid`       VARCHAR(30),
                `source_addr`      VARCHAR(40),
                `destination_addr` VARCHAR(40) NOT NULL CHECK (`destination_addr` <> ''),
                `rate`             DECIMAL(12, 7),
                `charge`             DECIMAL(12, 7),
                `pdu_count`        TINYINT(3) DEFAULT 1,
                `short_message`    BLOB,
                `binary_message`   BLOB,
                `status`           VARCHAR(15) NOT NULL CHECK (`status` <> ''),
                `uid`              VARCHAR(15) NOT NULL CHECK (`uid` <> ''),
                `trials`           TINYINT(4) DEFAULT 1,
                `created_at`       DATETIME NOT NULL,
                `status_at`        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
                INDEX (`source_connector`),
                INDEX (`routed_cid`),
                INDEX (`source_addr`),
                INDEX (`destination_addr`),
                INDEX (`status`),
                INDEX (`uid`),
                INDEX (`created_at`),
                INDEX (`created_at`, `uid`),
                INDEX (`created_at`, `uid`, `status`),
                INDEX (`created_at`, `routed_cid`),
                INDEX (`created_at`, `routed_cid`, `status`),
                INDEX (`created_at`, `source_connector`),
                INDEX (`created_at`, `source_connector`, `status`)
            ) ENGINE=InnoDB DEFAULT CHARSET=utf8 COLLATE=utf8_unicode_ci;""".format(db_table))
    else:
        create_table = ("""CREATE TABLE IF NOT EXISTS {}  (
                msgid VARCHAR(45) NOT NULL PRIMARY KEY,
                source_connector VARCHAR(15) NULL DEFAULT NULL,
                routed_cid VARCHAR(30) NULL DEFAULT NULL,
                source_addr VARCHAR(40) NULL DEFAULT NULL,
                destination_addr VARCHAR(40) NOT NULL CHECK (destination_addr <> ''),
                rate DECIMAL(12,7) NULL DEFAULT NULL,
                charge DECIMAL(12,7) NULL DEFAULT NULL,
                pdu_count SMALLINT NULL DEFAULT '1',
                short_message BYTEA NULL DEFAULT NULL,
                binary_message BYTEA NULL DEFAULT NULL,
                status VARCHAR(15) NOT NULL CHECK (status <> ''),
                uid VARCHAR(15) NOT NULL CHECK (uid <> ''),
                trials SMALLINT NULL DEFAULT '1',
                created_at TIMESTAMP(0) NOT NULL,
                status_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
            );
            CREATE INDEX ON {} (source_connector);
            CREATE INDEX ON {} (routed_cid);
            CREATE INDEX ON {} (source_addr);
            CREATE INDEX ON {} (destination_addr);
            CREATE INDEX ON {} (status);
            CREATE INDEX ON {} (uid);
            CREATE INDEX ON {} (created_at);
            CREATE INDEX ON {} (created_at, uid);
            CREATE INDEX ON {} (created_at, uid, status);
            CREATE INDEX ON {} (created_at, routed_cid);
            CREATE INDEX ON {} (created_at, routed_cid, status);
            CREATE INDEX ON {} (created_at, source_connector);
            CREATE INDEX ON {} (created_at, source_connector, status);
            """.format(db_table,db_table,db_table,
                       db_table,db_table,db_table,
                       db_table,db_table,db_table,
                       db_table,db_table,db_table,
                       db_table,db_table,))
        
    cursor.execute(create_table)
    if cursor.rowcount > 0:
        print ('*** {} table was created successfully'.format(db_table), flush=True)
    else:
        print ('*** {} table already exist'.format(db_table), flush=True)
    
    db_conn.commit()
    

    # Wait for messages
    # This can be done through a callback ...
    while True:
        msg = yield queue.get()
        props = msg.content.properties
    
        if db_type_mysql:
            db_conn.ping(reconnect=True, attempts=10, delay=1)
        else:
            check_connection = True
            while check_connection:
                try:
                    cursor = db_conn.cursor()
                    cursor.execute('SELECT 1')
                    check_connection = False
                except _postgres_error:
                    print ('*** PostgreSQL connection exception. Trying to reconnect', flush=True)
                    db_conn = get_psql_conn()
                    if db_conn:
                        print ("*** Pooling 20 connections", flush=True)
                        print ("*** Re-connected to psql", flush=True)
                    cursor = db_conn.cursor()
                    pass

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
                'rate': submit_sm_bill.getTotalAmounts(),
                'charge': submit_sm_bill.getTotalAmounts() * pdu_count,
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
                print('*** Got resp of an unknown submit_sm: %s' % props['message-id'], flush=True)
                chan.basic_ack(delivery_tag=msg.delivery_tag)
                continue

            qmsg = q[props['message-id']]

            if qmsg['source_addr'] is None:
                qmsg['source_addr'] = ''
            
            insert_log = ("""INSERT INTO {} (msgid, source_addr, rate, pdu_count, charge,
                                                      destination_addr, short_message,
                                                      status, uid, created_at, binary_message,
                                                      routed_cid, source_connector, status_at)
                    VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
                    ON DUPLICATE KEY UPDATE trials = trials + 1;""".format(db_table))
            
            cursor.execute(insert_log, (
                props['message-id'],
                qmsg['source_addr'],
                qmsg['rate'],
                qmsg['pdu_count'],
                qmsg['charge'],
                qmsg['destination_addr'],
                qmsg['short_message'],
                pdu.status,
                qmsg['uid'],
                props['headers']['created_at'],
                qmsg['binary_message'],
                qmsg['routed_cid'],
                qmsg['source_connector'],
                props['headers']['created_at'],))
            db_conn.commit()
        elif msg.routing_key[:12] == 'dlr_thrower.':
            if props['headers']['message_status'][:5] == 'ESME_':
                # Ignore dlr from submit_sm_resp
                chan.basic_ack(delivery_tag=msg.delivery_tag)
                continue

            # It's a dlr
            if props['message-id'] not in q:
                print('*** Got dlr of an unknown submit_sm: %s' % props['message-id'], flush=True)
                chan.basic_ack(delivery_tag=msg.delivery_tag)
                continue

            # Update message status
            qmsg = q[props['message-id']]
            update_log = ("UPDATE submit_log SET status = %s, status_at = %s WHERE msgid = %s;".format(db_table))
            cursor.execute(update_log, (
                props['headers']['message_status'],
                datetime.now(),
                props['message-id'],))
            db_conn.commit()
        else:
            print('*** unknown route: %s' % msg.routing_key, flush=True)

        chan.basic_ack(delivery_tag=msg.delivery_tag)

    # A clean way to tear down and stop
    yield chan.basic_cancel("sms_logger")
    yield chan.channel_close()
    chan0 = yield conn.channel(0)
    yield chan0.connection_close()

    reactor.stop()


if __name__ == "__main__":
    sleep(2)
    print(' ', flush=True)
    print(' ', flush=True)
    print('***************** sms_logger *****************', flush=True)
    if db_type_mysql == 1:
        print('*** Staring sms_logger, DB drive: MySQL', flush=True)
    else:
        print('*** Staring sms_logger, DB drive: PostgreSQL', flush=True)
    print('**********************************************', flush=True)
    
    host = amqp_broker_host
    port = amqp_broker_port
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