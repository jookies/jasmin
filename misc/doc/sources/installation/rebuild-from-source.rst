.. _rebuild_from_source:

##########################
Rebuilding from source
##########################

After pulling code changes (or modifying the source tree locally), use the steps
below to rebuild the ``jasmin`` Python wheel and reinstall it into a virtual
environment.

These instructions target the supported Python baseline (**3.11+**, with
**3.12 as the primary GA target**). They assume the project is checked out at
``/home/desktop/projects/jasmin``; substitute your own path as needed.

Prerequisites
*************

* **Python 3.12** available on ``$PATH`` (``python3.12 --version`` returns
  ``3.12.x``). On Ubuntu, install via the ``deadsnakes`` PPA:

  .. code-block:: bash

     sudo add-apt-repository -y ppa:deadsnakes/ppa
     sudo apt update
     sudo apt install -y python3.12 python3.12-venv python3.12-dev

* System build headers: ``libffi-dev``, ``libssl-dev``, ``gcc``.

.. important::
   Do **not** run ``sudo python3 setup.py install``. ``setup.py install`` is
   deprecated since setuptools 58 and removed in setuptools 80+; it does not
   install transitive dependencies and will leave you with broken ``attrs`` /
   ``tomli`` errors. Always use ``pip install`` of the built wheel.

Step 1 — Clean previous build artifacts
****************************************

From the project root, remove stale build directories and the existing venv so
the rebuild starts from a known-good state:

.. code-block:: bash

   cd /home/desktop/projects/jasmin
   rm -rf .venv dist build *.egg-info

Step 2 — Create a fresh Python 3.12 virtual environment
********************************************************

.. code-block:: bash

   python3.12 -m venv .venv
   .venv/bin/pip install -U pip wheel build

Step 3 — Build the wheel
*************************

.. code-block:: bash

   .venv/bin/python -m build --wheel

On success, you will see a line similar to::

   Successfully built jasmin-0.12-py3-none-any.whl

The wheel is written to ``dist/``.

Step 4 — Install the wheel into the venv
*****************************************

.. code-block:: bash

   .venv/bin/pip install dist/jasmin-0.12-py3-none-any.whl

Pip will resolve and install all runtime dependencies (Twisted, falcon,
cryptography, smpp.pdu3, txAMQP3, python-messaging, etc.) into the venv.

Step 5 — Smoke test
********************

Verify the entry points resolve and load without errors:

.. code-block:: bash

   .venv/bin/jasmind.py --help
   .venv/bin/dlrd.py --help
   .venv/bin/interceptord.py --help

Each command must exit ``0`` and print its options summary.

Step 6 (optional) — Install system-wide
****************************************

For a production install into ``/opt/jasmin-sms-gateway/venv`` (the path the
bundled systemd units expect):

