############
Installation
############

The Installation section is intended to get you up and running quickly with a simple SMS sending scenario through :doc:`/apis/ja-http/index` or :doc:`/apis/smpp-server/index`.

.. important:: Jasmin needs a working **RabbitMQ** and **Redis** servers, more info in :ref:`installation_prerequisites`.

Debian & Ubuntu
***************

`Jasmin <http://jasminsms.com/>`_ can be installed through **DEB** packages hosted on `Packagecloud <https://packagecloud.io/jookies/python-jasmin>`_::

  curl -s https://packagecloud.io/install/repositories/jookies/python-jasmin/script.deb.sh | sudo bash
  apt-get install python-jasmin

.. list-table:: DEB OS compliance
   :header-rows: 1

   * - Distribution
     - Tested
     - Result
   * - **Ubuntu 14.04**
     - Yes
     - *Compliant*
   * - **Ubuntu 14.10**
     - Yes
     - *Compliant*
   * - **Ubuntu 15.04**
     - Yes
     - *Compliant*

RHEL & CentOS
*************

`Jasmin <http://jasminsms.com/>`_ can be installed through **RPM** packages hosted on `Packagecloud <https://packagecloud.io/jookies/python-jasmin>`_::

  curl -s https://packagecloud.io/install/repositories/jookies/python-jasmin/script.rpm.sh | sudo bash
  yum install python-jasmin

You may get the following error if **RabbitMQ** or **Redis** server are not installed::

  No package redis available.
  No package rabbitmq-server available.

These requirements are available from the `EPEL repository <https://fedoraproject.org/wiki/EPEL>`_, you'll need to enable it before installing Jasmin::

  ## RHEL/CentOS 7 64-Bit ##
  yum -y install http://dl.fedoraproject.org/pub/epel/7/x86_64/e/epel-release-7-5.noarch.rpm

.. list-table:: RPM OS compliance
   :header-rows: 1

   * - Distribution
     - Tested
     - Result
   * - **RHEL 7.x**
     - Yes
     - *Compliant*
   * - **CentOS 7.x**
     - Yes
     - *Compliant*

Pypi
****

Having another OS not covered by package installations described above ? using the Python package installer will be possible, you may have to follow these instructions:

.. _installation_prerequisites:

Prerequisites
=============

`Jasmin <http://jasminsms.com/>`_ requires Python 2.7 or newer (but not Python 3) with a functioning `pip module <https://pypi.python.org/pypi/pip>`_.

.. hint:: Latest pip module installation:
          # **curl https://bootstrap.pypa.io/get-pip.py | python**

Depending on the Linux distribution you are using, you may need to install the following dependencies:

* `RabbitMQ Server <https://www.rabbitmq.com>`_, Ubuntu package name: **rabbitmq-server**
* `Redis Server <http://redis.io/>`_, Ubuntu package name: **redis-server**
* header files and a static library for Python, Ubuntu package name: **python-dev**
* Foreign Function Interface library (development files), Ubuntu package name: **libffi-dev**
* Secure Sockets Layer toolkit - development files, Ubuntu package name: **libssl-dev**

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
    /var/run/jasmin         #> Must be owned by jasmin user

.. _installation_linux_steps:

Installation
============

The last step is to install jasmin through `pip <https://pypi.python.org/pypi/pip>`_::

    sudo pip install --pre jasmin

After getting jasmin installed, it is time to start it as a system service::

    sudo wget https://raw.githubusercontent.com/jookies/jasmin/v0.6-beta/misc/config/init-script/jasmind-ubuntu -O /etc/init.d/jasmind
    sudo chmod +x /etc/init.d/jasmind
    sudo update-rc.d jasmind defaults
    sudo invoke-rc.d jasmind start

.. note:: On some Linux distributions, you may use **sudo systemctl enable jasmind** instead of **update-rc.d jasmind defaults**.

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

	http://127.0.0.1:1401/send?username=foo&password=bar&to=98700177&content=hello

Calling the above url from any brower will send an SMS to **98700177** with **hello** content, if you receive a response like the below example it means your SMS is accepted for delivery::

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