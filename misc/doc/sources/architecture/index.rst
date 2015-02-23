#####################
Architecture overview
#####################

`Jasmin <http://jasminsms.com/>`_ is composed of several components with scoped responsibilities:

.. figure:: /resources/architecture/hld.png
   :alt: HLD Architecture
   :align: Center
   
   Jasmin SMS Gateway high level design

#. **jCli**: Telnet management console, refer to :doc:`/management/jcli/index` for more details,
#. **SMPP Client Manager PB**: A `PerspectBroker <http://twisted.readthedocs.org/en/latest/core/howto/pb-intro.html>`_ 
   providing facilities to manage (add, remove, list, start, stop ...) SMPP client connectors,
#. **Router**: A `PerspectBroker <http://twisted.readthedocs.org/en/latest/core/howto/pb-intro.html>`_ 
   providing facilities to manage message routes, groups, users, http connectors and filters,
#. **DLR Thrower**: A service for delivering acknowledgement receipts back to third party applications
   through HTTP, refer to :doc:`/apis/ja-http/index` for more details,
#. **DeliverSM Thrower**: A service for delivering MO SMS (Mobile originated) to third party applications
   through HTTP, refer to :doc:`/apis/ja-http/index` for more details,
#. **HTTP API**: A HTTP Server to be used by third party application to send MT SMS (Mobile Terminated),
   refer to :doc:`/apis/ja-http/index` for more details.
#. **SMPP Server API**: A SMPP Server to be used by third party application to send and receive SMS through
   a stateful tcp protocol, refer to :doc:`/apis/smpp-server/index` for more details.

Jasmin core and its external connectors (used for AMQP, Redis, SMPP, HTTP, Telnet ...) are written in Python 
and are mainly based on `Twisted matrix <https://twistedmatrix.com/>`_, a event-driven networking engine.