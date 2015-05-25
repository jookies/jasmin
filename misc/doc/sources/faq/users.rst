########
User FAQ
########

.. _faq_1_Cnfavtstrj:

Could not find a version that satisfies the requirement jasmin
**************************************************************

Installing Jasmin using **pip** will through this error::

  $ sudo pip install jasmin
  [sudo] password for richard: 
  Downloading/unpacking jasmin
      Could not find a version that satisfies the requirement jasmin (from versions: 0.6b1, 0.6b10, 0.6b11, 0.6b12, 0.6b13, 0.6b14, 0.6b2, 0.6b3, 0.6b4, 0.6b5, 0.6b6, 0.6b7, 0.6b8, 0.6b9)
  Cleaning up...
  No distributions matching the version for jasmin
  Storing debug log for failure in /home/richard/.pip/pip.log

This is common question, since Jasmin is still tagged as a 'Beta' version, pip installation must be done with the **--pre** parameter::

  $ sudo pip install --pre jasmin
  ...

.. hint::
    This is clearly documented in :ref:`installation_linux_steps` installation steps.

.. _faq_1_CcttcasJ:

Cannot connect to telnet console after starting Jasmin
******************************************************

According to the installation guide, Jasmin requires running RabbitMQ and Redis servers, when starting it will wait for these servers to go up.

If you already have these requirements, please check jcli and redis-client logs:

* /var/log/jasmin/redis-client.log
* /var/log/jasmin/jcli.log

.. hint::
    Please check :ref:`installation_prerequisites` before installing.

.. _faq_1_SiemSSHAttpifru:

Should i expose my SMPP Server & HTTP API to the public internet for remote users ?
***********************************************************************************

As a security best practice, place *Jasmin* instance(s) behind a firewall and apply whitelisting rules to only accept users you already know, a better solution is to get VPN tunnels with your users.

If for some reasons you cannot consider these practices, here's a simple iptables configuration that can help to prevent Denial-of-service attacks::

  iptables -I INPUT -p tcp --dport 2775 -m state --state NEW -m recent --set --name SMPP_CONNECT
  iptables -N RULE_SMPP
  iptables -I INPUT -p tcp --dport 2775 -m state --state NEW -m recent --update --seconds 60 --hitcount 3 --name SMPP_CONNECT -j RULE_SMPP
  iptables -A RULE_SMPP -j LOG --log-prefix 'DROPPED SMPP CONNECT ' --log-level 7
  iptables -A RULE_SMPP -j DROP

This will drop any SMPP Connection request coming from the same source IP with more than 3 times per minute ...

.. _faq_1_DJpictd:

Does Jasmin persist its configuration to disk ?
***********************************************

Since everything in Jasmin runs fully in-memory, what will happen if i restart Jasmin or if it crashes for some reason ? how can i ensure my configuration (Connectors, Users, Routes, Filters ...) will be reloaded with the same state they were in before Jasmin goes off ?

Jasmin is doing everything in-memory for performance reasons, and is automatically persisting newly updated configurations every **persistence_timer_secs** seconds as defined in jasmin.cfg file.

.. important:: Set **persistence_timer_secs** to a reasonable value, keep in mind that every disk-access operation will cost you few performance points, and don’t set it too high as you can loose critical updates such as User balance updates.