########
HTTP API
########

This document is targeted at software designers/programmers wishing to integrate SMS messaging as a function into their applications using HTTP protocol, e.g. in connection with WEB-server, unified messaging, information services etc..

If you need to use a stateful tcp protocol (**SMPP v3.4**), please refer to :doc:`/apis/smpp-server/index`.

SMS Messages can be transmitted using HTTP protocol, the following requirements must be met to enable the service:

 * You need a Jasmin user account
 * You need sufficient credit on your Jasmin user account [1]_

.. note:: The ABCs:

   - **MT** is referred to Mobile Terminated, a SMS-MT is an SMS sent to mobile
   - **MO** is referred to Mobile Originated, a SMS-MO is an SMS sent from mobile

Features
********

The ja-http API allows you to:

* Send and receive SMS through Jasmin's connectors,
* Receive http callbacks for delivery notification (*receipts*) when SMS-MT is received (or not) on mobile station,
* Send and receive long (more than 160 characters) SMS, unicode/binary content and receive http callbacks when a mobile station send you a SMS-MO.
* Check your balance status,
* Check a message rate price before sending it.

.. _sending_sms-mt:

Sending SMS-MT
**************

In order to deliver **SMS-MT** messages, Data is transferred using **HTTP GET/POST** requests.
The Jasmin gateway accepts requests at the following URL:

http://127.0.0.1:1401/send

.. note:: Host ``127.0.0.1`` and port ``1401`` are default values and configurable in ``/etc/jasmin/jasmin.cfg``, see :ref:`configuration_http-api`.

This guide will help understand how the API works and provide :ref:`examples` for sending SMS-MT.

.. _http_request_parameters:

HTTP request parameters
=======================
When calling Jasmin's URL from an application, the below parameters must be passed (at least mandatory ones), the api will return a message id on success, see :ref:`http_response`.

