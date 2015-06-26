#######
Billing
#######

`Jasmin <http://jasminsms.com/>`_ comes with a user billing feature that lets you apply rates on message routes, every time a user sends a SMS through a rated route he'll get charged, once he runs out of credit no more sending will be permitted.

.. important:: New routes created through :ref:`mtrouter_manager` are not rated by default, you must :ref:`define <mtrouter_manager>` the rate of each route in order to enable billing.
.. note:: Billing is applied on all channels (SMPP Server and HTTP API) the same way.

Billing quotas
**************

A user can be charged through 2 types of quotas (balance and/or sms_count), if he reaches the limit of one of these quotas no more sending will be permitted, no matter the used channel (SMPP Server or HTTP API).

.. _billing_type_1:

1. Balance quota
================

The route rate will be charged on the user balance, let's get into these use cases for better comprehension:

* When sending one SMS through a route rated **1.2**, user's balance will get decreased by **1.2**
* When sending five SMS through a route rated **0.2**, user's balance will get decreased by **1**

.. important:: New users created through :ref:`user_manager` will have unlimited balance by default, assuming you'll apply postpaid billing (*or no billing at all*), user's balance must be :ref:`defined <user_credentials>` in order to enable billing.

Rate unit
---------

You can see that the rates have no *unit* or *currency*, this will offer better flexibility for different business cases, you can consider the rates as:

* Local Jasmin currency and keep a rate for converting to real-life currency.
* Real-life currency
* etc ..

In all cases, Jasmin will never manage the rate *unit* (or *currency*), all it does is to ensure users are correctly charged by the rates you define.

.. _billing_async:

Asynchronous billing
--------------------

As explained :ref:`later <billing_call_flow_async>`, it is important to know that whatever the used protocol, SMS is always sent **asynchronously**, this means there's always an acknowledgment to be received for every sent SMS; Jasmin provides an *optional* adapted billing :ref:`algorithm <billing_process_flow>` which is able to charge the user **asynchronously**:

#. A defined percentage of the route rate is charged when the user submits the SMS for sending.
#. The rest is charged when the SMS is acknowledged by the next relay, in SMPP protocol, this means receiving **SUBMIT_SM_RESP** PDU, more details :ref:`here <billing_call_flow_async>`.

Asynchronous billing is automatically enabled when the user have **early_decrement_balance_percent** :ref:`defined <user_credentials>` (undefined by default), let's get back to examples for better comprehension, assuming user have **early_decrement_balance_percent = 25**:

* When sending one SMS through a route rated **1.2**:

 * When sending, user's balance is decreased by **0.3** (*1.2* x **25%**)
 * When acknowledged, user's balance is decreased by **0.9** (the rest)

* When sending **five** SMS through a route rated **0.2**:

 * When sending, user's balance is decreased by **0.25** (**5** x *0.2* x **25%**)
 * For each acknowledged SMS, user's balance is decreased by **0.15**
 * When all **five** sent messages are acknowledged, the final charged amount is **0.75** (the rest)

Using asynchronous billing can be helpful in many use cases:

* Charge only when the SMS is acknowledged
* If SMS is not acknowledged for some reason, user can not fill Jasmin's queues by SMS requests indefinitely, he'll get out of credits
* etc ..

.. _billing_type_2:

2. sms_count quota
========================

Simpler than *Balance* management, *sms_count* is a counter to be decreased whenever the user submits the SMS for sending, let's get into these use cases for better comprehension:

* When sending one SMS through a route, user's *sms_count* will get decreased by **1**
* When sending five SMS through a route, user's *sms_count* will get decreased by **5**


.. note:: When defined, *sms_count* is always decreased no matter the route is rated or not.

.. important:: New users created through :ref:`user_manager` will have unlimited sms_count by default, assuming you'll apply postpaid billing (*or no billing at all*), user's *sms_count* must be :ref:`defined <user_credentials>` in order to enable billing (or limit).

.. _billing_process_flow:

Process flow
************

The following process flow shows how billing is done through HTTP Api (same process is applied on SMPP Server), it is including all types of billing:

* balance quota billing (:ref:`ref <billing_type_1>`) including asynchronous billing (:ref:`ref <billing_async>`)
* sms_count quota billing (:ref:`ref <billing_type_2>`)

.. figure:: /resources/billing/billing-process.png
   :alt: Billing process flow
   :align: Center
   
   Billing process flow

.. _billing_call_flow_async:

Asynchronous billing call flow
==============================

When enabled, :ref:`Asynchronous billing <billing_async>` algorithm can charge user every time an acknowledgment is received for each SMS he sent earlier, the following call flow explain the asynchronous billing algorithm:

.. figure:: /resources/billing/async-callflow.png
   :alt: Asynchronous billing call flow
   :align: Center
   
   Asynchronous billing call flow

In the above figure, user is charged early before submitting SMS to SMSC, and the charged later when the SMSC acknowledge back reception of the message, as detailed :ref:`earlier <billing_async>`, the charged amount in early stage is defined by **early_decrement_balance_percent** :ref:`set in user profile <user_credentials>`.

.. note:: The route rate is expressed on a per-SUBMIT_SM basis, submitting a long SMS will be splitted into multiple **submit_sm SMPP PDUs**, each one will be charged on user.

The below figure explain how asynchronous billing is handling long content messages, assuming a user is submitting a message containing 400 characters, which will imply sending 3 **submit_sm SMPP PDUs**:

.. figure:: /resources/billing/async-callflow-long-content.png
   :alt: Asynchronous billing call flow for long content messages
   :align: Center
   
   Asynchronous billing call flow for long content messages

Asynchronous billing is mainly relying on AMQP broker (like :doc:`messaging </messaging/index>`), The AMQP broker is providing a queuing mechanism, through the following illustration you can see how asynchronous billing is done:

.. figure:: /resources/billing/billing.png
   :alt: Asynchronous billing AMQP message exchange
   :align: Center
   
   Asynchronous billing AMQP message exchange

When receiving a **SUBMIT_SM_RESP** PDU, *submit_sm_resp_event()* method is called (:ref:`more details here <billing_async>`), it will check if there's a remaining bill to charge on user and publish it on **bill_request.submit_sm_resp.UID** (using *billing* exchange) where UID is the concerned User ID.

RouterPB's *bill_request_submit_sm_resp_callback()* is listening on the same topic and it will be fired whenever it consumes a new bill request, as the Router is holding User objects in memory, it will simply update their balances with the bill amount.

Jasmin is doing everything in-memory for performance reasons, including User charging where the balance must be persisted to disk for later synchronization whenever Jasmin is restarted, this is why RouterPB is automatically persisting Users and Groups to disk every **persistence_timer_secs** seconds as defined in jasmin.cfg file (INI format, located in /etc/jasmin).

.. important:: Set **persistence_timer_secs** to a reasonable value, keep in mind that every disk-access operation will cost you few performance points, and don't set it too high as you can loose Users balance data updates.