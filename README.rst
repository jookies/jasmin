Jasmin - Open source SMS gateway
================================

|python_ver| |current_version| |ci| |docs| |downloads| |status|

Introduction
------------
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
------------

.. figure:: https://github.com/jookies/jasmin/raw/master/misc/doc/sources/resources/architecture/hld.png
   :alt: HLD Architecture
   :align: Center

Jasmin core and its external connectors (used for AMQP, Redis, SMPP, HTTP, Telnet ...) are written in Python 
and are mainly based on `Twisted matrix <https://twistedmatrix.com/>`_, a event-driven networking engine.

Getting started
---------------

Refer to `Installation steps <http://docs.jasminsms.com/en/latest/installation/index.html>`_ to get Jasmin gateway up and running quickly with a simple SMS sending scenario.

Documentation
-------------

The documentation is at: http://docs.jasminsms.com

If you still need to build it locally::

  $ cd misc/doc
  $ make html
  ...
  ...
  Build finished. The HTML pages are in build/html.

License
-------
Jasmin is released under the terms of the [Apache License Version 2]. See **`LICENSE`** file for details.

.. |python_ver| image:: https://pypip.in/py_versions/jasmin/badge.svg
    :alt: Python version
    :scale: 100%
    :target: https://pypi.python.org/pypi/jasmin

.. |current_version| image:: https://pypip.in/v/jasmin/badge.png?text=version
    :alt: Current version
    :scale: 100%
    :target: https://pypi.python.org/pypi/jasmin

.. |ci| image:: https://api.travis-ci.org/jookies/jasmin.png
    :alt: Build status
    :scale: 100%
    :target: https://travis-ci.org/jookies/jasmin

.. |docs| image:: https://readthedocs.org/projects/jasmin/badge/?version=latest
    :alt: Documentation status
    :scale: 100%
    :target: http://docs.jasminsms.com

.. |downloads| image:: https://pypip.in/download/jasmin/badge.svg
    :alt: Downloads through pypi
    :scale: 100%
    :target: https://pypi.python.org/pypi/jasmin

.. |status| image:: https://pypip.in/status/jasmin/badge.svg
    :alt: Development status
    :scale: 100%
    :target: https://pypi.python.org/pypi/jasmin