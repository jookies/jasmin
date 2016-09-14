############
Interception
############

Starting from 0.7.0, Jasmin provides a convenient way for users to hook third party logics on **intercepted**
messages (submit_sm or deliver_sm) before proceding to :doc:`/routing/index`.

Interception of message is based on filter matching, just like the router; every intercepted message will be
handed to a user-script written in Python_.

This feature permits users to implement custom behaviors on top of Jasmin router, here's some possible
scenarios:

* Billing & charging of MO messages,
* Implement HLR lookup for a better SMS MT routing,
* Change a pdu content: fix npi/ton, prefixing/suffixing numbers, etc ...
* Modify Jasmin's response for the message: send back a *ESME_RINVDSTADR* instead of *ESME_ROK* for example.
* etc ..

.. _Python: http://www.python.org

Enabling interceptor
********************

Jasmin's interceptor is a system service that run separately from Jasmin, it can be hosted on remote server as
well; **interceptord** is a system service just like **jasmind**, so simply start it by typing::

  sudo systemctl start interceptord

.. note:: After starting the **interceptord** service, you may check */var/log/jasmin/interceptor.log* to
  ensure everything is okay.

Then you need to enable communication between **jasmind** and **interceptord** services by editing **jasmind**
start script (locate the **jasmind.service** file in */etc/systemd*) and replacing the following line::

  ExecStart=/usr/bin/jasmind.py --username jcliadmin --password jclipwd

by::

  ExecStart=/usr/bin/jasmind.py --username jcliadmin --password jclipwd --enable-interceptor-client

The last step is to restart **jasmind** and check */var/log/jasmin/interceptor.log* to ensure connection has
been successfully established by finding the following line::

  INFO     XXXX Authenticated Avatar: iadmin

Intercepting a message
**********************

As stated earlier, interceptor is behaving similarly to :doc:`/routing/index`, here's an example of setting up
a MO message (deliver_sm) interception rule through :doc:`jcli management console </management/jcli/index>`::

  jcli : mointerceptor -a
  Adding a new MO Interceptor: (ok: save, ko: exit)
  > type DefaultInterceptor
  <class 'jasmin.routing.Interceptors.DefaultInterceptor'> arguments:
  script
  > script python2(/opt/jasmin-scripts/interception/mo-interceptor.py)
  > ok
  Successfully added MOInterceptor [DefaultInterceptor] with order:0

Same thing apply to setting up a MT message (submit_sm) interception rule, here's another example using a
filtered rule instead of a default one::

  jcli : mtinterceptor -a
  Adding a new MT Interceptor: (ok: save, ko: exit)
  > type StaticMTInterceptor
  <class 'jasmin.routing.Interceptors.DefaultInterceptor'> arguments:
  filters, script
  > script python2(/opt/jasmin-scripts/interception/mt-interceptor.py)
  > filters U-foo;DA-33
  > order 100
  > ok
  Successfully added MTInterceptor [StaticMTInterceptor] with order:100

As show in the above examples, the interception rules are straightforward, any matched message will be handed to
the script you set through the **script python2(<path_to_pyfile>)** instruction.

When your python script is called it will get the following global variables set:

* **routable**: one of the *jasmin.routing.Routables.Routable* inheriters (:ref:`Route_Routable` for more details)
* **smpp_status**: (default to *0*) it is the smpp response that Jasmin must return for the message, more details
  in :ref:`controlling_response`
* **http_status**: (default to *0*) it is the http response that Jasmin must return for the message, more details
  in :ref:`controlling_response`

The script can:

* Override **routable** parameters like setting destination or source addresses, short message, etc ...
* Tag the **routable** to help the router matching a desired rule (useful for HRL lookup routing)
* Control Jasmin response by setting **smpp_status** and/or **http_status**.

Some practical examples are given :ref:`below <scripting_examples>`.

.. _controlling_response:

Controlling response
********************

The interceptor script can reject message before it goes to the router, this can be useful for implementing third
party controls like:

* Billing and charging authorization: reject message if user has no credits,
* Reject some illegal message content,
* Enable anti-spam to protect destination users from getting flooded,
* etc ...

In order to reject a message, depending on the source of message (httpapi ? smpp server ? smpp client ?) the
script must set **smpp_status** and/or **http_status** accordingly to the error to be returned back, here's an
error mapping table for smpp:

