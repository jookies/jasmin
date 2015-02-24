####################
Programming examples
####################

Subsequent chapters present how to send and receive messages through Jasmin :doc:`/apis/ja-http/index` and some more 
advanced use cases, such as manipulating receipts and complex routings, will look like. 

It is assumed the reader has already installed Jasmin and at least read the :doc:`/apis/ja-http/index` 
and :doc:`/routing/index` chapters and knows enough about Jasmin's architecture/design concepts.

.. _Examples_Sending_SMS:

Sending SMS
***********

Sending a SMS is done through the :doc:`/apis/ja-http/index`:

.. literalinclude:: /apis/ja-http/example_send_gsm0338.py
   :language: python 

In PHP:

.. literalinclude:: /apis/ja-http/example_send_gsm0338.php
   :language: php

In Ruby: 

.. literalinclude:: /apis/ja-http/example_send_gsm0338.rb
   :language: ruby

c.f. :doc:`/apis/ja-http/index` for more details about sending SMS with receipt enquiry, long content 
etc ...

.. _Examples_Receiving_SMS:

Receiving SMS
*************

Receiving a SMS is done through the :doc:`/apis/ja-http/index`, this a PHP script pointed by Jasmin for every 
received SMS (using routing):

.. literalinclude:: example_receive.php
   :language: php

In the above example, there's an error handling where the message is not ACKed if there's a database connection 
problem, if it occurs, the script will return "**Error connecting to DB**" when Jasmin HTTP thrower is waiting for 
a "**ACL/Jasmin**", this will lead to a message re-queue and later re-delivery to the same script, this behaviour is 
explained in :ref:`deliverSmHttpThrower_process`.

Another example of an interactive SMS application:

.. literalinclude:: example_receive_send.php
   :language: php

c.f. :doc:`/apis/ja-http/index` for more details.

.. _Examples_Routing:

Routing
*******

c.f. :ref:`morouter_manager` and :ref:`mtrouter_manager` for routing scenarios.
c.f. :doc:`/routing/index` for details about routing.