.. list-table:: ja-http sending SMS parameters
   :header-rows: 1

   * - Parameter
     - Value / Pattern
     - Example(s)
     - Presence
     - Description / Notes
   * - **to**
     - Destination address
     - 20203050
     - Mandatory
     - Destination address, only one address is supported per request
   * - **from**
     - Originating address
     - 20203050, Jasmin
     - Optional
     - Originating address, In case rewriting of the sender's address is supported or permitted by the SMS-C used to transmit the message, this number is transmitted as the originating address
   * - **coding**
     - 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13 or 14
     - 1
     - Optional
     - Sets the Data Coding Scheme bits, default is 0, accepts values all allowed values in SMPP protocol [1]_
   * - **username**
     - Text (30 char. max)
     - jasmin_user
     - Mandatory
     - Username for Jasmin user account.
   * - **password**
     - Text (30 char. max)
     - jasmin_pass
     - Mandatory
     - Password for Jasmin user account.
   * - **priority**
     - 0, 1, 2 or 3
     - 2
     - Optional
     - Default is 0 (lowest priority)
   * - **validity-period**
     - Integer
     - 1440
     - Optional
     - Message validity (minutes) to be passed to SMSC, default is value is None (message will take SMSC's default)
   * - **dlr**
     - yes or no
     - yes
     - Optional
     - Default is no (no DLR will be tracked)
   * - **dlr-url**
     - HTTP(s) URL
     - http://host/dlr.php
     - Mandatory *if dlr*
     - If a DLR is requested (dlr = ‘yes’), dlr-url MUST be set, if not, dlr value is reconsidered as ‘no’
   * - **dlr-level**
     - 1, 2 or 3
     - 2
     - Mandatory *if dlr*
     - 1: SMS-C level, 2: Terminal level, 3: Both
   * - **dlr-method**
     - GET or POST
     - GET
     - Mandatory *if dlr*
     - DLR is transmitted through http to a third party application using GET or POST method.
   * - **tags**
     - Text
     - 1,702,9901
     - Optional
     - Will tag the routable to help interceptor or router enable specific business logics.
   * - **content**
     - Text
     - Hello world !
     - Mandatory
     - Content to be sent

.. _http_response:

HTTP response
=============

When the request is validated, a SubmitSM PDU is set up with the provided request parameters and sent to the routed connector through a AMQP queue, a queued message-id is returned:

.. code-block:: text

  Success "07033084-5cfd-4812-90a4-e4d24ffb6e3d"

Otherwise, an error is returned:

.. code-block:: text

  Error "No route found"

.. list-table:: HTTP response code details
   :widths: 10 40 50
   :header-rows: 1

   * - HTTP Code
     - HTTP Body
     - Meaning
   * - **200**
     - Success "07033084-5cfd-4812-90a4-e4d24ffb6e3d"
     - Message is successfully queued, messaged-id is returned
   * - **400**
     - Error "Mandatory arguments not found, please refer to the HTTPAPI specifications."
     - Request parameters validation error
   * - **400**
     - Error "Argument _ is unknown."
     - Request parameters validation error
   * - **400**
     - Error "Argument _ has an invalid value: _."
     - Request parameters validation error
   * - **400**
     - Error "Mandatory argument _ is not found."
     - Request parameters validation error
   * - **400**
     - *dynamic messages*
     - Credentials validation error, c.f. :ref:`user_credentials`
   * - **403**
     - Error "Authentication failure for username:_"
     - Authentication error
   * - **403**
     - Error "Authorization failed for username:_"
     - Credentials validation error, c.f. :ref:`user_credentials`
   * - **403**
     - Error "Cannot charge submit_sm, check RouterPB log file for details"
     - User charging error
   * - **412**
     - Error "No route found"
     - Message routing error
   * - **500**
     - Error "Cannot send submit_sm, check SMPPClientManagerPB log file for details"
     - Fallback error, checking log file will provide better details


.. _examples:

Examples
========

Here is an example of how to send simple GSM 03.38 messages:

.. literalinclude:: example_send_gsm0338.py
   :language: python

Here is an example of how to request acknowledgement when sending a SMS:

.. literalinclude:: example_send_dlr2.py
   :language: python

And more use cases:

.. literalinclude:: example_send_misc.py
   :language: python

In PHP:

.. literalinclude:: example_send_gsm0338.php
   :language: php

In Ruby:

.. literalinclude:: example_send_gsm0338.rb
   :language: ruby

.. _configuration_http-api:

jasmin.cfg / http-api
=====================

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contain a section called **http-api** where all ja-http API related config elements are:

.. code-block:: ini
   :linenos:

   [http-api]
   bind          = 0.0.0.0
   port          = 1401

   long_content_max_parts = 5
   # Splitting long content can be made through SAR options or UDH
   # Possible values are: sar and udh
   long_content_split = udh

   access_log         = /var/log/jasmin/http-access.log
   log_level          = INFO
   log_file           = /var/log/jasmin/http-api.log
   log_format         = %(asctime)s %(levelname)-8s %(process)d %(message)s
   log_date_format    = %Y-%m-%d %H:%M:%S

.. list-table:: [http-api] configuration section
   :widths: 10 10 80
   :header-rows: 1

   * - Element
     - Default
     - Description
   * - bind
     - 0.0.0.0
     - The HTTP API listener will only bind to this specified address, given 0.0.0.0 the listener will bind on all interfaces.
   * - port
     - 1401
     - The binding TCP port.
   * - long_content_max_parts
     - 5
     - If the message to be sent is to be split into several parts. This is the maximum number of individual SMS-MT messages that can be used.
   * - long_content_split
     - udh
     - Splitting method: 'udh': Will split using 6-byte long User Data Header, 'sar': Will split using sar_total_segments, sar_segment_seqnum, and sar_msg_ref_num options.
   * - access_log
     - /var/log/jasmin/http-access.log
     - Where to log all http requests (and errors).
   * - log_*
     -
     - Python's logging module configuration.

.. _receiving_dlr:

Receiving DLR
*************

When requested through dlr-* fields when :ref:`sending_sms-mt`, a delivery receipt (**DLR**) will be sent back to the
application url (set in **dlr-url**) through **HTTP GET/POST** depending on **dlr-method**.

The receiving end point must reply back using a "**200 OK**" status header **and** a body containing an **acknowledgement** of
receiving the DLR, if one or both of these conditions are not met, the *DLRThrower service* will consider reshipment of the same
message if **config/dlr-thrower/max_retries** is not reached (see :ref:`configuration_dlr-thrower`).

In order to acknowledge DLR receipt, the receiving end point must reply back with **exactly** the following html body content::

   ACK/Jasmin

.. note:: It is very important to acknowledge back each received DLR, this will prevent to receive the same message many times, c.f. :ref:`DLRThrower_process` for details
.. note:: Reshipment of a message will be delayed for **config/dlr-thrower/retry_delay** seconds (see :ref:`configuration_dlr-thrower`).

HTTP Parameters for a level 1 DLR
=================================
The following parameters are sent to the receiving end point (at dlr-url) when the DLR's dlr-level is set to 1 (SMS-C level only)

.. list-table:: ja-http parameters for a level 1 DLR
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
     - Internal Jasmin's gateway message id used for tracking messages
   * - **message_status**
     - ESME_* SMPP Command status
     - ESME_ROK, ESME_RINVNUMDESTS
     - Always
     - The delivery status
   * - **level**
     - 1
     - 1
     - Always
     - This is a static value indicating the dlr-level originally requested

HTTP Parameters for a level 2 or 3 DLR
======================================
The following parameters are sent to the receiving end point (at dlr-url) when DLR's dlr-level is set to 2 or 3 (Terminal level or all levels)

.. list-table:: ja-http parameters for a level 2 or 3 DLR
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
     - Internal Jasmin's gateway message id used for tracking messages
   * - **id_smsc**
     - Integer
     - 2567
     - Always
     - Message id returned from the SMS-C
   * - **message_status**
     - ESME_* SMPP Command status
     - ESME_ROK, ESME_RINVNUMDESTS
     - Always
     - The delivery status
   * - **level**
     - 1
     - 1
     - Always
     - This is a static value indicating the dlr-level originally requested
   * - **subdate**
     - Date & time format: YYMMDDhhmm
     - 1311022338
     - Optional
     - The time and date at which the short message was submitted
   * - **donedate**
     - Date & time format: YYMMDDhhmm
     - 1311022338
     - Optional
     - The time and date at which the short message reached it’s final state
   * - **sub**
     - Integer
     - 1
     - Optional
     - Number of short messages originally submitted. This is only relevant when the original message was submitted to a distribution list.The value is padded with leading zeros if necessary
   * - **dlvrd**
     - Integer
     - 1
     - Optional
     - Number of short messages delivered. This is only relevant where the original message was submitted to a distribution list.The value is padded with leading zeros if necessary
   * - **err**
     - Integer
     - 0
     - Optional
     - Where appropriate this may hold a Network specific error code or an SMSC error code for the attempted delivery of the message
   * - **text**
     - Text (20 char. max)
     - Hello foo bar
     - Optional
     - The first 20 characters of the short message

.. _DLRThrower_process:

Processing
==========
The flowchart below describes how dlr delivery and retrying policy is done inside DLRThrower service:

.. figure:: /resources/ja-http/dlr-flowchart.png
   :alt: DLR delivery flowchart as processed by DLRThrower service
   :align: Center

.. _configuration_dlr-thrower:

jasmin.cfg / dlr-thrower
========================

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contain a section called **deliversm-thrower** where all DLRThrower service related config elements are:

.. code-block:: ini
   :linenos:

   [dlr-thrower]
   http_timeout       = 30
   retry_delay        = 30
   max_retries        = 3
   log_level          = INFO
   log_file           = /var/log/jasmin/dlr-thrower.log
   log_format         = %(asctime)s %(levelname)-8s %(process)d %(message)s
   log_date_format    = %Y-%m-%d %H:%M:%S

.. list-table:: [http-api] configuration section
   :widths: 10 10 80
   :header-rows: 1

   * - Element
     - Default
     - Description
   * - http_timeout
     - 30
     - Sets socket timeout in seconds for outgoing client http connections.
   * - retry_delay
     - 30
     - Define how many seconds should pass within the queuing system for retrying a failed throw.
   * - max_retries
     - 3
     - Define how many retries should be performed for failing throws of DLR.
   * - log_*
     -
     - Python's logging module configuration.

.. _receiving_sms-mo:

Receiving SMS-MO
****************

**SMS-MO** incoming messages (**M**\obile **O**\riginated) are forwarded by Jasmin to defined URLs using simple **HTTP GET/POST**, the forwarding is
made by *deliverSmHttpThrower* service, and the URL of the receiving endpoint is selected through a route checking process (c.f. :doc:`/routing/index`).

Receiving endpoint is a third party application which acts on the messages received and potentially generates replies, (:ref:`httpccm_manager` for more details about
HTTP Client connector management).

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
     - Originating address
     - +21620203060, 20203060, Jasmin
     - Always
     - Originating address
   * - **to**
     - Destination address
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
     - Default is 0, accepts values all allowed values in SMPP protocol [2]
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
   * - **binary**
     - Hexlified binary content
     - 062A063062A
     - Always
     - Content of the message in binary `hexlified <https://docs.python.org/2/library/binascii.html#binascii.hexlify>`_ form

.. note:: When receiving multiple parts of a long SMS-MO, *deliverSmHttpThrower service* will concatenate the content of all the parts and then throw one http call with
          concatenated *content*.

.. _deliverSmHttpThrower_process:

Processing
==========
The flowchart below describes how message delivery and retrying policy are done inside *deliverSmHttpThrower* service:

.. figure:: /resources/ja-http/sms-mo-flowchart.png
   :alt: MO delivery flowchart as processed by deliverSmHttpThrower service
   :align: Center

.. _configuration_deliversm-thrower:

jasmin.cfg / deliversm-thrower
==============================

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contain a section called **deliversm-thrower** where all deliverSmHttpThrower service related config elements are:

.. code-block:: ini
   :linenos:

   [deliversm-thrower]
   http_timeout       = 30
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
   * - http_timeout
     - 30
     - Sets socket timeout in seconds for outgoing client http connections.
   * - retry_delay
     - 30
     - Define how many seconds should pass within the queuing system for retrying a failed throw.
   * - max_retries
     - 3
     - Define how many retries should be performed for failing throws of SMS-MO.
   * - log_*
     -
     - Python's logging module configuration.

.. _check_balance:

Checking account balance
************************

In order to check user account balance and quotas, user may request a **HTTP GET/POST** from the following URL:

http://127.0.0.1:1401/balance

.. note:: Host ``127.0.0.1`` and port ``1401`` are default values and configurable in ``/etc/jasmin/jasmin.cfg``, see :ref:`configuration_http-api`.

.. _http_balance_request_parameters:

HTTP request parameters
=======================

.. list-table:: ja-http balance request parameters
   :header-rows: 1

   * - Parameter
     - Value / Pattern
     - Example(s)
     - Presence
     - Description / Notes
   * - **username**
     - Text (30 char. max)
     - jasmin_user
     - Mandatory
     - Username for Jasmin user account.
   * - **password**
     - Text (30 char. max)
     - jasmin_pass
     - Mandatory
     - Password for Jasmin user account.

.. _http_balance_response:

HTTP response
=============

Successful response:

.. code-block:: javascript

  {"balance": 100.0, "sms_count": "ND"}

Otherwise, an error is returned.

.. _balance_request_examples:

Examples
========

Here is an example of how to check balance:

.. literalinclude:: example_balance.py
   :language: python

.. _check_rate:

Checking rate price
*******************

It is possible to ask Jasmin's HTTPAPI for a message rate price before sending it, the request will lookup the route to be considered for the message and will provide the rate price if defined.

Request is done through **HTTP GET/POST** to the following URL:

http://127.0.0.1:1401/rate

.. note:: Host ``127.0.0.1`` and port ``1401`` are default values and configurable in ``/etc/jasmin/jasmin.cfg``, see :ref:`configuration_http-api`.

.. _http_rate_request_parameters:

HTTP request parameters
=======================

.. list-table:: ja-http rate request parameters
   :header-rows: 1

   * - Parameter
     - Value / Pattern
     - Example(s)
     - Presence
     - Description / Notes
   * - **to**
     - Destination address
     - 20203050
     - Mandatory
     - Destination address, only one address is supported per request
   * - **from**
     - Originating address
     - 20203050, Jasmin
     - Optional
     - Originating address, In case rewriting of the sender's address is supported or permitted by the SMS-C used to transmit the message, this number is transmitted as the originating address
   * - **coding**
     - 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13 or 14
     - 1
     - Optional
     - Sets the Data Coding Scheme bits, default is 0, accepts values all allowed values in SMPP protocol [1]_
   * - **username**
     - Text (30 char. max)
     - jasmin_user
     - Mandatory
     - Username for Jasmin user account.
   * - **password**
     - Text (30 char. max)
     - jasmin_pass
     - Mandatory
     - Password for Jasmin user account.
   * - **content**
     - Text
     - Hello world !
     - Optional
     - Content to be sent

.. _http_rate_response:

HTTP response
=============

Successful response:

.. code-block:: javascript

  {"submit_sm_count": 2, "unit_rate": 2.8}

Where **submit_sm_count** is the number of message units if the **content** is longer than 160 characters, **content** parameter is optional for requesting rate price.

Otherwise, an error is returned.

Otherwise, an error is returned:

.. code-block:: text

  Error "No route found"

.. _rate_request_examples:

Examples
========

Here is an example of how to check rate price:

.. literalinclude:: example_rate.py
   :language: python

.. rubric:: Footnotes
.. [1] :doc:`/billing/index`

.. [2]
.. include:: ../datacoding.table
