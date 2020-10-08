###########
RESTful API
###########

The RESTful API allows developers to expand and build their apps on Jasmin. The API makes it easy to send messages to one or many destinations, check balance and routing, as well as *enabling bulk messaging*.

This API is built on the `Falcon web framework <http://falcon.readthedocs.io/en/stable/>`_ and relying on a standard WSGI architecture, this makes it simple and scalable.

If you need to use a stateful tcp protocol (**SMPP v3.4**), please refer to :doc:`/apis/smpp-server/index`.

SMS Messages can be transmitted using the RESTful api, the following requirements must be met to enable the service:

 * You need a Jasmin user account
 * You need sufficient credit on your Jasmin user account

.. _restapi-installaton:

Installation
************

The RESTful API's made available starting from **v0.9rc16**, it can be launched as a system service, so simply start it by typing::

  sudo systemctl start jasmin-restapi

.. note:: The RESTful API works on Ubuntu16.04 and CentOS/RHEL 7.x out of the box, some requirements may be installed manually if you are using older Ubuntu distributions.

If you are not using rpm/deb packages to install Jasmin then that systemd service may not be installed on your system, you still can launch the RESTful API manually::

  celery -A jasmin.protocols.rest.tasks worker -l INFO -c 4 --autoscale=10,3
  twistd -n --pidfile=/tmp/twistd-web-restapi.pid web --wsgi=jasmin.protocols.rest.api

Configuration file for Celery and the Web server can be found in **/etc/jasmin/rest-api.py.conf**.

.. note:: You may also use any other WSGI server for better performance, eg: gunicorn with parallel workers ...

.. _restapi-services:

Services
========

The Services resource represents all web services currently available via Jasmin's RESTful API.

.. list-table:: RESTful services
   :header-rows: 1

   * - Method
     - Service
     - Description / Notes
   * - POST
     - :ref:`/secure/send <restapi-POST_send>`
     - Send a single message to one destination address.
   * - POST
     - :ref:`/secure/sendbatch <restapi-POST_sendbatch>`
     - Send multiple messages to one or more destination addresses.
   * - GET
     - :ref:`/secure/balance <restapi-GET_balance>`
     - Get user account's balance and quota.
   * - GET
     - :ref:`/secure/rate <restapi-GET_rate>`
     - Check a route and it's rate.
   * - GET
     - :ref:`/ping <restapi-GET_ping>`
     - A simple check to ensure this is a Jasmin API.

.. _restapi-auth:

Authentication
**************

