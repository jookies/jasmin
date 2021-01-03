#!/bin/bash
set -e

JASMIN_USER="jasmin"
JASMIN_GROUP="jasmin"

PACKAGE_NAME="jasmin-sms-gateway"
PYPI_NAME="jasmin"

# Get installed package version and install the related pypi package(s)
if [ "$(grep -Ei 'debian|buntu' /etc/*release)" ]; then
  PACKAGE_VERSION=$(dpkg -s "$PACKAGE_NAME"|grep ^Version:|awk '{print $2}')
  pip3 install "$PYPI_NAME"=="$PACKAGE_VERSION"
elif [ "$(grep -Ei 'centos|rhel|fedora' /etc/*release)" ]; then
  PACKAGE_VERSION=$(rpm -qi "$PACKAGE_NAME"|grep ^Version|awk {'print $3'})
  pip3 install "$PYPI_NAME"=="$PACKAGE_VERSION"
else
  echo "ERROR: Unsupported OS for this package."
  exit 1
fi

# Provision user/group
getent group $JASMIN_GROUP || groupadd -r "$JASMIN_GROUP"
grep -q "^${JASMIN_USER}:" /etc/passwd || useradd -r -g $JASMIN_GROUP \
  -d /usr/share/jasmin -s /sbin/nologin \
  -c "Jasmin SMS Gateway user" $JASMIN_USER

# Change owner of required folders
chown -R "$JASMIN_USER:$JASMIN_GROUP" /etc/jasmin/store/
chown -R "$JASMIN_USER:$JASMIN_GROUP" /var/log/jasmin

if [ "$1" = configure ]; then
  # Enable jasmind service
  /bin/systemctl daemon-reload
  /bin/systemctl enable jasmind
fi

# python3-falcon package is not available in centos/rhel 8
# this is a workaround
if [ "$(grep -Ei 'centos|rhel|fedora' /etc/*release)" ]; then
  pip3 install falcon==2.0.0
fi
