#!/bin/bash
set -e

if [ "$1" = configure ]; then
  # Enable jasmind service
  /bin/systemctl daemon-reload
  /bin/systemctl enable jasmind
fi
