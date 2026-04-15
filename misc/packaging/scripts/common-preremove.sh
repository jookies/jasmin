#!/bin/bash
set -e

PYPI_NAME="jasmin"
JASMIN_VENV_DIR="/opt/jasmin-sms-gateway/"

# Stop and disable jasmind service
/bin/systemctl stop jasmind
/bin/systemctl disable jasmind
/bin/systemctl daemon-reload

# Remove whole VENV whe jasmin lives
rm -rf "${JASMIN_VENV_DIR}"
