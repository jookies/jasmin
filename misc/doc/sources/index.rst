##################################
The Jasmin Open source SMS gateway
##################################
Jasmin is an open-source SMS Gateway with many enterprise-class features, Jasmin is built to be 
easily customized to meet the specific needs of SMS exchange growing business.

Jasmin is written in Python and Django for serving highly customized applications, SMS message delivery 
can be done through HTTP and SMPP protocols, intelligent routing can be configured in real-time through
cli interface or a web backend.

Features
========
* SMPP Client / Server
* HTTP Client / Server
* Based on AMQP broker for store&forward mechanisms
* Advanced message routing/filtering (Simpls, Roundrobin, Failover, Leastcost ...)
* Web ui for management
* Supports Unicode (UTF-8) for sending out multilingual SMS
* Supports easy creation and sending of specialized/binary SMS like mono Ringtones, WAP Push, Vcards
* Supports concatenated SMS strings (long SMS)

Jasmin is designed for **performance**, **high traffic loads** and **full in-memory execution**.

Getting started
===============
.. hlist::
   :columns: 2
   
   * **TODO** -- Install and run Jasmin gateway
   * **TODO** -- Running basic SMS and routing scenarios
   * **TODO** -- Basic push/pull SMS application
   * :doc:`apis/ja-http/index` -- HTTP API technical specification

Full contents
=============
.. toctree::
   :maxdepth: 2

   apis/ja-http/index

License
=======
Jasmin is released under the terms of the [Apache License Version 2]. See **`LICENSE`** file for details.