.. code-block:: bash

   sudo python3.12 -m venv /opt/jasmin-sms-gateway/venv
   sudo /opt/jasmin-sms-gateway/venv/bin/pip install -U pip wheel
   sudo /opt/jasmin-sms-gateway/venv/bin/pip install \
       /home/desktop/projects/jasmin/dist/jasmin-0.12-py3-none-any.whl

   # Configuration files
   sudo mkdir -p /etc/jasmin/{resource,store} /var/log/jasmin
   sudo cp misc/config/*.cfg /etc/jasmin/
   sudo cp misc/config/jasmind.environment /etc/jasmin/
   sudo cp misc/config/resource/* /etc/jasmin/resource/
   sudo chown -R jasmin:jasmin /etc/jasmin /var/log/jasmin

   # systemd units (shipped under misc/config/systemd/)
   sudo cp misc/config/systemd/*.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl restart jasmind

.. important::
   ``jasmind.service`` reads JCLI credentials via
   ``EnvironmentFile=/etc/jasmin/jasmind.environment``. The default file ships
   ``JCLI_USERNAME='jcliadmin'`` / ``JCLI_PASSWORD='jclipwd'`` — **change these
   before exposing the JCLI port to anything beyond localhost**. The variables
   are consumed by ``jasmin/protocols/cli/factory.py`` and override the
   built-in defaults.

.. note::
   ``jasmind.service`` declares
   ``Requires=jasmin-interceptord jasmin-deliversmd jasmin-dlrd jasmin-dlrlookupd``,
   so a single ``systemctl restart jasmind`` will start the supporting daemons
   automatically. Use ``systemctl status jasmind jasmin-dlrd jasmin-dlrlookupd
   jasmin-deliversmd jasmin-interceptord`` to verify all five are active.

Step 7 (optional) — Rebuild the Docker image
*********************************************

If you deploy via Docker, rebuild the image from the updated source:

.. code-block:: bash

   docker build -t jasmin-sms-gateway:0.12 \
       -f docker/Dockerfile .
   docker compose up -d --force-recreate jasmin

One-shot rebuild (Steps 1–5 combined)
**************************************

.. code-block:: bash

   cd /home/desktop/projects/jasmin \
     && rm -rf .venv dist build *.egg-info \
     && python3.12 -m venv .venv \
     && .venv/bin/pip install -U pip wheel build \
     && .venv/bin/python -m build --wheel \
     && .venv/bin/pip install dist/jasmin-0.12-py3-none-any.whl \
     && .venv/bin/jasmind.py --help

Troubleshooting
****************

* **"No module named 'distutils'"** when running the system
  ``python3.12 -m pip`` — Ubuntu's shared ``/usr/lib/python3/dist-packages/pip``
  is not compatible with Python 3.12+ (distutils was removed by PEP 594).
  Bootstrap a fresh pip with ``sudo python3.12 -m ensurepip --upgrade`` or just
  use the venv's ``pip`` as shown above.
* **Wheel builds to ``UNKNOWN-0.0.0.whl``** — this happens when an older
  setuptools (pre-PEP 621) tries to build from ``pyproject.toml``. Ensure the
  venv has ``setuptools >= 68`` (``.venv/bin/pip install -U setuptools``) or
  install the pre-built wheel directly (wheels have metadata baked in, no
  build step required).
* **``pkg_resources.DistributionNotFound``** at runtime — you installed with
  ``setup.py install`` instead of ``pip install``. Remove the leftover eggs
  from ``/usr/local/lib/python3.X/dist-packages/`` and reinstall using pip.
* **``attrs >= 21.3.0 required``** — same root cause as the ``pkg_resources``
  error above; use pip, not ``setup.py install``.
* **``jasmind.service: Failed to load environment files: No such file or directory``** —
  ``/etc/jasmin/jasmind.environment`` is missing. Copy it from
  ``misc/config/jasmind.environment`` in the source tree and re-run
  ``sudo systemctl daemon-reload && sudo systemctl restart jasmind``.
* **jcli login rejects ``jcliadmin`` / ``jclipwd``** — the credentials in
  ``/etc/jasmin/jasmind.environment`` take precedence over the built-in
  defaults. Check the current values of ``JCLI_USERNAME`` / ``JCLI_PASSWORD``
  there, or edit the file and restart ``jasmind``.

* **``Cannot start RouterPB: ChannelClosed ... PRECONDITION_FAILED -
  inequivalent arg 'type' for exchange 'messaging'``** — a previous
  install (or another app sharing the broker) declared the ``messaging`` or
  ``billing`` exchange in RabbitMQ with a type other than ``topic``. Jasmin
  refuses to redeclare-and-override; the channel closes with AMQP error 406
  and ``jasmind`` aborts startup.

  Two fixes:

  **Option 1 — Delete the stale exchanges** *(simplest; OK on dev / freshly
  provisioned brokers)*:

  .. code-block:: bash

     sudo systemctl stop jasmind
     sudo rabbitmqctl delete_exchange messaging
     sudo rabbitmqctl delete_exchange billing
     sudo rabbitmqctl delete_queue RouterPB_deliver_sm_all
     sudo rabbitmqctl delete_queue RouterPB_bill_request_submit_sm_resp_all
     sudo systemctl start jasmind

  Jasmin will re-declare both exchanges as ``topic`` on the next start.
  Unprocessed messages in those queues are lost; persistent state (routes,
  users, groups) lives in Jasmin's own config files and is unaffected.

  **Option 2 — Move Jasmin to a dedicated vhost** *(non-destructive;
  recommended when the broker is shared with other apps)*:

  .. code-block:: bash

     sudo rabbitmqctl add_vhost /jasmin
     sudo rabbitmqctl set_permissions -p /jasmin guest ".*" ".*" ".*"

  Then edit ``/etc/jasmin/jasmin.cfg`` and set:

  .. code-block:: ini

     [amqp-broker]
     vhost = /jasmin

  Default is ``/`` (see ``jasmin/queues/configs.py``). Apply the same
  ``vhost`` override to any ancillary config file that contains an
  ``[amqp-broker]`` section (``rest-api.cfg``, ``dlrd.cfg``, ``dlrlookupd.cfg``,
  ``deliversm.cfg``, ``interceptor.cfg``), then restart ``jasmind``.

  Verify afterwards:

  .. code-block:: bash

     sudo rabbitmqctl list_exchanges name type | grep -E "messaging|billing"

  Both should report type ``topic``.

See also
*********

* :ref:`installation_prerequisites` — system dependencies (RabbitMQ, Redis,
  build headers).
* :doc:`/apis/smpp-server/custom-tlv` — configuration for the TLV parameters
  introduced in 0.12.
