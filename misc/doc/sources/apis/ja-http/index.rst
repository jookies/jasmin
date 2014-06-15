#######################
Using http API: ja-http
#######################

This document is targeted at software designers/programmers wishing to integrate SMS messaging as a function 
into their applications, e.g. in connection with WEB-server, unified messaging, information services etc..

Introduction
============
SMS Messages can be transmitted using HTTP protocol, the following requirements must be met to enable the service :

 * You need a Jasmin user account
 * You need sufficient credit on your Jasmin user account [1]_

.. note:: The ABCs:

   - **MT** is referred to Mobile Terminated, a SMS-MT is an SMS sent to mobile
   - **MO** is referred to Mobile Originated, a SMS-MO is an SMS sent from mobile

What You Can Do with the API
============================
The ja-http API allows you to send and receive SMS through Jasmin's connectors.
Receive http callbacks for delivery notification when SMS-MT is received (or not) on mobile station, send and receive long 
(more than 160 characters) SMS, unicode content and receive http callbacks when a mobile station send you a SMS-MO.

.. toctree::
   :titlesonly:

   sending-mt
   receiving-mo
   receiving-dlr
   
.. rubric:: Footnotes
.. [1] Account management is planned in v0.5, including user credit and privilege management