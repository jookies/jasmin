##################
The message router
##################

The message router is Jasmin's decision making component for routing every type of exchanged message through the gateway:

#. MO Messages (deliver_sm)
#. MT Messages (submit_sm)

The router is provisioned through:

* Perspective broker interface (python programmatic API)
* jCli modules: :ref:`morouter_manager` and :ref:`mtrouter_manager`

Each time a message is requiring a route decision the following process is executed:

Process flow
************

.. figure:: /resources/routing/routing-process.png
   :alt: MO and MT routing process flow
   :align: Center

   Routing process flow

There's one **MORoutingTable** and one **MTRoutingTable** objects holding respective routes for each direction (MT or MO),
these are **Route** objects that hold one or many **Filter** (s) objects and one destination **Connector** (or many connectors
in some specific cases, c.f. :ref:`Route_multiple_con`).

As explained by the above routing process flow figure, for each message and depending on its direction, a routing table is
loaded and an iterative testing is run in order to select a final destination connector or to reject (returning no connector) it,
routes are selected in descendant order, and their respective filter objects are tested against the **Routable** object (It is an
extension of the low-level SMPP PDU object representing a message, more information in :ref:`Route_Routable`).

Examples
========

MO Routing
----------

Having the below MO routing table set through a jCli console session::

   jcli : morouter -l
   #MO Route order   Type                    Connector ID(s)                  Filter(s)
   #30               StaticMORoute           http_3                           <DestinationAddrFilter (dst_addr=^\+33\d+)>
   #20               RandomRoundrobinMORoute http_1, http_2                   <DateIntervalFilter (2015-06-01,2015-08-31)>, <TimeIntervalFilter (08:00:00,18:00:00)>
   #0                DefaultRoute            http_def
   Total MO Routes: 3

The following routing cases are considered:

* MO message is routed to http_3 **if**:

  * Its destination address matches the regular expression "^\+33\d+"

* MO message is routed to http_1 **OR** http_2 **if**:

  * Its received in summer months (June, July and August) of year 2015 and in working hours interval (8pm to 6am)

* MO message is routed to http_def **if**:

  * None of the above routes are matched (fallback / default route)

MT Routing
----------

Having the below MT routing table set through a jCli console session::

   jcli : mtrouter -l
   #MT Route order   Type                    Rate    Connector ID(s)                  Filter(s)
   #100              RandomRoundrobinMTRoute 0.00    smpp_1, smpp_2                   <DestinationAddrFilter (dst_addr=^\+33\d+)>
   #91               StaticMTRoute           0.00    smpp_4                           <GroupFilter (gid=G2)>, <TimeIntervalFilter (08:00:00,18:00:00)>
   #90               StaticMTRoute           0.00    smpp_3                           <GroupFilter (gid=G2)>
   Total MT Routes: 3

The following routing cases are considered:

* MT message is routed to smpp_1 **OR** smpp_2 **if**:

  * Its destination address matches the regular expression "^\+33\d+"

* MT message is routed to smpp_4 **if**:

  * Its sent by a user in group G2 and in working hours interval (8pm to 6am)

* MT message is routed to smpp_3 **if**:

  * Its sent by a user in group G2

