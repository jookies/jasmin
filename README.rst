.. image:: https://raw.githubusercontent.com/jookies/jasmin/master/misc/doc/sources/_static/jasmin-logo-small.png

Jasmin - Open source SMS gateway
################################

|ci| |docs|

Introduction
************
Jasmin is a very complete open source SMS Gateway with many enterprise-class features such as:

* SMPP Client / Server
* HTTP Client / Server
* Console-based configuration, no service restart required
* Based on AMQP broker for store&forward mechanisms and other queuing systems
* Using Redis for in-memory DLR tracking and billing
* Advanced message routing/filtering (Simple, Roundrobin, Failover, Leastcost ...)
* Web and console ui for management
* Supports Unicode (UTF-8) for sending out multilingual SMS
* Supports easy creation and sending of specialized/binary SMS like mono Ringtones, WAP Push, Vcards
* Supports concatenated SMS strings (long SMS)

Jasmin relies heavily on message queuing through message brokers (Using AMQP), it is designed for performance, 
high traffic loads and full in-memory execution.

Architecture
************

.. figure:: https://github.com/jookies/jasmin/raw/master/misc/doc/sources/resources/architecture/hld.png
   :alt: HLD Architecture
   :align: Center
   :figwidth: 100%

Jasmin core and its external connectors (used for AMQP, Redis, SMPP, HTTP, Telnet ...) are written in Python 
and are mainly based on `Twisted matrix <https://twistedmatrix.com/>`_, an event-driven networking engine.

Getting started
***************
Refer to `Installation steps <http://docs.jasminsms.com/en/latest/installation/index.html>`_ to get Jasmin gateway up and running quickly with a simple SMS sending scenario.

Documentation
*************
The documentation is at: http://docs.jasminsms.com

If you still need to build it locally::

  $ cd misc/doc
  $ make html
  ...
  ...
  Build finished. The HTML pages are in build/html.

Links
*****

* `Project home page <http://www.jasminsms.com>`_
* `Documentation <http://docs.jasminsms.com>`_
* `Support <https://groups.google.com/forum/#!forum/jasmin-sms-gateway>`_
* `Source code <http://github.com/jookies/jasmin>`_
* `Travis CI <https://travis-ci.org/jookies/jasmin>`_

License
*******
Jasmin is released under the terms of the [Apache License Version 2]. See **`LICENSE`** file for details.

Credits
*******

* SMPP PDU parsing based on smpp.pdu: https://github.com/mozes/smpp.pdu
* SMPP 3.4 Client based on smpp.twisted: https://github.com/mozes/smpp.twisted
* GSMP 3.38 based on Python messaging: https://github.com/pmarti/python-messaging

Change Log
**********

0.6 (*under development*)
=========================

* Setting "bind_ton 1" on connector is broken #97
* Check Tarik's bug (dlr-level and the value of registered_delivery) #87
* SMPP Client is disconnecting when receiving a submit_sm_resp with 0x00000106 (Destination Address Sub unit is Invalid.
* SMPP Server #49
* Publishing submit_sm_resp must be optional #66
* Implement NetworkErrorCodeEncoder() #65
* bind_ton and src_ton settings on connector not working #104
* A numeric password set through jCli will cause an exception when trying to bind #105
* Add validity period parameter in http api #109
* Incorrect http dlr-level parameter mapping to smpp registered_delivery parameter #107
* Incorrect comparison of bound_connections_count #114
* Tuning: change redis set to setex
* Add throughput in User MTCredentials and apply it on SMPP and HTTP #51
* PDUParseError #120
* Command Length is invalid #124
* PDUCorruptError: Command Length is invalid #128
* Error on receiving message on smpp connector #117
* need configuration to send deliver_sm instead of data_sm for SMPP delivery receipt #139
* Error when throwing deliver_sm through smpp: deliverSmThrowerConfig instance has no attribute 'max_retries' #147
* Services don´t start unless jasmin.cfg is missing or commented #144
* Fix problem with numeric logins and passwords #138
* Receipts are not transmitted to smpp client #153
* Expose real time statistics in jCli #123
* SMPP Server: Omit sending deliver_sm receipt when receiving a submit_sm_resp receipt #159
* Implement submit_sm retry on failure depending on the error type (for example ESME_RTHROTTLED ...) #60
* Don't charge messages when sending SMS through SMPP returns an error in submit_sm_resp #164
* CentOS / Red Hat Jasmin init script seems to be not working properly #165
* Provide deb and rpm packages for Jasmin #169
* Dockerize Jasmin #170
* SMPP Server API #49
* Basic statistics in user-level #77
* Delivery retrial on specific/configurable errors #60
* Easier installation procedure through Linux packages #78

0.5
===

* User credentials: validation, default/fallback values and balance/quota management
* Changed system service startup script from twisted tap plugin to bin/jasmind
* Rated routes (for billing purpose)
* Prepaid billing including asynchronous mode
* jCli supports user credentials management
* Updated documentation
* Various fixes

0.4.0
=====

* Setuptools command support
* Documentation hosted on http://docs.jasminsms.com
* Updated documentation
* Various fixes

0.3.0
=====

* Routing for MO and MT flows between HTTP and SMPP connectors
* Management through jCli console
* Updated documentation
* Various fixes

0.2.0
=====

* HTTP Server (for SMS MT sending)
* HTTP Client (for SMS MO and delivery receipts)

0.1.0
=====

* SMPP Client API

.. |ci| image:: https://api.travis-ci.org/jookies/jasmin.png
    :alt: Build status
    :scale: 100%
    :target: https://travis-ci.org/jookies/jasmin

.. |docs| image:: https://readthedocs.org/projects/jasmin/badge/?version=latest
    :alt: Documentation status
    :scale: 100%
    :target: http://docs.jasminsms.com