.. _jCli_Modules:

######################
Management CLI Modules
######################

As shown in the architecture figure :ref:`architecture`, jCli is mainly composed of management modules interfacing two 
Perspective brokers (**SMPPClientManagerPB** and **RouterPB**), each module is identified as a manager of defined scope:

 * User management
 * Group management
 * etc ..

.. note:: **filter** and **httpccm** modules are not interfacing any Perspective broker, they are facilitate the reuse 
          of created filters and HTTP Client connectors in MO and MT routers, e.g. a HTTP Client connector may be created 
          once and used many times in MO Routes.

.. _user_manager:

User manager
************

The User manager module is accessible through the **user** command and is providing the following features:

.. list-table:: **user** command line options
   :widths: 10 90
   :header-rows: 1

   * - Command
     - Description
   * - -l, --list
     - List all users or a group users when provided with GID
   * - -a, --add
     - Add user
   * - -u UID, --update=UID
     - Update user using it's UID
   * - -r UID, --remove=UID
     - Remove user using it's UID
   * - -s UID, --show=UID
     - Show user using it's UID

A User object is required for:

 * HTTP API authentication to send a SMS (c.f. :ref:`sending_sms-mt`)
 * Creating a **UserFilter** using the **filter** manager (c.f. :ref:`filter_manager`)

Every User **must** be a member of a Group, so before adding a new User, there must be at least one Group 
available, Groups are identified by *GID* (Group ID).

When adding a User, the following parameters are required:

 * **username**: A unique username used for authentication
 * **password**
 * **uid**: A unique identifier, can be same as **username**
 * **gid**: Group Identfier

Here's an example of adding a new User to the **marketing** group::

   jcli : user -a
   Adding a new User: (ok: save, ko: exit)
   > username foo
   > password bar
   > gid marketing
   > uid foo
   > ok
   Successfully added User [foo] to Group [marketing]

All the above parameters can be displayed after User creation, except the password::

   jcli : user -s foo
   username foo
   gid marketing
   uid foo

Listing Users will show currently added Users with their UID, GID and Username::

   jcli : user -l
   #User id          Group id         Username        
   #foo              marketing        foo             
   Total Users: 1

.. _group_manager:

Group manager
*************

The Group manager module is accessible through the **group** command and is providing the following features:

.. list-table:: **group** command line options
   :widths: 10 90
   :header-rows: 1

   * - Command
     - Description
   * - -l, --list
     - List groups
   * - -a, --add
     - Add group
   * - -r GID, --remove=GID
     - Remove group using it's GID

A Group object is required for:

 * Creating a **User** using the **user** manager (c.f. :ref:`user_manager`)
 * Creating a **GroupFilter** using the **filter** manager (c.f. :ref:`filter_manager`)

When adding a Group, only one parameter is required:

 * **gid**: Group Identfier

Here's an example of adding a new Group::

   jcli : group -a
   Adding a new Group: (ok: save, ko: exit)
   > gid marketing
   > ok
   Successfully added Group [marketing]

Listing Groups will show currently added Groups with their GID::

   jcli : group  -l
   #Group id        
   #marketing       
   Total Groups: 1

.. _morouter_manager:

MO router manager
*****************

The MO Router manager module is accessible through the **morouter** command and is providing the following features:

.. list-table:: **morouter** command line options
   :widths: 10 90
   :header-rows: 1

   * - Command
     - Description
   * - -l, --list
     - List MO routes
   * - -a, --add
     - Add a new MO route
   * - -r ORDER, --remove=ORDER
     - Remove MO route using it's ORDER
   * - -s ORDER, --show=ORDER
     - Show MO route using it's ORDER
   * - -f, --flush
     - Flush MO routing table

MO Router helps managing Jasmin's MORoutingTable, which is responsible of providing routes to received 
SMS MO, here the basics of Jasmin MO routing mechanism:

 #. **MORoutingTable** holds ordered **MORoute** objects (each MORoute has a unique order)
 #. A **MORoute** is composed of:

     * **Filters**: One or many filters (c.f. :ref:`filter_manager`)
     * **Connector**: One connector (can be *many* in some situations)

 #. There's many objects inheriting **MORoute** to provide flexible ways to route messages:

     * **DefaultRoute**: A route without a filter, this one can only set with the lowest order to be a 
       default/fallback route
     * **StaticMORoute**: A basic route with **Filters** and one **Connector**
     * **RandomRoundrobinMORoute**: A route with **Filters** and many **Connectors**, will return a random 
       **Connector** if its **Filters** are validated, can be used as a load balancer route

 #. When a SMS MO is received, Jasmin will ask for the right **MORoute** to consider, all routes are checked
    in descendant order for their respective **Filters** (when a **MORoute** have many filters, they are checked 
    with an **AND** boolean operator)
 #. When a **MORoute** is considered (its **Filters** are validated against a received SMS MO), Jasmin will use 
    its **Connector** to send the SMS MO.

