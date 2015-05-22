##########################
Frequently Asked Questions
##########################

.. _faq_for_users:

For users
*********

.. _faq_1_Cnfavtstrj:

Could not find a version that satisfies the requirement jasmin
==============================================================

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

.. _faq_1_CcttcasJ:

Cannot connect to telnet console after starting Jasmin
======================================================

According to the installation guide, Jasmin requires running RabbitMQ and Redis servers, when starting it will wait for these servers to go up.

If you already have these requirements, please check jcli and redis-client logs:

* /var/log/jasmin/redis-client.log
* /var/log/jasmin/jcli.log

.. hint::
    Please check :ref:`installation_prerequisites` before installing.

.. _faq_for_developers:

For developers
**************

.. _faq_2_Htlmiatpd:

How to 'log' messages in a third party database ?
=================================================

Jasmin runs without a database, everything is in-memory and messages are exchanged through AMQP broker (RabbitMQ), if you need to get these messages you have to consume from the right queues as described in :doc:`/messaging/index`.

Here's an example:

Thanks to Pedro_'s contribution::

  Here is the PySQLPool mod to @zoufou ´s gist
  https://gist.github.com/pguillem/5750e8db352f001138f2

  Here is the code to launch the consumer as a system Daemon in Debian/Ubuntu
  https://gist.github.com/pguillem/19693defb3feb0c02fe7

  1) create jasmind_consumer file in /etc/init.d/
  2) chmod a+x
  3) Modify the path and script name of your consumer in jasmind_consumer
  4) Remember to exec "update-rc.d jasmind_consumer defaults" in order to start at boot

  Cheers
  Pedro

*More on this:*

.. literalinclude:: consume_MT_messages.py
   :language: python

.. _Pedro: https://github.com/pguillem

.. _faq_2_HtdatPBA:

How to directly access the Perspective Broker API ?
===================================================

Management tasks can be done directly when accessing PerspectiveBroker_ API, it will be possible to:

* Manage SMPP Client connectors,
* Check status of all connectors,
* Send SMS,
* Manage Users & Groups,
* Manage Routes (MO / MT),
* Access statistics,
* ...

Here's an example:

.. literalinclude:: using_pb.py
   :language: python

.. _PerspectiveBroker: http://twistedmatrix.com/documents/current/core/howto/pb-intro.html

.. _faq_2_CypaeohtuE:

Can you provide an example of how to use EvalPyFilter ?
=======================================================

Let's say you need your filter to pass only messages from username **foo**::

  if routable.user == 'foo':
      result = False
  else:
      result = True

.. note::
    Although **UserFilter** is already there to provide this feature, this is just a simple example of using EvalPyFilter.

So your python script will have a **routable** global variable, it is an instance of **RoutableDeliverSm** if you're playing with a MO Route and it will be an instance of **RoutableSubmitSm** if you're considering it with a MT Route.

In order to implement your specific filter, you have to know all_ the attributes these objects are providing, 

Now let's make an advanced example, the below filter will:

* Connect to a database
* Check if the message *destination_address* is in *blacklisted_numbers* table
* Pass only if the *destination_address* is not blacklisted

.. literalinclude:: advanced_evalpyfilter.py
   :language: python

.. _faq_2_HtsaEfaMR:

How to set an EvalPyFilter for a MT Route ?
===========================================

I have written my *EvalPyFilter*, how can i use it to filter MT messages ?

Using jCli:

First, create your filter::

  jcli : filter -a
  Adding a new Filter: (ok: save, ko: exit)
  > type evalpyfilter
  > pyCode /some/path/advanced_evalpyfilter.py
  > fid blacklist_check
  > ok
  Successfully added Filter [EvalPyFilter] with fid:blacklist_check

Second, create a MT Route::

  jcli : mtrouter -a
  Adding a new MT Route: (ok: save, ko: exit)
  > type StaticMTRoute
  jasmin.routing.Routes.StaticMTRoute arguments:
  filters, connector, rate
  > filters blacklist_check
  > connector smppc(SOME-SMSC)
  > rate 0.0
  > order 10
  > ok
  Successfully added MTRoute [StaticMTRoute] with order:10

And you're done ! test your filter by sending a SMS through Jasmin's APIs.

.. _all: :ref:`Route_Routable`