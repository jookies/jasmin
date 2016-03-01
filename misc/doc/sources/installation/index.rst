############
Installation
############

The Installation section is intended to get you up and running quickly with a simple SMS sending scenario through :doc:`/apis/ja-http/index` or :doc:`/apis/smpp-server/index`.

Jasmin installation is provided as rpm & deb Linux packages, docker image and pypi package.

.. important:: Jasmin needs a working **RabbitMQ** and **Redis** servers, more info in :ref:`installation_prerequisites` below.

.. _installation_prerequisites:

Prerequisites & Dependencies
****************************

`Jasmin <http://jasminsms.com/>`_ requires Python 2.7 or newer (but not Python 3) with a functioning `pip module <https://pypi.python.org/pypi/pip>`_.

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

`Jasmin <http://jasminsms.com/>`_ can be installed through **DEB** packages hosted on `Packagecloud <https://packagecloud.io/jookies/python-jasmin>`_::

  wget -qO - http://bit.ly/jasmin-deb-repo | sudo bash
  sudo apt-get install python-jasmin

.. note:: Ubuntu 15.04 and higher versions are supported.

Once Jasmin installed, you may simply start the **jasmind** service::

  sudo systemctl enable jasmind
  sudo systemctl start jasmind

.. note:: redis and rabbitmq must be started with jasmin.

RHEL & CentOS
*************

`Jasmin <http://jasminsms.com/>`_ can be installed through **RPM** packages hosted on `Packagecloud <https://packagecloud.io/jookies/python-jasmin>`_::

  wget -qO - http://bit.ly/jasmin-rpm-repo | sudo bash
  sudo yum install python-jasmin

.. note:: Red Hat Enterprise Linux 7 & CentOS 7 are supported.

You may get the following error if **RabbitMQ** or **Redis** server are not installed::

  No package redis available.
  No package rabbitmq-server available.

These requirements are available from the `EPEL repository <https://fedoraproject.org/wiki/EPEL>`_, you'll need to enable it before installing Jasmin::

  ## RHEL/CentOS 7 64-Bit ##
  yum -y install https://dl.fedoraproject.org/pub/epel/epel-release-latest-7.noarch.rpm

Once Jasmin installed, you may simply start the **jasmind** service::

  sudo systemctl enable jasmind
  sudo systemctl start jasmind

.. note:: redis and rabbitmq must be started with jasmin.

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

Once Jasmin installed, execute the following steps to start Jasmin as a system service::

  # On ubuntu:
  sudo wget http://bit.ly/jasmind-ubuntu -O /etc/init.d/jasmind
  # On redhat, centos:
  sudo wget http://bit.ly/jasmind-redhat -O /etc/init.d/jasmind

  # Then:
  sudo chmod +x /etc/init.d/jasmind
  sudo update-rc.d jasmind defaults
  sudo invoke-rc.d jasmind start

.. note:: On some Linux distributions, you may use **sudo systemctl enable jasmind**.

.. note:: redis and rabbitmq must be started with jasmin.

Docker
******

You probably have heard of `Docker <https://www.docker.com/>`_, it is a container technology with a ton of momentum. But if you
haven't, you can think of containers as easily-configured, lightweight VMs that start up fast, often in under
one second. Containers are ideal for `microservice architectures <https://en.wikipedia.org/wiki/Microservices>`_
and for environments that scale rapidly or release often, Here's more from `Docker's website <https://www.docker.com/what-docker>`_.

Installing Docker
=================

Before we get into containers, we'll need to get Docker running locally. You can do this by installing the
package for your system (tip: you can find `yours here <https://docs.docker.com/installation/#installation>`_).
Running a Mac? You'll need to install the `boot2docker application <http://boot2docker.io/>`_ before using Docker.
Once that's set up, you're ready to start using Jasmin container !

Pulling Jasmin image
====================

This command will pull latest jasmin docker image to your computer::

    docker pull jookies/jasmin

You should have Jasmin image listed in your local docker images::

    # docker images
    REPOSITORY          TAG                 IMAGE ID            CREATED             VIRTUAL SIZE
    jasmin              latest              0e4cf8879899        36 minutes ago      478.6 MB

.. note:: The Jasmin docker image is a self-contained/standalone box including Jasmin+Redis+RabbitMQ.

Starting Jasmin in a container
==============================

This command will create a new docker container with name *jasmin_01* which run as a demon::

    docker run -d -p 1401:1401 -p 2775:2775 -p 8990:8990 --name jasmin_01 jookies/jasmin:latest

Note that we used the parameter **-p** three times, it defines port forwarding from host computer to the container,
typing **-p 2775:2775** will map the container's 2775 port to your host 2775 port; this can
be useful in case you'll be running multiple containers of Jasmin where you keep a port offset of 10 between
each, example::

    docker run -d -p 1411:1401 -p 2785:2775 -p 8990:8990 --name jasmin_02 jookies/jasmin:latest
    docker run -d -p 1421:1401 -p 2795:2775 -p 9000:8990 --name jasmin_03 jookies/jasmin:latest
    docker run -d -p 1431:1401 -p 2805:2775 -p 9010:8990 --name jasmin_04 jookies/jasmin:latest

You should have the container running by typing the following::

    # docker ps
    CONTAINER ID  IMAGE                   COMMAND                CREATED         STATUS         PORTS                                                                    NAMES
    0a2fafbe60d0  jookies/jasmin:latest   "/docker-entrypoint.   43 minutes ago  Up 41 minutes  0.0.0.0:1401->1401/tcp, 0.0.0.0:2775->2775/tcp, 0.0.0.0:8990->8990/tcp   jasmin_01

And in order to control the container **jasmin_01**, use::

    docker stop jasmin_01
    docker start jasmin_01

It's possible to access log files located in **/var/log/jasmin** inside the container by mounting it as a shared
folder::

    docker run -d -v /home/user/jasmin_logs:/var/log/jasmin --name jasmin_100 jookies/jasmin:latest

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

Sending outbound SMS (MT) is simply done through Jasmin's HTTP API (refer to :doc:`/apis/ja-http/index` for detailed information about sending and receiving SMS and receipts)::

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