When adding a MO Route, the following parameters are required:

 * **type**: One of the supported MO Routes: DefaultRoute, StaticMORoute, RandomRoundrobinMORoute
 * **order**: MO Route order

When choosing the MO Route **type**, additionnal parameters may be added to the above required parameters.

Here's an example of adding a **DefaultRoute** to a HTTP Client Connector (http_default)::

   jcli : morouter -a
   Adding a new MO Route: (ok: save, ko: exit)
   > type DefaultRoute
   jasmin.routing.Routes.DefaultRoute arguments:
   connector
   > connector http_default
   > ok
   Successfully added MORoute [DefaultRoute] with order:0

.. note:: You don't have to set **order** parameter when the MO Route type is **DefaultRoute**, it will be automatically
         set to 0

Here's an example of adding a **StaticMORoute** to a HTTP Client Connector (http_1)::

   jcli : morouter -a
   Adding a new MO Route: (ok: save, ko: exit)
   > type StaticMORoute
   jasmin.routing.Routes.StaticMORoute arguments:
   filters, connector
   > order 10
   > filters filter_1;filter_2
   > connector http_1
   > ok
   Successfully added MORoute [StaticMORoute] with order:10

Here's an example of adding a **RandomRoundrobinMORoute** to two HTTP Client Connectors (http_2 and http_3)::

   jcli : morouter -a
   Adding a new MO Route: (ok: save, ko: exit)
   > type RandomRoundrobinMORoute
   jasmin.routing.Routes.RandomRoundrobinMORoute arguments:
   filters, connectors
   > filters filter_3
   > connectors http_2;http_3
   > order 20
   > ok
   Successfully added MORoute [RandomRoundrobinMORoute] with order:20

Once the above MO Routes are added to **MORoutingTable**, it is possible to list these routes::

   jcli : morouter -l
   #MO Route order   Type                    Connector ID(s)  Filter(s)
   #20               RandomRoundrobinMORoute http_2, http_3   <TransparentFilter>
   #10               StaticMORoute           http_1           <TransparentFilter>, <TransparentFilter>
   #0                DefaultRoute            http_default
   Total MO Routes: 3

.. note:: Filters and Connectors were created before creating these routes, please check :ref:`filter_manager` and 
         :ref:`httpccm_manager` for further details

It is possible to obtain more information of a defined route by typing **moroute -s <order>**::

   jcli : morouter -s 20
   RandomRoundrobinMORoute to 2 connectors:
      - http_2
      - http_3
   
   jcli : morouter -s 10
   StaticMORoute to cid:http_1
   
   jcli : morouter -s 0
   DefaultRoute to cid:http_default

.. _mtrouter_manager:

MT router manager
*****************

The MT Router manager module is accessible through the **mtrouter** command and is providing the following features:

.. list-table:: **mtrouter** command line options
   :widths: 10 90
   :header-rows: 1

   * - Command
     - Description
   * - -l, --list
     - List MT routes
   * - -a, --add
     - Add a new MT route
   * - -r ORDER, --remove=ORDER
     - Remove MT route using it's ORDER
   * - -s ORDER, --show=ORDER
     - Show MT route using it's ORDER
   * - -f, --flush
     - Flush MT routing table

MT Router helps managing Jasmin's MTRoutingTable, which is responsible of providing routes to outgoing 
SMS MT, here the basics of Jasmin MT routing mechanism:

 #. **MTRoutingTable** holds ordered **MTRoute** objects (each MTRoute has a unique order)
 #. A **MTRoute** is composed of:

     * **Filters**: One or many filters (c.f. :ref:`filter_manager`)
     * **Connector**: One connector (can be *many* in some situations)

 #. There's many objects inheriting **MTRoute** to provide flexible ways to route messages:

     * **DefaultRoute**: A route without a filter, this one can only set with the lowest order to be a 
       default/fallback route
     * **StaticMTRoute**: A basic route with **Filters** and one **Connector**
     * **RandomRoundrobinMTRoute**: A route with **Filters** and many **Connectors**, will return a random 
       **Connector** if its **Filters** are validated, can be used as a load balancer route

 #. When a SMS MT is to be sent, Jasmin will ask for the right **MTRoute** to consider, all routes are checked
    in descendant order for their respective **Filters** (when a **MTRoute** have many filters, they are checked 
    with an **AND** boolean operator)
 #. When a **MTRoute** is considered (its **Filters** are validated against an outgoing SMS MT), Jasmin will use 
    its **Connector** to send the SMS MT.

