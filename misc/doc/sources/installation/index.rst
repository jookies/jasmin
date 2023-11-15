############
Installation
############

The Installation section is intended to get you up and running quickly with a simple SMS sending scenario through :doc:`/apis/http/index` or :doc:`/apis/smpp-server/index`.

Jasmin installation is provided as rpm & deb Linux packages, docker image and pypi package.

.. important:: Jasmin needs a working **RabbitMQ** and **Redis** servers, more info in :ref:`installation_prerequisites` below.

.. _installation_prerequisites:

Prerequisites & Dependencies
****************************

`Jasmin <http://jasminsms.com/>`_ requires Python 3.8+ with a functioning `pip module <https://pypi.python.org/pypi/pip>`_.

.. hint:: Latest pip module installation: # **curl https://bootstrap.pypa.io/get-pip.py | python**

Depending on the Linux distribution you are using, you may need to install the following dependencies:

* `RabbitMQ Server <https://www.rabbitmq.com>`_, Ubuntu package name: **rabbitmq-server**. RabbitMQ is used heavily by Jasmin as its core AMQP.
* `Redis Server <http://redis.io/>`_, Ubuntu package name: **redis-server**. Redis is used mainly for mapping message ID's when receiving delivery receipts.
* header files and a static library for Python, Ubuntu package name: **python-dev**
* Foreign Function Interface library (development files), Ubuntu package name: **libffi-dev**
* Secure Sockets Layer toolkit - development files, Ubuntu package name: **libssl-dev**
* `Twisted Matrix <https://twistedmatrix.com>`_, Python Event-driven networking engine, Ubuntu package name: **python-twisted**

Ubuntu
******

`Jasmin <http://jasminsms.com/>`_ can be installed through **DEB** packages hosted on `Packagecloud <https://packagecloud.io/jookies/jasmin-sms-gateway>`_::

    curl -s https://setup.jasminsms.com/deb | sudo bash
    sudo apt-get install jasmin-sms-gateway

.. note:: Ubuntu 20.04 and newer versions are supported.

You have to install and setup **RabbitMQ** or **Redis** servers on same machine (Default configuration) or on separate ones (Requires Jasmin configuration: /etc/jasmin/jasmin.cfg).

.. note:: redis and rabbitmq must be installed and already running.

Once Jasmin installed, you may simply start the **jasmind** service::

    sudo systemctl enable jasmind
    sudo systemctl start jasmind

.. note:: redis and rabbitmq must be installed and already running.

RHEL & CentOS
*************

`Jasmin <http://jasminsms.com/>`_ can be installed through **RPM** packages hosted on `Packagecloud <https://packagecloud.io/jookies/jasmin-sms-gateway>`_::

    curl -s https://setup.jasminsms.com/rpm | sudo bash
    sudo yum install epel-release
    sudo yum install jasmin-sms-gateway

.. note:: Many dependencies are installed from the Epel repository, please pay attention to activating this repository before installing jasmin-sms-gateway package.
.. note:: Red Hat Enterprise Linux 8 & CentOS 8 and newer versions are supported.

You have to install and setup **RabbitMQ** or **Redis** servers on same machine (Default configuration) or on separate ones (Requires Jasmin configuration: /etc/jasmin/jasmin.cfg).

.. note:: redis and rabbitmq must be installed and already running.

Once Jasmin installed, you may simply start the **jasmind** service::

    sudo systemctl enable jasmind
    sudo systemctl start jasmind


Pypi
****

Having another OS not covered by package installations described above ? using the Python package installer will be possible, you may have to follow these instructions:

System user
===========

Jasmin system service is running under the *jasmin* system user, you will have to create this user under *jasmin* group::

    sudo useradd jasmin

System folders
==============

In order to run as a POSIX system service, Jasmin requires the creation of the following folders before installation::

    /etc/jasmin
    /etc/jasmin/resource
    /etc/jasmin/store       #> Must be owned by jasmin user
    /var/log/jasmin         #> Must be owned by jasmin user

