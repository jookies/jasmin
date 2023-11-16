#!/bin/bash
set -e

JASMIN_USER="jasmin"
JASMIN_GROUP="jasmin"
JASMIN_VENV_DIR="/opt/jasmin-sms-gateway"
JASMIN_CFG_STORAGE="/var/log/jasmin"
JASMIN_LOG_DIR="/etc/jasmin/store"

PACKAGE_NAME="jasmin-sms-gateway"
PYPI_NAME="jasmin"

function create_dir {
  if [ ! -d ${1} ]
  then
    mkdir ${1}
    chown -R "${JASMIN_USER}:${JASMIN_GROUP}" ${1}
  fi
}

# Provision user/group
getent group ${JASMIN_GROUP} || groupadd -r "${JASMIN_GROUP}"
grep -q "^${JASMIN_USER}:" /etc/passwd || useradd -r -g ${JASMIN_GROUP} \
  -d ${JASMIN_VENV_DIR} -s /sbin/nologin \
  -c "Jasmin SMS Gateway user" ${JASMIN_USER}
  
# If there is any VENV from previous version, just remove it (configs are safe in /etc ) - we do not want any conflicts
if [ -d ${JASMIN_VENV_DIR} ]
then
  rm -rf ${JASMIN_VENV_DIR}
fi

# Create empty directories
create_dir ${JASMIN_CFG_STORAGE}
create_dir ${JASMIN_LOG_DIR}
create_dir ${JASMIN_VENV_DIR}

# Find latest installed python version available on system
LATEST_PYTHON="$(find /usr/bin/ -maxdepth 1 -regex '.*python3\.[0-9]+' | sort --version-sort | tail -n 1)"
# LATEST_PYTHON="$(basename ${LATEST_PYTHON})" # Not needed

# Get installed package version and install the related pypi package(s)
if [ "$(grep -Ei 'debian|buntu' /etc/*release)" ]; then
  PACKAGE_VERSION=$(dpkg -s "${PACKAGE_NAME}"|grep ^Version:|awk -F '[- ]' '{print $2}' | sort --version-sort | tail -n 1)
elif [ "$(grep -Ei 'centos|rhel|fedora|almalinux' /etc/*release)" ]; then
  PACKAGE_VERSION=$(rpm -qi "${PACKAGE_NAME}"|grep ^Version|awk {'print $3'} | sort --version-sort | tail -n 1)
else
  echo "ERROR: Unsupported OS for this package."
  exit 1
fi

# Create VENV and install jasmin and all dependencies from pipy into that VENV
# sudo -u jasmin virtualenv -p ${LATEST_PYTHON}  ${JASMIN_VENV_DIR}/venv
sudo -u "${JASMIN_USER}" ${LATEST_PYTHON} -m venv ${JASMIN_VENV_DIR}/venv
source ${JASMIN_VENV_DIR}/venv/bin/activate
sudo -u "${JASMIN_USER}" ${JASMIN_VENV_DIR}/venv/bin/pip install "${PYPI_NAME}"=="${PACKAGE_VERSION}"

if [ "$1" = configure ]; then
  # Enable jasmind service
  /bin/systemctl daemon-reload
  /bin/systemctl enable jasmind
fi

