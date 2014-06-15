################
Receiving SMS-MO
################

**SMS-MO** incoming messages (**M**\obile **O**\riginated) are forwarded by Jasmin to defined URLs using simple **HTTP GET/POST**, the forwarding is 
made by *deliverSmHttpThrower* service, and the URL of the receiving endpoint is selected through a route checking process.

Receiving endpoint is a third party application which acts on the messages received and potentially generates replies.

The parameters below are transmitted for each SMS-MO, the receiving end point must provide an url (set in **jasminApi.HttpConnector.baseurl**) and parse the
below parameters using GET or POST method (depends on **jasminApi.HttpConnector.method**).

The receiving end point must reply back using a "**200 OK**" status header **and** a body containing an **acknowledgement** of receiving the SMS-MO, if one or both of
these conditions are not met, the *deliverSmHttpThrower service* will consider reshipment of the same message if **config/deliversm-thrower/max_retries** is not reached, 
(see :ref:`configuration_deliversm-thrower`).

In order to acknowledge SMS-MO receipt, the receiving end point must reply back with **exactly** the following html body content::

   ACK/Jasmin

.. note:: It is very important to acknowledge back each received SMS-MO, this will prevent to receive the same message many times, c.f. :ref:`deliverSmHttpThrower_process` for details
.. note:: Reshipment of a message will be delayed for **config/deliversm-thrower/retry_delay** seconds (see :ref:`configuration_deliversm-thrower`).

HTTP Parameters
===============
When receiving an URL call from Jasmin's *deliverSmHttpThrower service*, the below parameters are delivered (at least *Always* present ones).

.. list-table:: ja-http receiving SMS parameters
   :header-rows: 1

   * - Parameter
     - Value / Pattern
     - Example(s)
     - Presence
     - Description / Notes
   * - **id**
     - Universally Unique IDentifier (UUID)
     - 16fd2706-8baf-433b-82eb-8c7fada847da
     - Always
     - Internal Jasmin's gateway message id
   * - **from**
     - Msisdn with or without international prefix, alphanumeric value
     - +21620203060, 20203060, Jasmin
     - Always
     - Originating address
   * - **to**
     - Msisdn with or without international prefix, alphanumeric value
     - +21620203060, 20203060, Jasmin
     - Always
     - Destination address, only one address is supported per request
   * - **origin-connector**
     - Alphanumeric id
     - 23, bcd, MTN, clickatell, beepsend
     - Always
     - Jasmin http connector id
   * - **priority**
     - 1, 2 or 3
     - 2
     - Optional
     - Default is 1 (lowest priority)
   * - **coding**
     - Numeric
     - 8
     - Optional
     - Default is 0, accepts values all allowed values in SMPP protocol [1]
   * - **validity**
     - YYYY-MM-DD hh:mm:ss
     - 2013-07-16 00-46:54
     - Optional
     - The validity period parameter indicates the Jasmin GW expiration time, after which the message should be discarded if not delivered to the destination
   * - **content**
     - Text
     - Hello world !
     - Always
     - Content of the message

.. note:: When receiving multiple parts of a long SMS-MO, *deliverSmHttpThrower service* will concatenate the content of all the parts and then throw one http call with 
          concatenated *content*. 

.. _deliverSmHttpThrower_process:

Processing
==========
The flowchart below describes how message delivery and retrying policy are done inside *deliverSmHttpThrower* service:

.. figure:: resources/sms-mo-flowchart.png
   :alt: MO delivery flowchart as processed by deliverSmHttpThrower service
   :align: Center
   
.. _configuration_deliversm-thrower:

jasmin.cfg / deliversm-thrower
==============================

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contain a section called **deliversm-thrower** where all deliverSmHttpThrower service related config elements are:

.. code-block:: ini
   :linenos:
   
   [deliversm-thrower]
   timeout            = 30
   retry_delay        = 30
   max_retries        = 3
   log_level          = INFO
   log_file           = /var/log/jasmin/deliversm-thrower.log
   log_format         = %(asctime)s %(levelname)-8s %(process)d %(message)s
   log_date_format    = %Y-%m-%d %H:%M:%S

.. list-table:: [http-api] configuration section
   :widths: 10 10 80
   :header-rows: 1

   * - Element
     - Default
     - Description
   * - timeout
     - 30
     - Sets socket timeout in seconds for outgoing client http connections.
   * - retry_delay
     - 30
     - Define how many seconds should pass within the HTTP queuing for retrying a failed HTTP request.
   * - max_retries
     - 3
     - Define how many retries should be performed for failing HTTP requests of SMS-MO.
   * - log_*
     - 
     - Python's logging module configuration.
     
.. rubric:: Footnotes
.. [1]
.. include:: ../datacoding.table