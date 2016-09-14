.. image:: https://raw.githubusercontent.com/jookies/jasmin/master/misc/doc/sources/_static/jasmin-logo-small.png

Jasmin - Open source SMS gateway
################################

|codacy| |landscape| |ci| |docker| |docs|

Introduction
************
Jasmin is a very complete open source SMS Gateway with many enterprise-class features such as:

* SMPP Client / Server
* HTTP Client / Server
* Console-based configuration, no service restart required
* Based on AMQP broker for store&forward mechanisms and other queuing systems
* Using Redis for in-memory DLR tracking and billing
* Advanced message routing/filtering (Simple, Roundrobin, Failover, HLR lookup, Leastcost ...)
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

.. |codacy| image:: https://www.codacy.com/project/badge/33dd57d6bade41fc9804b834695c0816
    :alt: Codacy code health
    :scale: 100%
    :target: https://www.codacy.com/app/support_3/jasmin

.. |landscape| image:: https://landscape.io/github/jookies/jasmin/master/landscape.svg?style=flat
    :alt: Landscape code health
    :scale: 100%
    :target: https://landscape.io/github/jookies/jasmin/master

.. |ci| image:: https://api.travis-ci.org/jookies/jasmin.png
    :alt: Build status
    :scale: 100%
    :target: https://travis-ci.org/jookies/jasmin

.. |docker| image:: https://images.microbadger.com/badges/image/jookies/jasmin.svg
    :alt: Docker image contents
    :scale: 100%
    :target: https://microbadger.com/#/images/jookies/jasmin

.. |docs| image:: https://readthedocs.org/projects/jasmin/badge/?version=latest
    :alt: Documentation status
    :scale: 100%
    :target: http://docs.jasminsms.com
