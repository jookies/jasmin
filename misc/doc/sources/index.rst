##################################
The Jasmin Open source SMS gateway
##################################

`Jasmin <http://jasminsms.com/>`_ is an open-source SMS Gateway with many enterprise-class features, Jasmin is built to be easily customized to meet the specific needs of messaging exchange growing business.

Based on strong message routing algorithms, Jasmin provides flexibility to define rule based routing based on various criteria: sender ID, source, destination and many combinations. Auto reconnection and re-routing mechanism managing peak hours or link failover for high availability services.

Jasmin is written in Python and Twisted framework for serving highly scalable applications, SMS message delivery can be done through HTTP and SMPP protocols, intelligent routing can be configured in real-time through an API, cli interface or a web backend.

Web backend is written in Django [1]_ and is providing advanced tools for managing and monitoring the gateway.

Features
********
* SMPP Client / Server
* HTTP Client / Server
* Based on AMQP broker for store&forward mechanisms
* Advanced message routing : Simple & static, *Roundrobin*, *Failover*, *Leastcost* ..
* Standard message filtering: *TransparentFilter*, *ConnectorFilter*, *UserFilter* ..
* Advanced message filtering: *EvalPyFilter*
* Flexible billing support
* Web ui for management
* Supports Unicode (UTF-8 / 16) for sending out multilingual SMS
* Supports easy creation and sending of specialized/binary SMS like mono Ringtones, WAP Push, Vcards
* Supports concatenated (multipart) SMS contents (long SMS)

Jasmin is designed for **performance**, **high traffic loads** and **full in-memory execution**.

Getting started
***************
.. hlist::
   :columns: 2

   * :doc:`/installation/index` -- Install and run Jasmin SMS Gateway
   * :ref:`Examples_Receiving_SMS` -- Basic push/pull SMS application via HTTP
   * :doc:`/apis/ja-http/index` -- HTTP API technical specification
   * :doc:`/apis/smpp-server/index` -- SMPP Server API technical specification
   * :ref:`Examples_Routing` -- Running basic SMS and routing scenarios
   * :doc:`/faq/users` -- Frequently asked questions

Full contents
*************
.. toctree::
   :maxdepth: 2

   /architecture/index
   /support/index
   /installation/index
   /apis/ja-http/index
   /apis/smpp-server/index
   /routing/index
   /interception/index
   /programming-examples/index
   /management/jcli/index
   /management/jcli/modules
   /billing/index
   /messaging/index
   /faq/users
   /faq/developers

Links
*****

* `Jasmin SMS Gateway home page <http://www.jasminsms.com>`_
* `Documentation <http://docs.jasminsms.com>`_
* `Source code <http://github.com/jookies/jasmin>`_
* `Travis CI <https://travis-ci.org/jookies/jasmin>`_

License
*******
Jasmin is released under the terms of the [Apache License Version 2]. See **`LICENSE`** file for details.

.. rubric:: Footnotes
.. [1] Web interface will come in v0.7