.. note:: The route order is very important: if we swap last both routes (#90 and #91) we will run into a shadowing route where all MT messages sent by a user in group G2 will be routed to smpp_3, no matter what time of the day it is.

.. note:: In this example, there's no DefaultRoute, this will lead to message rejection if none of the configured routes are matched.

.. note:: Route's **rate** are discussed in :doc:`/billing/index`.

Router components
*****************

The router components are mainly python objects having the unique responsibility of routing messages to Jasmin connectors.

.. _Route_Routable:

Routable
========

The **Routable** class is extended by child classes to hold necessary information about the message to be *routed*.

.. figure:: /resources/routing/Routable.png
   :alt: jasmin.routing.Routables.*
   :align: Center

   jasmin.routing.Routables.*

The **SimpleRoutablePDU** is only used for Jasmin unit testing, **RoutableSubmitSm** and **RoutableDeliverSm** are used depending
on the message direction:

* MO: **RoutableDeliverSm**
* MT: **RoutableSubmitSm**

All routables provide a tagging api through the *addTag()*, *hasTag()*, *getTags()*, *removeTag()*, *flushTags()* methods,
this feature is mainly used in the :doc:`interceptor </interception/index>`, there's a concrete example of such usage
:ref:`here <sc_hlr_lookup>`.

.. list-table:: **RoutableSubmitSm** attributes
   :widths: 10 10 80
   :header-rows: 1

   * - Attribute
     - Type
     - Description
   * - PDU
     - jasmin.vendor.smpp.pdu.pdu_types.PDURequest
     - The SMPP submit_sm PDU
   * - user
     - jasmin.routing.jasminApi.User
     - Jasmin user sending the message
   * - date_time
     - datetime.datetime
     - Date & time of message send request

.. list-table:: **RoutableDeliverSm** attributes
   :widths: 10 10 80
   :header-rows: 1

   * - Attribute
     - Type
     - Description
   * - PDU
     - jasmin.vendor.smpp.pdu.pdu_types.PDURequest
     - The SMPP deliver_sm PDU
   * - connector
     - jasmin.routing.jasminApi.Connector
     - Jasmin origin connector of the message
   * - date_time
     - datetime.datetime
     - Date & time of message reception

Connector
=========

The **Connector** class is extended by child classes to represent concrete HTTP or SMPP Client connectors.

.. figure:: /resources/routing/Connector.png
   :alt: jasmin.routing.jasminApi.Connector and childs
   :align: Center

   jasmin.routing.jasminApi.Connector and childs

Filter
======

The **Filter** class is extended by child classes to define specific filters which are run by Jasmin router to match a
desired **Routable**, every filter have a public **match(routable)** method returning a boolean value (*True* if the filter
matches the given **Routable**).

As explained, filters provide an advanced and customizable method to match for routables and decide which route to consider, the
figure below shows the **Filter** implementations provided by Jasmin, you can extend the **Filter** class and build a new filter
of your own.

The **usedFor** attribute indicates the filter-route compatibility, as some filters are not suitable for both MO and MT routes like
the examples below:

* **UserFilter** and **GroupFilter**: MO Messages are not identified by a user or a group, they are received through a connector
* **ConnectorFilter**: MT Messages are not coming from a connector, they are sent by a known user/group.

.. figure:: /resources/routing/Filter.png
   :alt: jasmin.routing.Filters.*
   :align: Center

   jasmin.routing.Filters.*

Route
=====

A **Route** class holds one or many filters, the **matchFilters(routable)** method is called to match the given routable against
every filter of the **Route** (using *AND* operation when there's many filters), if the matching succeed, the Jamsin router will ask
for the **Connector** to consider by calling **getConnector()** method which will return back the **Route** 's connector.

.. figure:: /resources/routing/Route.png
   :alt: jasmin.routing.Routes.*
   :align: Center

   jasmin.routing.Routes.*

Static and default routes are the simplest implemented routes, the difference between them is:

* **DefaultRoute** 's **matchFilter()** method will always return True, it is usually a fallback route matching any **Routable**
* **StaticMORoute** and **StaticMTRoute** will return one **Connector** after matching the filters with **matchFilters(routable)** method

There's a lot of things you can do by extending the **Route** class, here's a bunch of possibilities:

* *Best quality routing*: Implement a connector scoring system to always return the best quality route for a given message

.. _Route_multiple_con:

Multiple connectors
-------------------

When extending **Route** class, it is possible to customize the behavior of the route and that's what **RoundrobinMORoute** and **RoundrobinMTRoute**
do, they are initially provisioned with a set of connectors, and the **getConnector()** method is overloaded to return a random connector from it;
this can be a basic usage of a load balancer route.

The newly added (Jasmin 0.9b10+) has new **FailoverMORoute** and **FailoverMTRoute** routes, they are also extending the **Route** class to provide failover
on top of multiple connectors.

RoutingTable
============

The **RoutingTable** class is extended by destination-specific child classes (MO or MT), each class provide a **Route** provisioning api:

* **add(route, order)**: Will add a new route at a given order, will replace an older route having the same order
* **remove(order)**: Will remove the route at the given order
* **getAll()**: Will return all the provisioned routes
* **flush()**: Will remove all provisioned routes

The **getRouteFor(routable)** will get the right route to consider for a given routable, this method will iterate through all the provisioned
routes in descendant order to call their respective **matchFilters(routable)** method.

.. figure:: /resources/routing/RoutingTable.png
   :alt: jasmin.routing.RoutingTables.*
   :align: Center

   jasmin.routing.RoutingTables.*
