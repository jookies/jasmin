#######################
Management CLI overview
#######################

jCli is Jasmin's CLI interface, it is an advanced console to manage and configure everything needed to start messaging
through Jasmin, from users to connectors and message routing management.

jCli is multi-profile configurator where it is possible to create a testing, staging and production profiles to hold
different sets of configurations depending on the desired execution environment.

In order to connect to jCli and start managing Jasmin, the following requirements must be met:

 * You need a jCli admin account
 * You need to have a connection to jCli's tcp port

Jasmin management through jCli is done using different modules (users, groups, filters, smpp connectors, http connectors ...),
these are detailed in :ref:`jCli_Modules`, before going to this part, you have to understand how to:

 * :ref:`Configure <configuration_jcli>` jCli to change it's binding host and port, authentication and logging parameters,
 * :ref:`Authenticate <jCli_1st_Cnx_Authentication>` to jCli and discover basic commands to navigate through the console,
 * :ref:`Know how <jCli_Profiles_And_Persistence>` to persist to disk the current configuration before restarting or load a
   specific configuration profile to run test scenarios for example

.. _architecture:

Architecture
************

The Jasmin CLI interface is designed to be a user interactive interface on front of the Perspective brokers provided by Jasmin.

.. figure:: /resources/management/jcli-architecture.png
   :alt: Jasmin CLI architecture
   :align: center

   Jasmin CLI architecture

In the above figure, every Jasmin CLI module (blue boxes) is connected to its perspective broker, and below you find more details
on the Perspective brokers used and the actions they are exposing:

 * **SMPPClientManagerPB** which provides the following actions:

    #. **persist**: Persist current configuration to disk
    #. **load**: Load configuration from disk
    #. **is_persisted**: Used to check if the current configuration is persisted or not
    #. **connector_add**: Add a SMPP Client connector
    #. **connector_remove**: Remove a SMPP Client connector
    #. **connector_list**: List all SMPP Client connectors
    #. **connector_start**: Start a SMPP Client connector
    #. **connector_stop**: Stop a SMPP Client connector
    #. **connector_stopall**: Stop all SMPP Client connectors
    #. **service_status**: Return a SMPP Client connector service status (running or not)
    #. **session_state**: Return a SMPP Client connector session state (SMPP binding status)
    #. **connector_details**: Get all details for a gived SMPP Client connector
    #. **connector_config**: Returns a SMPP Client connector configuration
    #. **submit_sm**: Send a submit_sm *

 * **RouterPB** which provides the following actions:

    #. **persist**: Persist current configuration to disk
    #. **load**: Load configuration from disk
    #. **is_persisted**: Used to check if the current configuration is persisted or not
    #. **user_add**: Add a new user
    #. **user_authenticate**: Authenticate username/password with the existent users *
    #. **user_remove**: Remove a user
    #. **user_remove_all**: Remove all users
    #. **user_get_all**: Get all users
    #. **user_update_quota**: Update a user quota
    #. **group_add**: Add a group
    #. **group_remove**: Remove a group
    #. **group_remove_all**: Remove all groups
    #. **group_get_all**: Get all groups
    #. **mtroute_add**: Add a new MT route
    #. **moroute_add**: Add a new MO route
    #. **mtroute_remove**: Remove a MT route
    #. **moroute_remove**: Remove a MO route
    #. **mtroute_flush**: Flush MT routes
    #. **moroute_flush**: Flush MO routes
    #. **mtroute_get_all**: Get all MT routes
    #. **moroute_get_all**: Get all MO routes
    #. **mtinterceptor_add**: Add a new MT interceptor
    #. **mointerceptor_add**: Add a new MO interceptor
    #. **mtinterceptor_remove**: Remove a MT interceptor
    #. **mointerceptor_remove**: Remove a MO interceptor
    #. **mtinterceptor_flush**: Flush MT interceptor
    #. **mointerceptor_flush**: Flush MO interceptor
    #. **mtinterceptor_get_all**: Get all MT interceptor
    #. **mointerceptor_get_all**: Get all MO interceptor

.. note:: (*): These actions are not exposed through jCli

.. hint:: **SMPPClientManagerPB** and **RouterPB** are available for third party applications to implement specific business processes, there's a :ref:`FAQ subject including an example <faq_2_HtdatPBA>` of how an external application can use these Perspective Brokers.

.. _configuration_jcli:

Configuration
*************

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contains a **jcli** section where all JCli interface related config elements are:

.. code-block:: ini
   :linenos:

   [jcli]
   bind             = 127.0.0.1
   port             = 8990
   authentication   = True
   admin_username   = jcliadmin
   # MD5 password digest hex encoded
   admin_password   = 79e9b0aa3f3e7c53e916f7ac47439bcb

   log_level        = INFO
   log_file         = /var/log/jasmin/jcli.log
   log_format       = %(asctime)s %(levelname)-8s %(process)d %(message)s
   log_date_format  = %Y-%m-%d %H:%M:%S

