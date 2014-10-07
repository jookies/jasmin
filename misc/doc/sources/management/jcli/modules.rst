.. _jCli_Modules:

######################
Management CLI Modules
######################

As shown in the architecture figure :ref:`architecture`, jCli is mainly composed of management modules interfacing two 
Perspective brokers (**SMPPClientManagerPB** and **RouterPB**), each module is identified as a manager of a defined scope:

 * User management
 * Group management
 * etc ..

.. note:: **filter** and **httpccm** modules are not interfacing any Perspective broker, they are facilitating the reuse 
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
 * **mt_messaging_cred** (*optional*): MT Messaging credentials (c.f. :ref:`user_credentials`)

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
   mt_messaging_cred defaultvalue src_addr None
   mt_messaging_cred quota balance ND
   mt_messaging_cred quota sms_count ND
   mt_messaging_cred quota early_percent ND
   mt_messaging_cred valuefilter priority ^[0-3]$
   mt_messaging_cred valuefilter content .*
   mt_messaging_cred valuefilter src_addr .*
   mt_messaging_cred valuefilter dst_addr .*
   mt_messaging_cred authorization dlr_level True
   mt_messaging_cred authorization dlr_method True
   mt_messaging_cred authorization long_content True
   mt_messaging_cred authorization src_addr True
   mt_messaging_cred authorization http_send True
   mt_messaging_cred authorization priority True
   gid marketing
   uid foo

Listing Users will show currently added Users with their UID, GID and Username::

   jcli : user -l
   #User id          Group id         Username         Balance MT SMS
   #foo              1                foo              ND      ND             
   Total Users: 1

.. _user_credentials:

User credentials
================

As seen above, User have an optional **mt_messaging_cred** parameter which define a set of sections:

* **Authorizations**: Privileges to send messages and set some defined parameters,
* **Value filters**: Restrictions on some parameter values (such as source address),
* **Default values**: Default parameter values to be set by Jasmin when not manually set by User,
* **Quotas**: Everything about (c.f. :doc:`/billing/index`),

For each section of the above, there's keys to be defined when adding/updating a user, the example below show how to set a source address value filter and a balance of 44.2::

   jcli : user -a
   Adding a new User: (ok: save, ko: exit)
   > username foo
   > password bar
   > gid marketing
   > uid foo
   > mt_messaging_cred valuefilter src_addr ^JASMIN$
   > mt_messaging_cred quota balance 44.2
   > ok
   Successfully added User [foo] to Group [marketing]

In the below tables, you can find exhaustive list of keys for each mt_messaging_cred section:

.. list-table:: **authorization** section keys
   :widths: 10 10 80
   :header-rows: 1

   * - Key
     - Default
     - Description
   * - http_send
     - True
     - Privilege to send SMS through HTTP API
   * - long_content
     - True
     - Privilege to send long content SMS through HTTP API
   * - dlr_level
     - True
     - Privilege to set **dlr-level** parameter (default is 1)
   * - dlr_method
     - True
     - Privilege to set **dlr-method** HTTP parameter (default is GET)
   * - src_addr
     - True
     - Privilege to defined source address of SMS-MT
   * - priority
     - True
     - Privilege to defined priority of SMS-MT (default is 0)

.. list-table:: **valuefilter** section keys
   :widths: 10 10 80
   :header-rows: 1

   * - Key
     - Default
     - Description
   * - src_addr
     - .*
     - Regex pattern to validate source address of SMS-MT
   * - dst_addr
     - .*
     - Regex pattern to validate destination address of SMS-MT
   * - content
     - .*
     - Regex pattern to validate content of SMS-MT
   * - priority
     - ^[0-3]$
     - Regex pattern to validate priority of SMS-MT

.. list-table:: **defaultvalue** section keys
   :widths: 10 10 80
   :header-rows: 1

   * - Key
     - Default
     - Description
   * - src_addr
     - *None*
     - Default source address of SMS-MT

