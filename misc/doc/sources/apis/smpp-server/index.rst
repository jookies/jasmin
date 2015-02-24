###############
SMPP Server API
###############

This document is targeted at software designers/programmers wishing to integrate SMS messaging through a stateful tcp protocol **SMPP v3.4**, if you feel this does not fit your needs and that you are more "web-service-guy" then you still can try :doc:`/apis/ja-http/index`.

SMS Messages can be transmitted using SMPP protocol, the following requirements must be met to enable the service :

 * You need a Jasmin user account
 * You need sufficient credit on your Jasmin user account [1]_

.. note:: The ABCs:

   - **MT** is referred to Mobile Terminated, a SMS-MT is an SMS sent to mobile
   - **MO** is referred to Mobile Originated, a SMS-MO is an SMS sent from mobile

Features
********
The SMPP Server API allows you to send and receive SMS and delivery receipts (DLR) through Jasmin's connectors, send and receive long (more than 160 characters) SMS and unicode/binary content.

.. _configuration_smpps:

jasmin.cfg / smpp-server
========================

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contain a section called **smpp-server** where all SMPP Server API related config elements are:

.. code-block:: ini
   :linenos:
   
   [smpp-server]
   id            = "smpps_01"
   bind          = 0.0.0.0
   port          = 2775
   
   sessionInitTimerSecs = 30
   enquireLinkTimerSecs = 30
   inactivityTimerSecs  = 300
   responseTimerSecs    = 60
   pduReadTimerSecs     = 30
   
   log_level          = INFO
   log_file           = /var/log/jasmin/default-smpps_01.log
   log_format         = %(asctime)s %(levelname)-8s %(process)d %(message)s
   log_date_format    = %Y-%m-%d %H:%M:%S

.. list-table:: [smpp-server] configuration section
   :widths: 10 10 80
   :header-rows: 1

   * - Element
     - Default
     - Description
   * - id
     - smpps_01
     - The SMPP Server id, used to identify the instance in case you use multiple servers per Jasmin process.
   * - bind
     - 0.0.0.0
     - The SMPP Server API listener will only bind to this specified address, given 0.0.0.0 the listener will bind on all interfaces.
   * - port
     - 2775
     - The binding TCP port.
   * - sessionInitTimerSecs
     - 30
     - Protocol tuning parameter: timeout for a bind request.
   * - enquireLinkTimerSecs
     - 30
     - Protocol tuning parameter: timeout for an enquire_link request.
   * - inactivityTimerSecs
     - 300
     - Protocol tuning parameter: inactivity timeout.
   * - responseTimerSecs
     - 60
     - Protocol tuning parameter: global request timeout.
   * - pduReadTimerSecs
     - 30
     - Protocol tuning parameter: binary pdu ready timeout.
   * - log_*
     - 
     - Python's logging module configuration.

.. _smpps_binding:

Binding to SMPP Server
======================

Using a proper SMPP Client application (or a Jasmin SMPP Client), the following parameters must be considered:

.. list-table:: SMPP Server binding parameters
   :header-rows: 1

   * - Parameter
     - Value / Pattern
     - Example(s)
     - Presence
     - Description / Notes
   * - **system_id**
     - Text (30 char. max)
     - jasmin_user
     - Mandatory
     - Username for Jasmin user account.
   * - **password**
     - Text (30 char. max)
     - jasmin_pass
     - Mandatory
     - Password for Jasmin user account.

.. _smpps_pdus:

Supported SMPP PDUs
===================

Jamsin's SMPP Server is supporting the following PDUs:

* bind_transmitter
* bind_transceiver
* bind_receiver
* unbind
* submit_sm
* deliver_sm
* enquire_link

.. rubric:: Footnotes
.. [1] :doc:`/billing/index`