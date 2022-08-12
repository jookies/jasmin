Kubernetes clustering for Jasmin
################################

Overview
========

This is the documentation for clustering Jasmin SMS Gateway, it is also made as a reference configuration, it was built as a proof-of-concept model for deploying simple and advanced clusters, these were used for making stress tests and performance metering of the sms gateway, `documented here <http://docs.jasminsms.com/@TODO>`_.

Before you begin
----------------

* You need to have a Kubernetes cluster, and the **kubectl** command-line tool must be configured to communicate with your cluster. It is recommended to run this tutorial on a cluster with at least two nodes that are not acting as control plane hosts. If you do not already have a cluster, you can create one by using minikube or you can use one of these Kubernetes playgrounds:
 * `Okteto <https://www.okteto.com/>`_
 * `Killercoda <https://killercoda.com/playgrounds/scenario/kubernetes>`_
 * `Play with Kubernetes <https://labs.play-with-k8s.com/>`_
Your Kubernetes server must be at or later than version v1.10. To check the version, enter kubectl version.

How to read this document
-------------------------

This README covers two different ways of clustering Jasmin:

* `Simple pods architecture`_: Barely simple architecture with running pods and a SMPP simulator to allow simple functional or performance testing.
* `Advanced deployment architecture`_: *[work in progress]*

Simple pods architecture
========================

This section of the guide uses the provided Kubernetes objects located in the **simple-pods/** directory.

Start by adjusting the namespace in **configmaps.yml**: replace the rabbitmq and redis hosts to hostnames provided by your own Kubernetes cluster then deploy:

1. kubectl apply -f configmaps.yml
2. kubectl apply -f redis.yml
3. kubectl apply -f rabbitmq.yml
4. kubectl apply -f jasmin.yml
5. kubectl apply -f smppsimulator.yml

You should have the cluster up and running within seconds, your Jasmin pod must log to stdout the following messages:

.. code-block:: bash

   INFO 1 Starting Jasmin Daemon ...
   INFO 1 Interceptor client Started.
   INFO 1 RedisClient Started.
   INFO 1 AMQP Broker Started.
   INFO 1 RouterPB Started.
   INFO 1 SMPPClientManagerPB Started.
   INFO 1 DLRLookup Started.
   INFO 1 SMPPServer Started.
   INFO 1 deliverSmThrower Started.
   INFO 1 DLRThrower Started.
   INFO 1 HTTPApi Started.
   INFO 1 jCli Started.

.. warning::

   If you don't have the indicated above logged lines to Jasmin's pod stdout then you are having troubles somewhere, do not step forward before solving them.

Now you can connect jcli by *first* running a port-forward and then connecting to the forwarded port:

.. code-block:: bash

   kubectl port-forward jasmin 8990:8990

Then:

.. code-block:: bash

   telnet 127.0.0.1 8990

.. note::

   The **kubectl port-forward** command will not return unless you *ctrl-c* to stop the port-forward, the second command (telnet) needs to be run in another terminal session.

You can now make the same steps to port-forward the smpp (2775) port or the http (1401) port and start using Jasmin.

If you need to connect Jasmin to a *provided smpp simulator* then add a new SMPP client connector by following these steps:

.. code-block:: bash

   smppccm -a
   > host smppsim.test.farirat.svc.cluster.local
   > username smppclient1
   > password password
   > ok
   smppccm -1 smppsim

You will also need to create a group, user and at least a mt route to make your first sms delivery test, `this guide is your friend ! <https://docs.jasminsms.com/en/latest/installation/index.html#sending-your-first-sms>`_

.. note::

   You may adjust the **host** value in the example above to your own host (provided by your Kubernetes cluster).

Advanced deployment architecture
================================

*[work in progress]*