.. list-table:: **quota** section keys
   :widths: 10 10 80
   :header-rows: 1

   * - Key
     - Default
     - Description
   * - balance
     - ND
     - c.f. :ref:`billing_type_1`
   * - sms_count
     - ND
     - c.f. :ref:`billing_type_2`
   * - early_percent
     - ND
     - c.f. :ref:`billing_async`

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

Check :doc:`/routing/index` for more details about Jasmin's routing.

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

More control commands:

* **morouter -r <order>**: Remove route at defined *order*
* **morouter -f**: Flush MORoutingTable (unrecoverable)

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

MT Router helps managing Jasmin's MTRoutingTable, which is responsible of providing routes to outgoing SMS MT, here the basics of Jasmin MT routing mechanism:

 #. **MTRoutingTable** holds ordered **MTRoute** objects (each MTRoute has a unique order)
 #. A **MTRoute** is composed of:

     * **Filters**: One or many filters (c.f. :ref:`filter_manager`)
     * **Connector**: One connector (can be *many* in some situations)
     * **Rate**: For billing purpose, the rate of sending one message through this route; it can be zero
       to mark the route as FREE (NOT RATED) (c.f. :doc:`/billing/index`)

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

Check :doc:`/routing/index` for more details about Jasmin's routing.

When adding a MT Route, the following parameters are required:

 * **type**: One of the supported MT Routes: DefaultRoute, StaticMTRoute, RandomRoundrobinMTRoute
 * **order**: MO Route order
 * **rate**: The route rate, can be zero

When choosing the MT Route **type**, additionnal parameters may be added to the above required parameters.

Here's an example of adding a **DefaultRoute** to a SMPP Client Connector (smppcc_default)::

   jcli : mtrouter -a
   Adding a new MT Route: (ok: save, ko: exit)
   > type DefaultRoute
   jasmin.routing.Routes.DefaultRoute arguments:
   connector
   > connector smppcc_default
   > rate 0.0
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
   > rate 0.0
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
   > rate 0.0
   > ok
   Successfully added MTRoute [RandomRoundrobinMTRoute] with order:20

Once the above MT Routes are added to **MTRoutingTable**, it is possible to list these routes::

   jcli : mtrouter -l
   #MT Route order   Type                    Rate    Connector ID(s)     Filter(s)
   #20               RandomRoundrobinMTRoute 0.00    smppcc_2, smppcc_3  <TransparentFilter>
   #10               StaticMTRoute           0.00    smppcc_1            <TransparentFilter>, <TransparentFilter>
   #0                DefaultRoute            0.00    smppcc_default
   Total MT Routes: 3

.. note:: Filters and Connectors were created before creating these routes, please check :ref:`filter_manager` and 
         :ref:`httpccm_manager` for further details

It is possible to obtain more information of a defined route by typing **mtroute -s <order>**::

   jcli : mtrouter -s 20
   RandomRoundrobinMTRoute to 2 connectors:
      - smppcc_2
      - smppcc_3
   NOT RATED
   
   jcli : mtrouter -s 10
   StaticMTRoute to cid:smppcc_1 NOT RATED
   
   jcli : mtrouter -s 0
   DefaultRoute to cid:smppcc_default NOT RATED

More control commands:

* **mtrouter -r <order>**: Remove route at defined *order*
* **mtrouter -f**: Flush MTRoutingTable (unrecoverable)

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

A SMPP Client connector is used to send/receive SMS through SMPP v3.4 protocol, it is directly connected to MO and MT routers to 
provide end-to-end message delivery.

Adding a new SMPP Client connector requires knowledge of the parameters detailed in the listing below:

.. _smppcc_params:

.. list-table:: SMPP Client connector parameters
   :widths: 10 80 10
   :header-rows: 1

   * - Parameter
     - Description
     - Default
   * - **cid**
     - Connector ID (must be unique)
     - 
   * - **logfile**
     - 
     - /var/log/jasmin/default-**<cid>**.log
   * - **loglevel**
     - Logging numeric level: 10=DEBUG, 20=INFO, 30=WARNING, 40=ERROR, 50=CRITICCAL 
     - 20
   * - **host**
     - Server that runs SMSC
     - 127.0.0.1
   * - **port**
     - The port number for the connection to the SMSC.
     - 2775
   * - **username**
     - 
     - smppclient
   * - **password**
     - 
     - password
   * - **bind**
     - Bind type: transceiver, receiver or transmitter
     - transceiver
   * - **bind_to**
     - Timeout for response to bind request
     - 30
   * - **trx_to**
     - Maximum time lapse allowed between transactions, after which, the connection is considered as inactive and will reconnect
     - 300
   * - **res_to**
     - Timeout for responses to any request PDU
     - 60
   * - **pdu_red_to**
     - Timeout for reading a single PDU, this is the maximum lapse of time between receiving PDU's header and its complete read, if the PDU reading timed out, the connection is considered as 'corrupt' and will reconnect
     - 10
   * - **con_loss_retry**
     - Reconnect on connection loss ? (yes, no)
     - yes
   * - **con_loss_delay**
     - Reconnect delay on connection loss (seconds)
     - 10
   * - **con_fail_retry**
     - Reconnect on connection failure ? (yes, no)
     - yes
   * - **con_fail_delay**
     - Reconnect delay on connection failure (seconds)
     - 10
   * - **src_addr**
     - Default source adress of each SMS-MT if not set while sending it, can be numeric or alphanumeric, when not defined it will take SMSC default
     - *Not defined*
   * - **src_ton**
     - Source address TON setting for the link: 0=Unknown, 1=International, 2=National, 3=Network specific, 4=Subscriber number, 5=Alphanumeric, 6=Abbreviated
     - 2
   * - **src_npi**
     - Source address NPI setting for the link: 0=Unknown, 1=ISDN, 3=Data, 4=Telex, 6=Land mobile, 8=National, 9=Private, 10=Ermes, 14=Internet, 18=WAP Client ID
     - 1
   * - **dst_ton**
     - Destination address TON setting for the link: 0=Unknown, 1=International, 2=National, 3=Network specific, 4=Subscriber number, 5=Alphanumeric, 6=Abbreviated
     - 1
   * - **dst_npi**
     - Destination address NPI setting for the link: 0=Unknown, 1=ISDN, 3=Data, 4=Telex, 6=Land mobile, 8=National, 9=Private, 10=Ermes, 14=Internet, 18=WAP Client ID
     - 1
   * - **bind_ton**
     - Bind address TON setting for the link: 0=Unknown, 1=International, 2=National, 3=Network specific, 4=Subscriber number, 5=Alphanumeric, 6=Abbreviated
     - 0
   * - **bind_npi**
     - Bind address NPI setting for the link: 0=Unknown, 1=ISDN, 3=Data, 4=Telex, 6=Land mobile, 8=National, 9=Private, 10=Ermes, 14=Internet, 18=WAP Client ID
     - 1
   * - **validity**
     - Default validity period of each SMS-MT if not set while sending it, when not defined it will take SMSC default (seconds)
     - *Not defined*
   * - **priority**
     - SMS-MT default priority if not set while sending it: 0, 1, 2 or 3
     - 0
   * - **requeue_delay**
     - Delay to be considered when requeuing a rejected message
     - 120
   * - **addr_range**
     - Indicates which MS's can send messages to this connector, seems to be an informative value
     - *Not defined*
   * - **systype**
     - The system_type parameter is used to categorize the type of ESME that is binding to the SMSC. Examples include “VMS” (voice mail system) and “OTA” (over-the-air activation system).
     - *Not defined*
   * - **dlr_expiry**
     - When a SMS-MT is not acked, it will remain waiting in memory for *dlr_expiry* seconds, after this period, any received ACK will be ignored
     - 86400
   * - **submit_throughput**
     - Active SMS-MT throttling in MPS (Messages per second)
     - 1
   * - **proto_id**
     - Used to indicate protocol id in SMS-MT and SMS-MO
     - *Not defined*
   * - **coding**
     - Default coding of each SMS-MT if not set while sending it: 0=SMSC Default, 1=IA5 ASCII, 2=Octet unspecified, 3=Latin1, 4=Octet unspecified common, 5=JIS, 6=Cyrillic, 7=ISO-8859-8, 8=UCS2, 9=Pictogram, 10=ISO-2022-JP, 13=Extended Kanji Jis, 14=KS C 5601
     - 0
   * - **elink_interval**
     - Enquire link interval (seconds)
     - 10
   * - **def_msg_id**
     - Specifies the SMSC index of a pre-defined ('canned') message.
     - 0
   * - **ripf**
     - Replace if present flag: 0=Do not replace, 1=Replace
     - 0