Services having the **/secure/** path (such as :ref:`restapi-POST_send` and :ref:`restapi-GET_rate`) require authentication using `Basic Auth <https://en.wikipedia.org/wiki/Basic_access_authentication>`_ which transmits Jasmin account credentials as username/password pairs, encoded using base64.

Example::

  curl -X GET -H 'Authorization: Basic Zm9vOmJhcg==' http://127.0.0.1:8080/secure/balance

We have passed the base64 encoded credentials through the **Authorization** header, '*Zm9vOmJhcg==*' is the encoded username:password pair ('*foo:bar*'), you can use any `tool <https://www.base64encode.org/>`_ to base64 encode/decode.

If wrong or no authentication credentials are provided, a **401 Unauthorized** error will be returned.

.. _restapi-POST_send:

Send a single message
*********************

Send a single message to one destination address.

Definition::

  http://<jasmin host>:<rest api port>/secure/send

Parameters are the same as :ref:`the old http api <http_request_parameters>`.

Examples:

.. code-block:: bash

  curl -X POST -H 'Authorization: Basic Zm9vOmJhcg==' -d '{
    "to": 19012233451,
    "from": "Jookies",
    "content": "Hello",
    "dlr": "yes",
    "dlr-url": "http://192.168.202.54/dlr_receiver.php",
    "dlr-level": 3
  }' http://127.0.0.1:8080/secure/send

.. note:: Do not include **username** and **password** in the parameters, they are already provided through the :ref:`Authorization header <restapi-auth>`.

Result Format:

.. code-block:: json

  {"data": "Success \"c723d42a-c3ee-452c-940b-3d8e8b944868"}

If successful, response header HTTP status code will be **200 OK** and and the message will be sent, the *message id* will be returned in **data**.

.. _restapi-POST_sendbatch:

Send multiple messages
**********************

Send multiple messages to one or more destination addresses.

Definition::

  http://<jasmin host>:<rest api port>/secure/sendbatch

Example of sending same message to multiple destinations:

.. code-block:: bash

  curl -X POST -H 'Authorization: Basic Zm9vOmJhcg==' -d '{
    "messages": [
      {
        "to": [
          "33333331",
          "33333332",
          "33333333"
        ],
        "content": "Same content goes to 3 numbers"
      }
    ]
  }' http://127.0.0.1:8080/secure/sendbatch

Result Format:

.. code-block:: json

  {"data": {"batchId": "af268b6b-1ace-4413-b9d2-529f4942fd9e", "messageCount": 3}}

If successful, response header HTTP status code will be **200 OK** and and the messages will be sent, the *batch id* and total *message count* will be returned in **data**.

.. _restapi-POST_sendbatch_params:

.. list-table:: POST /secure/sendbatch json parameters
   :header-rows: 1

   * - Parameter
     - Example(s)
     - Presence
     - Description / Notes
   * - **messages**
     - [{"to": 1, "content": "hi"}, {"to": 2, "content": "hello"}]
     - Mandatory
     - A Json list of messages, every message contains
       the :ref:`/secure/send <restapi-POST_send>` parameters
   * - **globals**
     - {"from": "Jookies"}
     - Optional
     - May contain any global message parameter, c.f. :ref:`examples <restapi-POST_sendbatch_ex>`
   * - **batch_config**
     - {"callback_url": "http://127.0.0.1:7877", "schedule_at": "2017-11-15 09:00:00"}
     - Optional
     - May contain the following parameters: callback_url or/and errback_url (used for batch tracking in real time c.f. :ref:`examples <restapi-POST_callbacks>`), schedule_at (used for scheduling sendouts c.f. :ref:`examples <restapi-POST_scheduling>`).

.. note:: The Rest API server has an advanced QoS control to throttle pushing messages back to Jasmin, you may fine-tune it through the **http_throughput_per_worker** and **smart_qos** parameters.

.. _restapi-binary_messages:

Send binary messages
********************

Sending binary messages can be done using :ref:`single <restapi-POST_send>` or :ref:`batch <restapi-POST_sendbatch>`
messaging APIs.

It's made possible by replacing the **content** parameter by the **hex_content**, the latter shall contain your binary
data hex value.

Example of sending a message with coding=8:

.. code-block:: bash

  curl -X POST -H 'Authorization: Basic Zm9vOmJhcg==' -d '{
    "to": 19012233451,
    "from": "Jookies",
    "coding": 8,
    "hex_content": "0623063106460628"
  }' http://127.0.0.1:8080/secure/send

The **hex_content** used in the above example is the UTF16BE encoding of arabic word "أرنب" ('\x06\x23\x06\x31\x06\x46\x06\x28').

Same goes for sending batches with binary data:

.. code-block:: bash

  curl -X POST -H 'Authorization: Basic Zm9vOmJhcg==' -d '{
    "messages": [
      {
        "to": [
          "33333331",
          "33333332",
          "33333333"
        ],
        "hex_content": "0623063106460628"
      }
    ]
  }' http://127.0.0.1:8080/secure/sendbatch

.. _restapi-POST_sendbatch_ex:

Usage examples:
===============

The ref:`parameter <restapi-POST_sendbatch_params>` listed above can be used in many ways to setup a sendout batch, we're going to list some use cases to show the flexibility of these parameters:

*Example 1, send different messages to different numbers::*

.. code-block:: json

  {
    "messages": [
      {
        "from": "Brand1",
        "to": [
          "55555551",
          "55555552",
          "55555553"
        ],
        "content": "Message 1 goes to 3 numbers"
      },
      {
        "from": "Brand2",
        "to": [
          "33333331",
          "33333332",
          "33333333"
        ],
        "content": "Message 2 goes to 3 numbers"
      },
      {
        "from": "Brand2",
        "to": "7777771",
        "content": "Message 3 goes to 1 number"
      }
    ]
  }

*Example 2, using global vars:*

From the previous Example (#1) we used the same "from" address for two different messages (**"from": "Brand2"**), in the below example
we're going to make the "from" a global variable, and we are asking for level3 dlr for all sendouts:

.. code-block:: json

  {
    "globals" : {
      "from": "Brand2",
      "dlr-level": 3,
      "dlr": "yes",
      "dlr-url": "http://some.fancy/url"
    }
    "messages": [
      {
        "from": "Brand1",
        "to": [
          "55555551",
          "55555552",
          "55555553"
        ],
        "content": "Message 1 goes to 3 numbers"
      },
      {
        "to": [
          "33333331",
          "33333332",
          "33333333"
        ],
        "content": "Message 2 goes to 3 numbers"
      },
      {
        "to": "7777771",
        "content": "Message 3 goes to 1 number"
      }
    ]
  }

So, **globals** are vars to be inherited in **messages**, we still can force a *local* value in some messages like the **"from": "Brand1"** in the above example.

*Example 3, using callbacks:*

As :ref:`explained <restapi-POST_callbacks>`, Jasmin is enqueuing a sendout batch everytime you call **/secure/sendbatch**,
the batch job will run and call Jasmin's http api to deliver the messages, since this is running in background you can ask
for success or/and error callbacks to follow the batch progress.

