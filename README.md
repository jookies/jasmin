[![Build Status](https://travis-ci.org/fourat/jasmin.png)](https://travis-ci.org/fourat/jasmin)
Jasmin - Open source SMS gateway
================================
Work in progress, no stable releases yet


Introduction
------------
Jasmin is a very complete open source SMS Gateway with many enterprise-class features such as::
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

Documentation
-------------
```
cd misc/doc
make html
...
...
Build finished. The HTML pages are in build/html.
```

License
-------
Jasmin is released under the terms of the [Apache License Version 2]. See **`LICENSE`** file for details.