.. list-table:: **smpp_status** Error mapping
   :widths: 10 10 80
   :header-rows: 1

   * - Value
     - SMPP Status
     - Description
   * - 0
     - ESME_ROK
     - No error
   * - 1
     - ESME_RINVMSGLEN
     - Message Length is invalid
   * - 2
     - ESME_RINVCMDLEN
     - Command Length is invalid
   * - 3
     - ESME_RINVCMDID
     - Invalid Command ID
   * - 4
     - ESME_RINVBNDSTS
     - Invalid BIND Status for given command
   * - 5
     - ESME_RALYBND
     - ESME Already in Bound State
   * - 6
     - ESME_RINVPRTFLG
     - Invalid Priority Flag
   * - 7
     - ESME_RINVREGDLVFLG
     - Invalid Registered Delivery Flag
   * - 8
     - ESME_RSYSERR
     - System Error
   * - 265
     - ESME_RINVBCASTAREAFMT
     - Broadcast Area Format is invalid
   * - 10
     - ESME_RINVSRCADR
     - Invalid Source Address
   * - 11
     - ESME_RINVDSTADR
     - Invalid Dest Addr
   * - 12
     - ESME_RINVMSGID
     - Message ID is invalid
   * - 13
     - ESME_RBINDFAIL
     - Bind Failed
   * - 14
     - ESME_RINVPASWD
     - Invalid Password
   * - 15
     - ESME_RINVSYSID
     - Invalid System ID
   * - 272
     - ESME_RINVBCAST_REP
     - Number of Repeated Broadcasts is invalid
   * - 17
     - ESME_RCANCELFAIL
     - Cancel SM Failed
   * - 274
     - ESME_RINVBCASTCHANIND
     - Broadcast Channel Indicator is invalid
   * - 19
     - ESME_RREPLACEFAIL
     - Replace SM Failed
   * - 20
     - ESME_RMSGQFUL
     - Message Queue Full
   * - 21
     - ESME_RINVSERTYP
     - Invalid Service Type
   * - 196
     - ESME_RINVOPTPARAMVAL
     - Invalid Optional Parameter Value
   * - 260
     - ESME_RINVDCS
     - Invalid Data Coding Scheme
   * - 261
     - ESME_RINVSRCADDRSUBUNIT
     - Source Address Sub unit is Invalid
   * - 262
     - ESME_RINVDSTADDRSUBUNIT
     - Destination Address Sub unit is Invalid
   * - 263
     - ESME_RINVBCASTFREQINT
     - Broadcast Frequency Interval is invalid
   * - 257
     - ESME_RPROHIBITED
     - ESME Prohibited from using specified operation
   * - 273
     - ESME_RINVBCASTSRVGRP
     - Broadcast Service Group is invalid
   * - 264
     - ESME_RINVBCASTALIAS_NAME
     - Broadcast Alias Name is invalid
   * - 270
     - ESME_RBCASTQUERYFAIL
     - query_broadcast_sm operation failed
   * - 51
     - ESME_RINVNUMDESTS
     - Invalid number of destinations
   * - 52
     - ESME_RINVDLNAME
     - Invalid Distribution List Name
   * - 267
     - ESME_RINVBCASTCNTTYPE
     - Broadcast Content Type is invalid
   * - 266
     - ESME_RINVNUMBCAST_AREAS
     - Number of Broadcast Areas is invalid
   * - 192
     - ESME_RINVOPTPARSTREAM
     - Error in the optional part of the PDU Body
   * - 64
     - ESME_RINVDESTFLAG
     - Destination flag is invalid (submit_multi)
   * - 193
     - ESME_ROPTPARNOTALLWD
     - Optional Parameter not allowed
   * - 66
     - ESME_RINVSUBREP
     - Invalid submit with replace request (i.e.  submit_sm with replace_if_present_flag set)
   * - 67
     - ESME_RINVESMCLASS
     - Invalid esm_class field data
   * - 68
     - ESME_RCNTSUBDL
     - Cannot Submit to Distribution List
   * - 69
     - ESME_RSUBMITFAIL
     - submit_sm or submit_multi failed
   * - 256
     - ESME_RSERTYPUNAUTH
     - ESME Not authorised to use specified service_type
   * - 72
     - ESME_RINVSRCTON
     - Invalid Source address TON
   * - 73
     - ESME_RINVSRCNPI
     - Invalid Source address NPI
   * - 258
     - ESME_RSERTYPUNAVAIL
     - Specified service_type is unavailable
   * - 269
     - ESME_RBCASTFAIL
     - broadcast_sm operation failed
   * - 80
     - ESME_RINVDSTTON
     - Invalid Destination address TON
   * - 81
     - ESME_RINVDSTNPI
     - Invalid Destination address NPI
   * - 83
     - ESME_RINVSYSTYP
     - Invalid system_type field
   * - 84
     - ESME_RINVREPFLAG
     - Invalid replace_if_present flag
   * - 85
     - ESME_RINVNUMMSGS
     - Invalid number of messages
   * - 88
     - ESME_RTHROTTLED
     - Throttling error (ESME has exceeded allowed message limits
   * - 271
     - ESME_RBCASTCANCELFAIL
     - cancel_broadcast_sm operation failed
   * - 97
     - ESME_RINVSCHED
     - Invalid Scheduled Delivery Time
   * - 98
     - ESME_RINVEXPIRY
     - Invalid message validity period (Expiry time)
   * - 99
     - ESME_RINVDFTMSGID
     - Predefined Message Invalid or Not Found
   * - 100
     - ESME_RX_T_APPN
     - ESME Receiver Temporary App Error Code
   * - 101
     - ESME_RX_P_APPN
     - ESME Receiver Permanent App Error Code
   * - 102
     - ESME_RX_R_APPN
     - ESME Receiver Reject Message Error Code
   * - 103
     - ESME_RQUERYFAIL
     - query_sm request failed
   * - 259
     - ESME_RSERTYPDENIED
     - Specified service_type is denied
   * - 194
     - ESME_RINVPARLEN
     - Invalid Parameter Length
   * - 268
     - ESME_RINVBCASTMSGCLASS
     - Broadcast Message Class is invalid
   * - 255
     - ESME_RUNKNOWNERR
     - Unknown Error
   * - 254
     - ESME_RDELIVERYFAILURE
     - Delivery Failure (used for data_sm_resp)
   * - 195
     - ESME_RMISSINGOPTPARAM
     - Expected Optional Parameter missing

As for http errors, the value you set in **http_status** will be the http error code to return.

.. note:: When setting **http_status** to some value different from 0, the **smpp_status** value will be automatically
   set to **255** (ESME_RUNKNOWNERR).
.. note:: When setting **smpp_status** to some value different from 0, the **http_status** value will be automatically
   set to **520** (Unknown error).

Checkout the :ref:`sc_charging_mo` example to see how's rejection is done.

.. _scripting_examples:

Scripting examples
******************

You'll find below some helping examples of scripts used to intercept MO and/or MT messages.

.. _sc_hlr_lookup:

HLR Lookup routing
==================

The following script will help the router decide where to send the MT message, let's say we have some HLR lookup
webservice to call in order to know to which network the destination number belong, and then tag the routable
for later filtering in router:

.. literalinclude:: sc_hlr_lookup.py
   :language: python

The script is tagging the routable if destination is Vodaphone, Orange or Lyca mobile; that's because we need
to route message to different connector based on destination network, let's say:

* *Vodaphone* needs to be routed through **connectorA**
* *Orange* needs to be routed through **connectorB**
* *Lyca mobile* needs to be routed through **connectorC**
* All the rest needs to be routed through **connectorD**

Here's the routing table to execute the above example::

  jcli : mtrouter -l
  #Order Type            Rate    Connector ID(s)     Filter(s)
  #102   StaticMTRoute   0 (!)   smppc(connectorA)   <TG (tag=21401)>
  #101   StaticMTRoute   0 (!)   smppc(connectorB)   <TG (tag=21403)>
  #100   StaticMTRoute   0 (!)   smppc(connectorC)   <TG (tag=21425)>
  #0     DefaultRoute    0 (!)   smppc(connectorD)
  Total MT Routes: 4

.. _sc_charging_mo:

MO Charging
===========

In this case, the script is calling CGRateS_ charging system to check if user has sufficient balance to send
sms, based on the following script, Jasmin will return a **ESME_ROK** if user balance, or **ESME_RDELIVERYFAILURE**
if not:

.. literalinclude:: sc_mo_charging.py
   :language: python

.. _CGRateS: http://www.cgrates.org/

Overriding source address
=========================

There's some cases where you need to override sender-id due to some MNO policies, in the following example all
intercepted messages will have their sender-id set to **123456789**:

.. literalinclude:: sc_override_senderid.py
   :language: python

.. note::
    Some pdu parameters require locking to protect them from being updated by Jasmin, more_ on this.

.. _more: :ref:`faq_2_Ppkrtcdeai`

Activate logging
================

The following is an example of activating log inside a script:

.. literalinclude:: sc_logging.py
   :language: python