.. note:: When adding a SMPP Client connector, only it's **cid** is required, all the other parameters will 
         be set to their respective defaults.

.. note:: Connector restart is required only when changing the following parameters: **host**, **port**, **username**, 
         **password**, **systemType**, **logfile**, **loglevel**; any other change is applied without requiring connector 
         to be restarted.

Here’s an example of adding a new **transmitter** SMPP Client connector with **cid=Demo**::

   jcli : smppccm -a
   Adding a new connector: (ok: save, ko: exit)
   > cid Demo
   > bind transmitter
   > ok
   Successfully added connector [Demo]

All the above parameters can be displayed after connector creation::

   jcli : smppccm -s Demo
   ripf 0
   con_fail_delay 10
   dlr_expiry 86400
   coding 0
   submit_throughput 1
   elink_interval 10
   bind_to 30
   port 2775
   con_fail_retry 1
   password password
   src_addr None
   bind_npi 1
   addr_range None
   dst_ton 1
   res_to 60
   def_msg_id 0
   priority 0
   con_loss_retry 1
   username smppclient
   dst_npi 1
   validity None
   requeue_delay 120
   host 127.0.0.1
   src_npi 1
   trx_to 300
   logfile /var/log/jasmin/default-Demo.log
   systype 
   cid Demo
   loglevel 20
   bind transmitter
   proto_id None
   con_loss_delay 10
   bind_ton 0
   pdu_red_to 10
   src_ton 2   

.. note:: From the example above, you can see that showing a connector details will return all it's parameters 
          even those you did not enter while creating/updating the connector, they will take their respective 
          default values as explained in :ref:`smppcc_params`

Listing connectors will show currently added SMPP Client connectors with their CID, Service/Session state and 
start/stop counters::

   jcli : smppccm -l
   #Connector id                        Service Session          Starts Stops
   #888                                 stopped None             0      0    
   #Demo                                stopped None             0      0    
   Total connectors: 2

Updating an existent connector is the same as creating a new one, simply type **smppccm -u <cid>** where **cid** 
is the connector id you want to update, you'll run into a new interactive session to enter the parameters you 
want to update (c.f. :ref:`smppcc_params`).

Here’s an example of updating SMPP Client connector's host::

   jcli : smppccm -u Demo
   Updating connector id [Demo]: (ok: save, ko: exit)
   > host 10.10.1.2
   > ok
   Successfully updated connector [Demo]

More control commands:

* **smppccm -1 <cid>**: Start connector and try to connect
* **smppccm -0 <cid>**: Stop connector and disconnect
* **smppccm -r <cid>**: Remove connector (unrecoverable)

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

Filters are used by MO/MT routers to help decide on which route a message must be delivered, the following 
flowchart provides details of the routing process:

.. figure:: /resources/routing/routing-process.png
   :alt: MO and MT routing process flow
   :align: Center
   
   Routing process flow

Jasmin provides many Filters offering advanced flexibilities to message routing:
 