.. code-block:: json

  {
    "batch_config": {
      "callback_url": "http://127.0.0.1:7877/successful_batch",
      "errback_url": "http://127.0.0.1:7877/errored_batch"
	},
    "messages": [
      {
        "to": [
          "55555551",
          "55555552",
          "55555553"
        ],
        "content": "Hello world !"
      },
      {
        "to": "7777771",
        "content": "Holà !"
      }
    ]
  }

.. _restapi-POST_callbacks:

About callbacks:
================

The RESTful api is a wrapper around Jasmin's http api, it relies on `Celery task queue <http://www.celeryproject.org/>`_
to process long running batches.

When you launch a batch, the api will enqueue the sendouts through Celery and return a **batchId**, that's the Celery task id.

Since the batch will be executed in background, the API provides a convenient way to follow its progression through two different
callbacks passed inside the batch parameters:

.. code-block:: json

  {
    "batch_config": {
      "callback_url": "http://127.0.0.1:7877/successful_batch",
      "errback_url": "http://127.0.0.1:7877/errored_batch"
	},
    "messages": [
      {
        "to": "7777771",
        "content": "Holà !"
      }
    ]
  }

The **callback_url** will be called (GET) everytime a message is successfuly sent, otherwise the **errback_url** is called.

In both callbacks the following parameters are passed:

.. list-table:: Batch callbacks parameters
   :header-rows: 1

   * - Parameter
     - Example(s)
     - Description / Notes
   * - **batchId**
     - 50a4581a-6e46-48a4-b617-bbefe7faa3dc
     - The batch id
   * - **to**
     - 1234567890
     - The **to** parameter identifying the destination number
   * - **status**
     - 1
     - 1 or 0, indicates the status of a message sendout
   * - **statusText**
     - Success "07033084-5cfd-4812-90a4-e4d24ffb6e3d"
     - Extra text for the **status**


.. _restapi-POST_scheduling:

About batch scheduling:
=======================

It is possible to schedule the launch of a batch, the api will enqueue the sendouts through Celery and return a **batchId** while
deferring message deliveries to the scheduled date & time.

.. code-block:: json

  {
    "batch_config": {
      "schedule_at": "2017-11-15 09:00:00"
	},
    "messages": [
      {
        "to": "7777771",
        "content": "Good morning !"
      }
    ]
  }

The above batch will be scheduled for the 15th of November 2017 at 9am, the Rest API will consider it's local server time to make the delivery, so please make sure it's accurate to whatever timezone you're in.

It's possible to use another **schedule_at** format:

.. code-block:: json

  {
    "batch_config": {
      "schedule_at": "86400s"
	},
    "messages": [
      {
        "to": "7777771",
        "content": "Good morning !"
      }
    ]
  }

The above batch will be scheduled for delivery in 1 day from now (86400 seconds = 1 day).

.. _restapi-GET_balance:

Balance check
*************

Get user account’s balance and quota.

Definition::

  http://<jasmin host>:<rest api port>/secure/balance

Parameters are the same as :ref:`the old http api <http_balance_request_parameters>`.

Examples:

.. code-block:: bash

  curl -X GET -H 'Authorization: Basic Zm9vOmJhcg==' http://127.0.0.1:8080/secure/balance

.. note:: Do not include **username** and **password** in the parameters, they are already provided through the :ref:`Authorization header <restapi-auth>`.

Result Format:

.. code-block:: json

  {"data": {"balance": "10.23", "sms_count": "ND"}}

If successful, response header HTTP status code will be **200 OK**, the *balance* and the *sms count* will be returned in **data**.

.. _restapi-GET_rate:

Route check
***********

Check a route and it’s rate.

Definition::

  http://<jasmin host>:<rest api port>/secure/rate

Parameters are the same as :ref:`the old http api <http_rate_request_parameters>`.

Examples:

.. code-block:: bash

  curl -X GET -H 'Authorization: Basic Zm9vOmJhcg==' http://127.0.0.1:8080/secure/rate?to=19012233451

.. note:: Do not include **username** and **password** in the parameters, they are already provided through the :ref:`Authorization header <restapi-auth>`.

Result Format:

.. code-block:: json

  {"data": {"submit_sm_count": 1, "unit_rate": 0.02}}

If successful, response header HTTP status code will be **200 OK**, the *message rate* and "pdu count" will be returned in **data**.

.. _restapi-GET_ping:

Ping
****

A simple check to ensure this is a responsive Jasmin API, it is used by third party apps like Web campaigners, cluster service checks, etc ..

Definition::

  http://<jasmin host>:<rest api port>/ping

Examples:

.. code-block:: bash

  curl -X GET http://127.0.0.1:8080/ping

Result Format:

.. code-block:: json

  {"data": "Jasmin/PONG"}

If successful, response header HTTP status code will be **200 OK** and a static "Jasmin/PONG" value in **data**.
