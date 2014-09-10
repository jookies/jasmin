Jasmin - Open source SMS gateway
================================

|jasmin-ci|_

.. |jasmin-ci| image:: https://travis-ci.org/fourat/jasmin.png
.. _jasmin-ci: https://travis-ci.org/fourat/jasmin

Introduction
------------
Jasmin is a very complete open source SMS Gateway with many enterprise-class features such as:

* SMPP Client / Server
* HTTP Client / Server
* Console-based configuration, no service restart required
* Based on AMQP broker for store&forward mechanisms and other queuing systems
* Using Redis for in-memory DLR tracking
* Advanced message routing/filtering (Simple, Roundrobin, Failover, Leastcost ...)
* Web ui for management
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

Installation
------------

System requirements:

* **Python**: Minimum version is 2.7
* header files and a static library for Python, Ubuntu package name: **python-dev**
* Foreign Function Interface library (development files), Ubuntu package name: **libffi-dev**
* Secure Sockets Layer toolkit - development files, Ubuntu package name: **libssl-dev**
* **pip**: Python package installer, needed if you are going to install *jasmin* from `the Python Package Index <https://pypi.python.org/pypi>`_

Once requirements are met, you can install:

Via pip::

    pip install jasmin

Or locally via downloaded package::

    python setup.py install

Documentation
-------------

Build it locally::

  $ cd misc/doc
  $ make html
  ...
  ...
  Build finished. The HTML pages are in build/html.

License
-------
Jasmin is released under the terms of the [Apache License Version 2]. See **`LICENSE`** file for details.