.. list-table:: Jasmin Filters
   :widths: 10 10 80
   :header-rows: 1

   * - Name
     - Routes
     - Description
   * - **TransparentFilter**
     - All
     - This filter will always match any message criteria
   * - **ConnectorFilter**
     - MO
     - Will match the source connector of a message
   * - **UserFilter**
     - MT
     - Will match the owner of a MT message
   * - **GroupFilter**
     - MT
     - Will match the owner's group of a MT message
   * - **SourceAddrFilter**
     - MO
     - Will match the source address of a MO message
   * - **DestinationAddrFilter**
     - All
     - Will match the source address of a message
   * - **ShortMessageFilter**
     - All
     - Will match the content of a message
   * - **DateIntervalFilter**
     - All
     - Will match the date of a message
   * - **TimeIntervalFilter**
     - All
     - Will match the time of a message
   * - **EvalPyFilter**
     - All
     - Will pass the message to a third party python script for user-defined filtering

Check :doc:`/routing/index` for more details about Jasmin's routing.

When adding a Filter, the following parameters are required:

 * **type**: One of the supported Filters: TransparentFilter, ConnectorFilter, UserFilter, GroupFilter, SourceAddrFilter, 
   DestinationAddrFilter, ShortMessageFilter, DateIntervalFilter, TimeIntervalFilter, EvalPyFilter
 * **fid**: Filter id (must be unique)

When choosing the Filter **type**, additionnal parameters may be added to the above required parameters:

.. list-table:: Filters parameters
   :widths: 10 10 80
   :header-rows: 1

   * - Name
     - Example
     - Parameters
   * - **TransparentFilter**
     - 
     - No parameters are required
   * - **ConnectorFilter**
     - smpp-01
     - **cid** of the connector to match
   * - **UserFilter**
     - bobo
     - **uid** of the user to match
   * - **GroupFilter**
     - partners
     - **gid** of the group to match
   * - **SourceAddrFilter**
     - ^20\d+
     - **source_addr**: Regular expression to match source address
   * - **DestinationAddrFilter**
     - ^85111$
     - **destination_addr**: Regular expression to match destination address
   * - **ShortMessageFilter**
     - ^hello.*$
     - **short_message**: Regular expression to match message content
   * - **DateIntervalFilter**
     - 2014-09-18;2014-09-28
     - **dateInterval**: Two dates separated by ; (date format is YYYY-MM-DD)
   * - **TimeIntervalFilter**
     - 08:00:00;18:00:00
     - **timeInterval**: Two timestamps separated by ; (timestamp format is HH:MM:SS)
   * - **EvalPyFilter**
     - /root/thirdparty.py
     - **pyCode**: Path to a python script, (:ref:`external_buslogig_filters` for more details)

Here's an example of adding a **TransparentFilter** ::

   jcli : filter -a
   Adding a new Filter: (ok: save, ko: exit)
   type fid
   > type transparentfilter
   > fid TF
   > ok
   Successfully added Filter [TransparentFilter] with fid:TF

Here's an example of adding a **SourceAddrFilter** ::

   jcli : filter -a
   Adding a new Filter: (ok: save, ko: exit)
   > type sourceaddrfilter
   jasmin.routing.Filters.SourceAddrFilter arguments:
   source_addr
   > source_addr ^20\d+
   > ok
   You must set these options before saving: type, fid, source_addr
   > fid From20*
   > ok
   Successfully added Filter [SourceAddrFilter] with fid:From20*

Here's an example of adding a **TimeIntervalFilter** ::

   jcli : filter -a
   Adding a new Filter: (ok: save, ko: exit)
   > fid WorkingHours
   > type timeintervalfilter
   jasmin.routing.Filters.TimeIntervalFilter arguments:
   timeInterval
   > timeInterval 08:00:00;18:00:00
   > ok
   Successfully added Filter [TimeIntervalFilter] with fid:WorkingHours

