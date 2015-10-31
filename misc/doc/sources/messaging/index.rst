###############
Messaging flows
###############

Messaging is heavily relying on an AMQP broker using topics to queue messages for routing, delivering and acking back.

The AMQP broker is providing a strong store & forward queuing mechanism, through the following illustration you can see how every messaging component is asynchronously connected to the broker.

.. figure:: /resources/messaging/messaging.png
   :alt: AMQP Messaging flows
   :align: Center

   AMQP Messaging flows

Five main actors are messaging through the "*messaging*" topic, their business logic are explained in the below paragraphs.

SMPPClientManagerPB
*******************

This is a *PerspectiveBroker* (PB) responsible of managing SMPP Client connectors (list, add, remove, start, stop,
send SMS, etc ...), we'll be only covering the latter (Send SMS).

When the **perspective_submit_sm()** is called with a SubmitSm PDU and destination connector ID, it will build
an AMQP Content message and publish it to a queue named **submit.sm.CID** where *CID* is the destination connector ID.

.. note:: **perspective_submit_sm()** is called from HTTP API and SMPP Server API after they check with RouterPB for the right connector to send a SubmitSM to.

Every SMPP Connector have a consumer waiting for these messages, once published as explained above, it will be consumed by
the destination connector's **submit_sm_callback()** method (c.f. :ref:`SMPPClientSMListener`).

.. _RouterPB:

RouterPB
********

This is another *PerspectiveBroker* (PB) responsible of routing DeliverSm messages, these are received through
the SMPP client connector's *deliver_sm_event_interceptor()* method (c.f. :ref:`SMPPClientSMListener`) which
publish to **deliver.sm.CID**, the RouterPB main role is to decide whether to route DeliverSm messages to:

* **deliver_sm_thrower.smpps**: if the message is to be delivered through SMPP Server API.
* **deliver_sm_thrower.http**: if the message is to be delivered through a HTTP connector.

.. _SMPPClientSMListener:

SMPPClientSMListener
********************

Every SMPP Client connector have one attached *SMPPClientSMListener* instance, it is responsible for handling messages exchanged through the SMPP Client connector using the following event catchers:

deliver_sm_event_interceptor
============================

Every received DeliverSm PDU is published directly to the broker with the following assumptions:

* If it's a SMS-MO message it will get published as an AMQP Content message to **deliver_sm.CID** where *CID* is the source connector ID, this message will be handled by the :ref:`RouterPB`.
* If it's a delivery receipt and if it were requested when sending the SubmitSm, it will get published as an AMQP Content message to **dlr_thrower.http** or **dlr_thrower.smpps** (depends on the used channel for sending initial SubmitSM) for later delivery by DLRThrower's **dlr_throwing_callback()** method.

.. note:: **deliver_sm_event_interceptor()** will check for interception rules before proceding to routing, c.f.
  :doc:`/interception/index` for more details.

submit_sm_callback
==================

It is a simple consumer of **submit.sm.CID** where *CID* is its connector ID, it will send every message received through SMPP connection.

submit_sm_resp_event
====================

It is called for every received SubmitSmResp PDU, will check if the related SubmitSm was requiring a delivery receipt and will publish it (or not) to **dlr_thrower.http** or **dlr_thrower.smpps** (depends on the used channel for sending initial SubmitSM).

.. note:: There's no actual reason why messages are published to **submit.sm.resp.CID**, this may change in future.

deliverSmThrower
********************

This is will through any received message from **deliver_sm_thrower.http** to its final http connector, c.f. :ref:`receiving_sms-mo` for details and from **deliver_sm_thrower.smpps** to its final SMPP Server binding.

DLRThrower
**********

This is will through any received delivery receipt from **dlr_thrower.http** to its final http connector, c.f. :ref:`receiving_dlr` for details and from **dlr_thrower.smpps** to its final SMPP Server binding.
