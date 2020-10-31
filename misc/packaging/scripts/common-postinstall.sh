#!/bin/bash
set -e

JASMIN_USER="jasmin"
JASMIN_GROUP="jasmin"

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