It is possible to list filters with::

   jcli : filter -l
   #Filter id        Type                   Routes Description                     
   #StartWithHello   ShortMessageFilter     MO MT  <ShortMessageFilter (msg=^hello.*$)>
   #ExternalPy       EvalPyFilter           MO MT  <EvalPyFilter (pyCode= ..)>     
   #To85111          DestinationAddrFilter  MO MT  <DestinationAddrFilter (dst_addr=^85111$)>
   #September2014    DateIntervalFilter     MO MT  <DateIntervalFilter (2014-09-01,2014-09-30)>
   #WorkingHours     TimeIntervalFilter     MO MT  <TimeIntervalFilter (08:00:00,18:00:00)>
   #TF               TransparentFilter      MO MT  <TransparentFilter>             
   #From20*          SourceAddrFilter       MO     <SourceAddrFilter (src_addr=^20\d+)>
   Total Filters: 7

It is possible to obtain more information of a specific filter by typing **filter -s <fid>**::

   jcli : filter -s September2014
   DateIntervalFilter:
   Left border = 2014-09-01
   Right border = 2014-09-30

More control commands:

* **filter -r <fid>**: Remove filter

.. _external_buslogig_filters:

External business logic
=======================

In addition to predefined filters listed above (:ref:`filter_manager`), it is possible to extend 
filtering with external scripts written in Python using the **EvalPyFilter**.

Here's a very simple example where an **EvalPyFilter** is matching the connector **cid** of a message:

**First, write an external python script**:

.. code-block:: python

   # File @ /opt/jasmin-scripts/routing/abc-connector.py
   if routable.connector.cid == 'abc':
       result = True
   else:
       result = False

**Second, create an EvalPyFilter with the python script**::

   jcli : filter -a
   Adding a new Filter: (ok: save, ko: exit)
   > type EvalPyFilter
   jasmin.routing.Filters.EvalPyFilter arguments:
   pyCode
   > pyCode /opt/jasmin-scripts/routing/abc-connector.py
   > fid SimpleThirdParty
   > ok
   Successfully added Filter [EvalPyFilter] with fid:SimpleThirdParty

This example will provide an **EvalPyFilter** (SimpleThirdParty) that will match any message coming from 
the connector with **cid** = abc.

Using **EvalPyFilter** is as simple as the shown example, when the python script is called it will get the 
following global variables set:

* **routable**: one of the *jasmin.routing.Routables.Routable* inheriters (:ref:`Route_Routable` for more details)
* **result**: (default to *False*) It will be read by Jasmin router at the end of the script execution to check
  if the filter is matching the message passed through the routable variable, matched=True / unmatched=False

.. note:: It is possible to check for any parameter of the SMPP PDU: TON, NPI, PROTOCOL_ID ... since it is provided through 
          the **routable** object.
.. note:: Using **EvalPyFilter** offers the possibility to call external webservices, databases ... for powerfull 
          routing or even for logging, rating & billing through external third party systems.

.. _httpccm_manager:

HTTP Client connector manager
*****************************

The HTTP Client connector manager module is accessible through the **httpccm** command and is providing the 
following features:

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

A HTTP Client connector is used in SMS-MO routing, it is called with the message parameters when it is returned 
by a matched MO Route (:ref:`receiving_sms-mo` for more details).

When adding a HTTP Client connector, the following parameters are required:

 * **cid**: Connector id (must be unique)
 * **url**: URL to be called with message parameters
 * **method**: Calling method (GET or POST)

Here's an example of adding a new HTTP Client connector::

   jcli : httpccm -a
   Adding a new Httpcc: (ok: save, ko: exit)
   > url http://10.10.20.125/receive-sms/mo.php
   > method GET
   > cid HTTP-01
   > ok
   Successfully added Httpcc [HttpConnector] with cid:HTTP-01

All the above parameters can be displayed after Connector creation::

   jcli : httpccm -s HTTP-01
   HttpConnector:
   cid = HTTP-01
   baseurl = http://10.10.20.125/receive-sms/mo.php
   method = GET

Listing Connectors will show currently added Connectors with their CID, Type, Method and Url::

   jcli : httpccm -l
   #Httpcc id        Type                   Method URL
   #HTTP-01          HttpConnector          GET    http://10.10.20.125/receive-sms/mo.php
   Total Httpccs: 1