When adding a MT Route, the following parameters are required:

 * **type**: One of the supported MT Routes: DefaultRoute, StaticMTRoute, RandomRoundrobinMTRoute
 * **order**: MO Route order

When choosing the MT Route **type**, additionnal parameters may be added to the above required parameters.

Here's an example of adding a **DefaultRoute** to a SMPP Client Connector (smppcc_default)::

   jcli : mtrouter -a
   Adding a new MT Route: (ok: save, ko: exit)
   > type DefaultRoute
   jasmin.routing.Routes.DefaultRoute arguments:
   connector
   > connector smppcc_default
   > ok
   Successfully added MTRoute [DefaultRoute] with order:0

.. note:: You don't have to set **order** parameter when the MT Route type is **DefaultRoute**, it will be automatically
         set to 0

Here's an example of adding a **StaticMTRoute** to a SMPP Client Connector (smppcc_1)::

   jcli : mtrouter -a
   Adding a new MT Route: (ok: save, ko: exit)
   > type StaticMTRoute
   jasmin.routing.Routes.StaticMTRoute arguments:
   filters, connector
   > filters filter_1;filter_2
   > order 10
   > connector smppcc_1
   > ok
   Successfully added MTRoute [StaticMTRoute] with order:10

Here's an example of adding a **RandomRoundrobinMTRoute** to two SMPP Client Connectors (smppcc_2 and smppcc_3)::

   jcli : mtrouter -a
   Adding a new MT Route: (ok: save, ko: exit)
   > order 20
   > type RandomRoundrobinMTRoute
   jasmin.routing.Routes.RandomRoundrobinMTRoute arguments:
   filters, connectors
   > filters filter_3
   > connectors smppcc_2;smppcc_3
   > ok
   Successfully added MTRoute [RandomRoundrobinMTRoute] with order:20

Once the above MT Routes are added to **MTRoutingTable**, it is possible to list these routes::

   jcli : mtrouter -l
   #MT Route order   Type                    Connector ID(s)     Filter(s)
   #20               RandomRoundrobinMTRoute smppcc_2, smppcc_3  <TransparentFilter>
   #10               StaticMTRoute           smppcc_1            <TransparentFilter>, <TransparentFilter>
   #0                DefaultRoute            smppcc_default
   Total MT Routes: 3

.. note:: Filters and Connectors were created before creating these routes, please check :ref:`filter_manager` and 
         :ref:`httpccm_manager` for further details

It is possible to obtain more information of a defined route by typing **mtroute -s <order>**::

   jcli : mtrouter -s 20
   RandomRoundrobinMTRoute to 2 connectors:
      - smppcc_2
      - smppcc_3
   
   jcli : mtrouter -s 10
   StaticMTRoute to cid:smppcc_1
   
   jcli : mtrouter -s 0
   DefaultRoute to cid:smppcc_default

.. _smppccm_manager:

SMPP Client connector manager
*****************************

The SMPP Client connector manager module is accessible through the **smppccm** command and is providing the following features:

.. list-table:: **smppccm** command line options
   :widths: 10 90
   :header-rows: 1

   * - Command
     - Description
   * - -l, --list
     - List SMPP connectors
   * - -a, --add
     - Add SMPP connector
   * - -u CID, --update=CID
     - Update SMPP connector configuration using it's CID
   * - -r CID, --remove=CID
     - Remove SMPP connector using it's CID
   * - -s CID, --show=CID
     - Show SMPP connector using it's CID
   * - -1 CID, --start=CID
     - Start SMPP connector using it's CID
   * - -0 CID, --stop=CID
     - Start SMPP connector using it's CID

.. _filter_manager:

Filter manager
**************

The Filter manager module is accessible through the **filter** command and is providing the following features:

.. list-table:: **filter** command line options
   :widths: 10 90
   :header-rows: 1

   * - Command
     - Description
   * - -l, --list
     - List filters
   * - -a, --add
     - Add filter
   * - -r FID, --remove=FID
     - Remove filter using it's FID
   * - -s FID, --show=FID
     - Show filter using it's FID

.. _httpccm_manager:

HTTP Client connector manager
*****************************

The HTTP Client connector manager module is accessible through the **httpccm** command and is providing the following features:

.. list-table:: **httpccm** command line options
   :widths: 10 90
   :header-rows: 1

   * - Command
     - Description
   * - -l, --list
     - List HTTP client connectors
   * - -a, --add
     - Add a new HTTP client connector
   * - -r FID, --remove=FID
     - Remove HTTP client connector using it's CID
   * - -s FID, --show=FID
     - Show HTTP client connector using it's CID