.. _installation_linux_steps:

Installation
============

The last step is to install jasmin through `pip <https://pypi.python.org/pypi/pip>`_::

    sudo pip install jasmin

systemd scripts must be downloaded from `here <https://github.com/jookies/jasmin/tree/master/misc/config/systemd>` and
manually installed into your system, once placed in **/lib/systemd/system** jasmind shall be enabled and started::

    sudo systemctl enable jasmind
    sudo systemctl start jasmind

.. note:: redis and rabbitmq must be started with jasmin.

Docker
******

Containers are ideal for `microservice architectures <https://en.wikipedia.org/wiki/Microservices>`_
and for environments that scale rapidly or release often, Here's more from `Docker's website <https://www.docker.com/what-docker>`_.

Installing Docker
=================

Before we get into containers, we'll need to get Docker running locally. You can do this by installing the
package for your system (tip: you can find `yours here <https://docs.docker.com/installation/#installation>`_).

Once that's set up, you're ready to start using Jasmin container !

Using docker-compose
====================

Create a file named "docker-compose.yml" and paste the following:

.. literalinclude:: /installation/docker-compose.yml
   :language: yaml

Then spin it::

    docker-compose up -d

This command will pull latest jasmin v0.10, latest redis and latest rabbitmq images to your computer::

    # docker images
    REPOSITORY          TAG                 IMAGE ID            CREATED             VIRTUAL SIZE
    jasmin              latest              0e4cf8879899        36 minutes ago      478.6 MB

Jasmin is now up and running::

    # docker ps
    CONTAINER ID   IMAGE                 COMMAND                  CREATED         STATUS         PORTS                                                                    NAMES
    1a9016d298bf   jookies/jasmin:0.10   "/docker-entrypoint.…"   3 seconds ago   Up 2 seconds   0.0.0.0:1401->1401/tcp, 0.0.0.0:2775->2775/tcp, 0.0.0.0:8990->8990/tcp   jasmin
    af450de4fb95   rabbitmq:alpine       "docker-entrypoint.s…"   5 seconds ago   Up 3 seconds   4369/tcp, 5671-5672/tcp, 15691-15692/tcp, 25672/tcp                      rabbitmq
    c8feb6c07d94   redis:alpine          "docker-entrypoint.s…"   5 seconds ago   Up 3 seconds   6379/tcp                                                                 redis

.. note:: You can play around with the docker-compose.yml to choose different versions, mounting the configs outside the container, etc ...

.. _monitoring_grafana:

Monitoring using Grafana
************************

Through its native exporter for `Prometheus <https://prometheus.io/>`_ you can collect and analyze detailed metrics within a production environment, we will be using the /metrics API (:ref:`get_metrics`) with `Prometheus <https://prometheus.io/>`_  and `Grafana <https://grafana.com/>`_ in this guide.

Prepare Prometheus's settings:

.. literalinclude:: /installation/prometheus.yml
   :language: yaml

The use the following docker-compose including prometheus and grafana:

.. literalinclude:: /installation/docker-compose.grafana.yml
   :language: yaml

Spin it:

    docker-compose -f docker-compose.grafana.yml up -d

You should have the following containers up and running::

    CONTAINER ID   IMAGE                    COMMAND                  CREATED       STATUS              PORTS                                                                     NAMES
    cd7597137e9a   grafana/grafana          "/run.sh"                2 days ago    Up About a minute   0.0.0.0:3000->3000/tcp                                                    jasmin-grafana-1
    bd3be30a5cd5   prom/prometheus:latest   "/bin/prometheus --c…"   2 days ago    Up About a minute   9090/tcp                                                                  jasmin-prometheus-1
    8209435c2f8d   jasmin-jasmin            "/docker-entrypoint.…"   2 days ago    Up About a minute   0.0.0.0:1401->1401/tcp, 0.0.0.0:2775->2775/tcp, 0.0.0.0:8990->8990/tcp    jasmin
    6c88fa5e47db   rabbitmq:alpine          "docker-entrypoint.s…"   2 days ago    Up About a minute   4369/tcp, 5671-5672/tcp, 15691-15692/tcp, 25672/tcp                       jasmin-rabbit-mq-1
    a649abd164c8   redis:alpine             "docker-entrypoint.s…"   2 days ago    Up About a minute   6379/tcp                                                                  jasmin-redis-1

