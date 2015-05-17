##########################
Frequently Asked Questions
##########################

.. _faq_01:

Could not find a version that satisfies the requirement jasmin
**************************************************************

Installing Jasmin using **pip** will through this error::

  $ sudo pip install jasmin
  [sudo] password for richard: 
  Downloading/unpacking jasmin
      Could not find a version that satisfies the requirement jasmin (from versions: 0.6b1, 0.6b10, 0.6b11, 0.6b12, 0.6b13, 0.6b14, 0.6b2, 0.6b3, 0.6b4, 0.6b5, 0.6b6, 0.6b7, 0.6b8, 0.6b9)
  Cleaning up...
  No distributions matching the version for jasmin
  Storing debug log for failure in /home/richard/.pip/pip.log

This is common question, since Jasmin is still tagged as a 'Beta' version, pip installation must be done with the **--pre** parameter::

  $ sudo pip install --pre jasmin
  ...

.. hint::
    This is clearly documented in :ref:`installation_linux_steps` installation steps.

.. _faq_02:

Cannot connect to telnet console after starting Jasmin
******************************************************

According to the installation guide, Jasmin requires running RabbitMQ and Redis servers, when starting it will wait for these servers to go up.

If you already have these requirements, please check jcli and redis-client logs:

* /var/log/jasmin/redis-client.log
* /var/log/jasmin/jcli.log

.. hint::
    Please check :ref:`installation_prerequisites` before installing.

.. _faq_03:

How to 'log' messages in a third party database ?
*************************************************

Jasmin runs without a database, everything is in-memory and messages are exchanged through AMQP broker (RabbitMQ), if you need to get these messages you have to consume from the right queues as described in :doc:`/messaging/index`.

Here's an example:

.. literalinclude:: consume_MT_messages.py
   :language: python