.. image:: https://raw.githubusercontent.com/jookies/jasmin/master/misc/doc/sources/_static/jasmin-logo-small.png

Jasmin - Open source SMS gateway
################################

|contribs| |ubuntu-package| |redhat-package| |ci| |docker| |docs| |support|

Introduction
************
Jasmin is a very complete open source SMS Gateway with many enterprise-class features such as:

* SMPP Client / Server
* HTTP Client / Server
* Console-based configuration, no service restart required
* Based on AMQP broker for store&forward mechanisms and other queuing systems
* Using Redis for in-memory DLR tracking and billing
* Advanced message routing/filtering (Simple, Roundrobin, Failover, HLR lookup, Leastcost ...)
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
   :target: https://docs.jasminsms.com/en/latest/architecture/index.html

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
* `Continuous Integration <https://github.com/jookies/jasmin/actions>`_
* `Source code <https://github.com/jookies/jasmin>`_
* `Kubernetes deployment <https://docs.jasminsms.com/en/latest/installation/index.html#kubernetes-cluster>`_ or `/Experimental/ Deploy to Heroku <https://heroku.com/deploy?template=https://github.com/jookies/jasmin>`_

Contributors
************

.. figure:: https://opencollective.com/jookies/contributors.svg?width=840&button=false
   :alt: Contributors
   :align: Center
   :figwidth: 100%
   :target: https://github.com/jookies/jasmin/graphs/contributors

License
*******
Jasmin is released under the terms of the [Apache License Version 2]. See **`LICENSE`** file for details.

Credits
*******

* Supported by `DigitalOcean <https://www.digitalocean.com/>`_
* Dominic Amato for the extraordinary effort porting Jasmin to Python3: https://github.com/DomAmato
* SMPP PDU parsing based on smpp.pdu: https://github.com/mozes/smpp.pdu
* SMPP 3.4 Client based on smpp.twisted: https://github.com/mozes/smpp.twisted
* GSMP 3.38 based on Python messaging: https://github.com/pmarti/python-messaging

.. |contribs| image:: https://img.shields.io/badge/contributions-welcome-brightgreen?logo=github
    :alt: Contributions welcome
    :scale: 100%
    :target: https://github.com/jookies/jasmin/blob/master/CODE_OF_CONDUCT.md

.. |ci| image:: https://github.com/jookies/jasmin/actions/workflows/ci.yml/badge.svg
    :alt: Build status
    :scale: 100%
    :target: https://github.com/jookies/jasmin/actions

.. |ubuntu-package| image:: https://img.shields.io/badge/package-ubuntu-brightgreen.svg?style=flat-square
    :alt: Ubuntu package
    :scale: 100%
    :target: https://packagecloud.io/jookies/jasmin-sms-gateway

.. |redhat-package| image:: https://img.shields.io/badge/package-redhat-brightgreen.svg?style=flat-square
    :alt: Redhat package
    :scale: 100%
    :target: https://packagecloud.io/jookies/jasmin-sms-gateway

.. |docker| image:: https://github.com/jookies/jasmin/actions/workflows/docker.yml/badge.svg
    :alt: Docker image build status
    :scale: 100%
    :target: https://github.com/jookies/jasmin/actions

.. |docs| image:: https://readthedocs.org/projects/jasmin/badge/?version=latest
    :alt: Documentation status
    :scale: 100%
    :target: http://docs.jasminsms.com

.. |support| image:: https://img.shields.io/badge/discussions-GitHub-333333?logo=github
    :alt: Github discussions
    :scale: 100%
    :target: https://github.com/jookies/jasmin/discussions