Now open Grafana using default username (admin) and password (admin)::

  http://127.0.0.1:3000

Then go to *Dashboards* where you'll find 2 folders having a bunch of pre-made dashboards:

* *Jasmin* > **HTTP API**: HTTP Api monitoring,
* *Jasmin* > **SMPP Clients**: Per SMPP Client (cid) monitoring with rabbitmq queues,
* *Jasmin* > **SMPP Server**: SMPP Server monitoring,
* *RabbitMQ* > **RabbitMQ-Overview**: Standard RabbitMQ monitoring,

Now you can start playing around with the collected metrics, go to **Explore** and play with the autocomplete feature in **Metrics browser** by typing **httapi**, **smpps** or **smppc**.

You can also *explore* metrics of a defined SMPP client connector by setting the **cid** tag, example of getting number of bound session of a specific connector::

  smppc_bound_count{cid="foo"}

.. note:: The complete set of metrics exposed by Jasmin can be checked through the **/metrics** http api, these metrics are also exposed through jcli's :ref:`stats_manager` module.

.. _install_k8s:

Kubernetes cluster
******************

This part of the documentation covers clustering Jasmin SMS Gateway using `Kubernetes <https://kubernetes.io/>`_, it is also made as a reference setup for anyone looking to deploy Jasmin in cloud architectures, this is a proof-of-concept model for deploying simple clusters, these were used for making stress tests and performance metering of the sms gateway.

Before you begin you need to have a Kubernetes cluster, and the **kubectl** command-line tool must be configured to communicate with your cluster. It is recommended to run this tutorial on a cluster with at least two nodes that are not acting as control plane hosts. If you do not already have a cluster, you can create one by using minikube or you can use one of these Kubernetes playgrounds:

* `Okteto <https://www.okteto.com/>`_
* `Killercoda <https://killercoda.com/playgrounds/scenario/kubernetes>`_
* `Play with Kubernetes <https://labs.play-with-k8s.com/>`_

Your Kubernetes server must be at or later than version v1.10. To check the version, enter *kubectl version*.

Simple k8s architecture
=======================

This is barely simple architecture with running pods and a SMPP simulator to allow simple functional or performance testing.

.. note:: This section of the guide uses the provided Kubernetes objects located in this `directory <https://github.com/jookies/jasmin/blob/master/kubernetes/simple-pods>`_, please note that you may need to prepare volumes and metallb ip address pools to make these manifests run on your bare-metal K8s cluster.
.. note:: Please note this set of K8s manifests are prepared for a bare-metal cluster and you may need to adjust it for cloud/managed clusters where volumes, networking and services are handled with a slight difference.

Start by adjusting the namespace in **configmaps.yml**: replace the rabbitmq and redis hosts to hostnames provided by your own Kubernetes cluster then deploy:

1. kubectl apply -f redis.yml
2. kubectl apply -f rabbitmq.yml
3. kubectl apply -f jasmin.yml

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

If you need to connect Jasmin to a *provided smpp simulator* then first deploy the simulator::

  kubectl apply -f smppsimulator.yml

And then add a new SMPP client connector by following these steps:

.. code-block:: bash

   smppccm -a
   > cid smpp_simulator
   > host smpp-simulator
   > username smppclient1
   > password password
   > ok
   smppccm -1 smpp_simulator

You will also need to create a group, user and at least a mt route to make your first sms delivery test, `this guide is your friend ! <https://docs.jasminsms.com/en/latest/installation/index.html#sending-your-first-sms>`_

.. note::

   You may adjust the **host** value in the example above to your own host (provided by your Kubernetes cluster).