.. list-table:: [jcli] configuration section
   :widths: 10 10 80
   :header-rows: 1

   * - Element
     - Default
     - Description
   * - bind
     - 127.0.0.1
     - jCli  will only bind to this specified address.
   * - port
     - 8990
     - The binding TCP port.
   * - authentication
     - True
     - If set to **False**, anonymous user can connect to jCli and admin user account is no more needed
   * - admin_username
     - jcliadmin
     - The admin username
   * - admin_password
     - jclipwd
     - The admin MD5 crypted password
   * - log_*
     -
     - Python's logging module configuration.

.. warning:: Don't set **authentication** to False if you're not sure about what you are doing

.. _jCli_1st_Cnx_Authentication:

First connection & authentication
*********************************

In order to connect to jCli, initiate a telnet session with the hostname/ip and port of jCli as set in
:ref:`configuration_jcli`::

   telnet 127.0.0.1 8990

And depending on whether **authentication** is set to True or False, you may have to authenticate using
the **admin_username** and **admin_password**, here's an example of an authenticated
connection::

   Authentication required.

   Username: jcliadmin
   Password:
   Welcome to Jasmin console
   Type help or ? to list commands.

   Session ref: 2
   jcli :

Once successfully connected, you'll get a welcome message, your session id (Session ref) and a prompt (jcli : )
where you can start typing your commands and use :ref:`jCli_Modules`.

Available commands:
===================

Using tabulation will help you discover the available commands::

   jcli : [TABULATION]
   persist load user group filter mointerceptor mtinterceptor morouter mtrouter smppccm httpccm quit help

Or type **help** and you'll get detailed listing of the available commands with comprehensive descriptions::

   jcli : help
   Available commands:
   ===================
   persist             Persist current configuration profile to disk in PROFILE
   load                Load configuration PROFILE profile from disk
   user                User management
   group               Group management
   filter              Filter management
   mointerceptor       MO Interceptor management
   mtinterceptor       MT Interceptor management
   morouter            MO Router management
   mtrouter            MT Router management
   smppccm             SMPP connector management
   httpccm             HTTP client connector management

   Control commands:
   =================
   quit                Disconnect from console
   help                List available commands with "help" or detailed help with "help cmd".

More detailed help for a specific command can be obtained running **help cmd** where **cmd** is the command
you need help for::

   jcli : help user
   User management
   Usage: user [options]

   Options:
     -l, --list            List all users or a group users when provided with GID
     -a, --add             Add user
     -u UID, --update=UID  Update user using it's UID
     -r UID, --remove=UID  Remove user using it's UID
     -s UID, --show=UID    Show user using it's UID

Interactivity:
==============

When running a command you may enter an interactive session, for example, adding a user with **user -a** will
start an interactive session where you have to indicate the user parameters, the prompt will be changed from
**jcli :** to **>** indicating you are in an interactive session::

   jcli : user -a
   Adding a new User: (ok: save, ko: exit)
   > username foo
   > password bar
   > uid u1
   > gid g1
   > ok
   Successfully added User [u1] to Group [g1]

In the above example, user parameters were **username**, **password**, **uid** and **gid**, note that there's no
order in entering these parameters, and you may use a simple TABULATION to get the parameters you have to enter::

   ...
   > [TABULATION]
   username password gid uid
   ...


.. _jCli_Profiles_And_Persistence:

Profiles and persistence
************************

Everything done using the Jasmin console will be set in runtime memory, and it will remain there until Jasmin is
stopped, that's where persistence is needed to keep the same configuration when restarting.

Persist
=======

Typing **persist** command below will persist runtime configuration to disk using the default profile set in :ref:`configuration_jcli`::

   jcli : persist
   mtrouter configuration persisted (profile:jcli-prod)
   smppcc configuration persisted (profile:jcli-prod)
   group configuration persisted (profile:jcli-prod)
   user configuration persisted (profile:jcli-prod)
   httpcc configuration persisted (profile:jcli-prod)
   mointerceptor configuration persisted (profile:jcli-prod)
   filter configuration persisted (profile:jcli-prod)
   mtinterceptor configuration persisted (profile:jcli-prod)
   morouter configuration persisted (profile:jcli-prod)

It is possible to persist to a defined profile::

   jcli : persist -p testing

.. important:: On Jasmin startup, **jcli-prod** profile is automatically loaded, any other profile can only be manually loaded through **load -p AnyProfile**.

Load
====

Like **persist** command, there's a **load** command which will loaded a configuration profile from disk, typing **load**
command below will load the default profil set in :ref:`configuration_jcli` from disk::

   jcli : load
   mtrouter configuration loaded (profile:jcli-prod)
   smppcc configuration loaded (profile:jcli-prod)
   group configuration loaded (profile:jcli-prod)
   user configuration loaded (profile:jcli-prod)
   httpcc configuration loaded (profile:jcli-prod)
   mointerceptor configuration loaded (profile:jcli-prod)
   filter configuration loaded (profile:jcli-prod)
   mtinterceptor configuration loaded (profile:jcli-prod)
   morouter configuration loaded (profile:jcli-prod)

It is possible to load to a defined profile::

   jcli : load -p testing

.. note:: When loading a profile, any defined current runtime configuration will lost and replaced by this profile configuration
