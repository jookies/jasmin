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

For a production install into ``/opt/jasmin-sms-gateway/venv``:

.. code-block:: bash

   sudo python3.12 -m venv /opt/jasmin-sms-gateway/venv
   sudo /opt/jasmin-sms-gateway/venv/bin/pip install -U pip wheel
   sudo /opt/jasmin-sms-gateway/venv/bin/pip install \
       /home/desktop/projects/jasmin/dist/jasmin-0.12-py3-none-any.whl
   sudo systemctl daemon-reload
   sudo systemctl restart jasmind

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

See also
*********

* :ref:`installation_prerequisites` — system dependencies (RabbitMQ, Redis,
  build headers).
* :doc:`/apis/smpp-server/custom-tlv` — configuration for the TLV parameters
  introduced in 0.12.