Sending your first SMS
**********************

For the really impatient, if you want to give Jasmin a whirl right now and send your first SMS, you'll have to connect to :doc:`/management/jcli/index` and setup a connection to your SMS-C, let's **assume** you have the following SMPP connection parameters as provided from your partner:

.. list-table:: Basic SMPP connection parameters
   :widths: 10 10 80
   :header-rows: 1

   * - Paramater
     - Description
     - Value
   * - **Host**
     - Host of remote SMS-C
     - 172.16.10.67
   * - **Port**
     - SMPP port on remote SMS-C
     - 2775
   * - **Username**
     - Authentication username
     - smppclient1
   * - **Password**
     - Authentication password
     - password
   * - **Throughput**
     - Maximum sent SMS/second
     - 110

.. note:: In the next sections we'll be heavily using jCli console, if you feel lost, please refer to :doc:`/management/jcli/index` for detailed information.

1. Adding SMPP connection
=========================

Connect to jCli console through telnet (**telnet 127.0.0.1 8990**) using **jcliadmin/jclipwd** default authentication parameters and add a new connector with an *CID=DEMO_CONNECTOR*::

    Authentication required.

    Username: jcliadmin
    Password:
    Welcome to Jasmin console
    Type help or ? to list commands.

    Session ref: 2
    jcli : smppccm -a
    > cid DEMO_CONNECTOR
    > host 172.16.10.67
    > port 2775
    > username smppclient1
    > password password
    > submit_throughput 110
    > ok
    Successfully added connector [DEMO_CONNECTOR]

2. Starting the connector
=========================

Let's start the newly added connector::

	jcli : smppccm -1 DEMO_CONNECTOR
	Successfully started connector id:DEMO_CONNECTOR

You can check if the connector is bound to your provider by checking its log file (default to /var/log/jasmin/default-DEMO_CONNECTOR.log) or through jCli console::

	jcli : smppccm --list
	#Connector id                        Service Session          Starts Stops
	#DEMO_CONNECTOR                      started BOUND_TRX        1      0
	Total connectors: 1

3. Configure simple route
=========================

We'll configure a default route to send all SMS through our newly created DEMO_CONNECTOR::

	jcli : mtrouter -a
	Adding a new MT Route: (ok: save, ko: exit)
	> type defaultroute
	jasmin.routing.Routes.DefaultRoute arguments:
	connector
	> connector smppc(DEMO_CONNECTOR)
	> rate 0.00
	> ok
	Successfully added MTRoute [DefaultRoute] with order:0

4. Create a user
================

In order to use Jasmin's HTTP API to send SMS messages, you have to get a valid user account, that's what we're going to do below.

First we have to create a group to put the new user in::

    jcli : group -a
	Adding a new Group: (ok: save, ko: exit)
	> gid foogroup
	> ok
	Successfully added Group [foogroup]

And then create the new user::

	jcli : user -a
	Adding a new User: (ok: save, ko: exit)
	> username foo
	> password bar
	> gid foogroup
	> uid foo
	> ok
	Successfully added User [foo] to Group [foogroup]

5. Send SMS
===========

Sending outbound SMS (MT) is simply done through Jasmin's HTTP API (refer to :doc:`/apis/http/index` for detailed information about sending and receiving SMS and receipts)::

	http://127.0.0.1:1401/send?username=foo&password=bar&to=06222172&content=hello

Calling the above url from any brower will send an SMS to **06222172** with **hello** content, if you receive a response like the below example it means your SMS is accepted for delivery::

	Success "9ab2867c-96ce-4405-b890-8d35d52c8e01"

For more troubleshooting about message delivery, you can check details in related log files in **/var/log/jasmin**:

.. list-table:: Messaging related log files
   :widths: 10 90
   :header-rows: 1

   * - Log filename
     - Description
   * - **messages.log**
     - Information about queued, rejected, received and sent messages
   * - **default-DEMO_CONNECTOR.log**
     - The SMPP connector log file
