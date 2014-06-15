.. _sending_sms-mt:

##############
Sending SMS-MT
##############

In order to deliver **SMS-MT** messages, Data is transferred using **HTTP GET/POST** requests.
The Jasmin gateway accepts requests at the following URL:

http://127.0.0.1:1401/send

.. note:: Host ``127.0.0.1`` and port ``1401`` are default values and configurable in ``/etc/jasmin/jasmin.cfg``, see :ref:`configuration_http-api`

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
     - Msisdn with or without international prefix
     - 20203050
     - Mandatory
     - Destination address, only one address is supported per request
   * - **from**
     - Msisdn with or without international prefix, alphanumeric value
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
     - 1, 2 or 3
     - 2
     - Optional
     - Default is 1 (lowest priority)
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
   * - **403**
     - Error "Authentication failure for username:_"
     - Authentication error
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
   long_content_split = sar
   
   access_log         = /var/log/jasmin/http-access.log
   log_level          = INFO
   log_file           = /var/log/jasmin/http-api.log
   log_format         = %(asctime)s %(levelname)-8s %(process)d %(message)s
   log_date_format    = %Y-%m-%d %H:%M:%S

.. list-table:: [http-api] configuration section
   :widths: 10 40
   :header-rows: 1

   * - Element
     - Description
   * - bind
     - The HTTP API listener will only bind to this specified address, given 0.0.0.0 the listener will bind on all interfaces.
   * - port
     - The binding TCP port.
   * - long_content_max_parts
     - If the message to be sent is to be split into several parts. This is the maximum number of individual SMS-MT messages that can be used.
   * - long_content_split
     - Splitting method: 'udh': Will split using 6-byte long User Data Header, 'sar': Will split using sar_total_segments, sar_segment_seqnum, and sar_msg_ref_num options.
   * - access_log
     - Where to log all http requests (and errors).
   * - log_*
     - Python's logging module configuration.

.. rubric:: Footnotes
.. [1]
.. include:: ../datacoding.table