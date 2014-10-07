###############
Messaging flows
###############

Messaging is heavily relying on an AMQP broker using topics to queue messages for routing, delivering and acking back.

The AMQP broker is providing a strong store & forward queuing mechanism, through the following illustration you can see how every messaging component is connected to the broker.

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
an AMQP Content message and publish it to a queue named **submit.sm.CID** where CID is the destination connector ID.

Every SMPP Connector have a consumer waiting for these messages, once published as explained above, it will be consumed by 
the destination connector's **submit_sm_callback()** method (c.f. :ref:`SMPPClientSMListener`).

RouterPB
********

This is another *PerspectiveBroker* (PB) responsible of routing DeliverSm messages, these are received through the SMPP 
client connector's *delvier_sm_event()* method (c.f. :ref:`SMPPClientSMListener`) which publish on **deliver.sm.CID**, 
the RouterPB main role is to decide whether to route DeliverSm messages to:

* **deliver_sm_thrower.smppc**: if the message is to be delivered through a SMPP Client connector
* **deliver_sm_thrower.http**: if the message is to be delivered through a HTTP connector

.. _SMPPClientSMListener:

SMPPClientSMListener
********************

Every SMPP Client connector have a *SMPPClientSMListener* instance, it is responsible for handling messages exchanged through
the SMPP Client connector using the following event catchers:

deliver_sm_event
================

Every received DeliverSm PDU is published directly to the broker with the following assumptions:

* If it's a SMS-MO message it will get published as an AMQP Content message to **deliver_sm.CID** where CID is the source connector ID.
* If it's a delivery receipt and if it were requested when sending the SubmitSm, it will get published as an AMQP Content 
  message to **dlr_thrower.http** for later delivery by DLRThrower's **dlr_throwing_callback()** method.

submit_sm_callback
==================

It is a simple consumer of **submit.sm.CID** where CID is its connector ID, it will send every message received through SMPP connection.

submit_sm_resp_event
====================

It is called for every received SubmitSmResp PDU, will check if the related SubmitSm was requiring a delivery receipt and will publish it 
(or not) to **dlr_thrower.http**.

.. note:: There's no actual reason why messages are published to **submit.sm.resp.CID**, this may change in future.

deliverSmHttpThrower
********************

This is will through any received message from **deliver_sm_thrower.http** to its final http connector, c.f. :ref:`receiving_sms-mo` for details.

DLRThrower
**********

This is will through any received delivery receipt from **dlr_thrower.http** to its final http connector, c.f. :ref:`receiving_dlr` for details.