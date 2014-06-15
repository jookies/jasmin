#############
Receiving DLR
#############

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
     - Always
     - The time and date at which the short message was submitted
   * - **donedate**
     - Date & time format: YYMMDDhhmm
     - 1311022338
     - Always
     - The time and date at which the short message reached it’s final state
   * - **sub**
     - Integer
     - 1
     - Always
     - Number of short messages originally submitted. This is only relevant when the original message was submitted to a distribution list.The value is padded with leading zeros if necessary
   * - **dlvrd**
     - Integer
     - 1
     - Always
     - Number of short messages delivered. This is only relevant where the original message was submitted to a distribution list.The value is padded with leading zeros if necessary
   * - **err**
     - Integer
     - 0
     - Always
     - Where appropriate this may hold a Network specific error code or an SMSC error code for the attempted delivery of the message
   * - **text**
     - Text (20 char. max)
     - Hello foo bar
     - Always
     - The first 20 characters of the short message

.. note:: (*): The optionnal parameters are used only for level 2 and 3 DLRs

.. _DLRThrower_process:

Processing
==========
The flowchart below describes how dlr delivery and retrying policy is done inside DLRThrower service:

.. figure:: resources/dlr-flowchart.png
   :alt: DLR delivery flowchart as processed by DLRThrower service
   :align: Center

.. _configuration_dlr-thrower:

jasmin.cfg / dlr-thrower
========================

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contain a section called **deliversm-thrower** where all DLRThrower service related config elements are:

.. code-block:: ini
   :linenos:
   
   [dlr-thrower]
   timeout            = 30
   retry_delay        = 30
   max_retries        = 3
   log_level          = INFO
   log_file           = /var/log/jasmin/dlr-thrower.log
   log_format         = %(asctime)s %(levelname)-8s %(process)d %(message)s
   log_date_format    = %Y-%m-%d %H:%M:%S

.. list-table:: [http-api] configuration section
   :widths: 10 40
   :header-rows: 1

   * - Element
     - Description
   * - timeout
     - Sets socket timeout in seconds for outgoing client http connections.
   * - retry_delay
     - Define how many seconds should pass within the HTTP queuing for retrying a failed HTTP request.
   * - max_retries
     - Define how many retries should be performed for failing HTTP requests of DLR.
   * - log_*
     - Python's logging module configuration.