##############
Management CLI
##############

jCli is Jasmin's CLI interface, it is an advanced console to manage and configure everything needed to start messaging 
through Jasmin, from user to connector and message routing management.

jCli is multi-profile configurator where it is possible to create a testing, staging and production profiles to hold 
different set of configurations depending on the desired execution environment.

Introduction
============
jCli is accessible using telnet client, in order to connect to jCli and start managing Jasmin, the following requirements 
must be met:

 * You need a jCli admin account
 * You need to have a connection to jCli's tcp port

Jasmin management through jCli is done using different modules (user, group, filters, smpp connectors, http connectors ...), 
these are detailed in :ref:`jCli_Modules`, before going to this part, you have to understand how to:

 * :ref:`configuration_jcli`: Configure jCli to be able to change it's binding host and port, authentication and logging parameters
 * :ref:`jCli_1st_Cnx_Authentication`: Authenticate to jCli and discover basic commands to navigate through the console
 * :ref:`jCli_Profiles_And_Persistence`: Know how to persist to disk the current configuration before restarting or load a 
   specific configuration profile to run test scenarios fox example

.. _configuration_jcli:

Configuration
=============

The **jasmin.cfg** file *(INI format, located in /etc/jasmin)* contain a section called **jcli** where all JCli interface related config elements are:

.. code-block:: ini
   :linenos:
   
   [jcli]
   load_profile     = jcli-prod
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
   * - load_profile
     - jcli-prod
     - Sets the profile name to be loaded on Jasmin startup.
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

.. _jCli_1st_Cnx_Authentication:

First connection & authentication
=================================

In order to connect to jCli, initiate a telnet session with the hostname/ip and port of jCli as set in 
:ref:`configuration_jcli`::

   telnet 127.0.0.1 8990

And depending on whether **authentication** is set to True or False in :ref:`configuration_jcli`, you may 
have to authenticate using the **admin_username** and **admin_password**, here's an example of an authenticated 
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
-------------------

Using tabulation will help you discover the available commands::

   persist load user group filter morouter mtrouter smppccm httpccm quit help

Or type **help** and you'll get detailed listing of the available commands with a description for each one::

   jcli : help
   Available commands:
   ===================
   persist             Persist current configuration profile to disk in PROFILE
   load                Load configuration PROFILE profile from disk
   user                User management
   group               Group management
   filter              Filter management
   morouter            MO Router management
   mtrouter            MT Router management
   smppccm             SMPP connector management
   httpccm             HTTP client connector management
   
   Control commands:
   =================
   quit                Disconnect from console
   help                List available commands with "help" or detailed help with "help cmd".

More detailed help for a specific command can be obtained running **help cmd** where **cmd** is the command 
you need help for, example::

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
--------------

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
========================

.. _jCli_Modules:

jCli Modules
============