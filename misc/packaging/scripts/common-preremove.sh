#!/bin/bash
set -e

if [ "$1" = remove ]; then
  # Stop and disable jasmind service
  /bin/systemctl stop jasmind
  /bin/systemctl disable jasmind
  /bin/systemctl daemon-reload
fi
