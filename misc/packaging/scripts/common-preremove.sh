#!/bin/bash
set -e

PYPI_NAME="jasmin"

pip3 uninstall -y "$PYPI_NAME"

# Stop and disable jasmind service
/bin/systemctl stop jasmind
/bin/systemctl disable jasmind
/bin/systemctl daemon-reload

# python3-falcon package is not available in centos/rhel 8
# this is a workaround
if [ "$(grep -Ei 'centos|rhel|fedora' /etc/*release)" ]; then
  pip3 uninstall -y falcon==2.0.0
fi
