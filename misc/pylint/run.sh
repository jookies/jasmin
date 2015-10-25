#!/bin/bash

pylint --rcfile=./misc/pylint/pylintrc jasmin|tee /dev/stderr > /tmp/pylint-outcome
RATE=$(grep "Your code has been rated" /tmp/pylint-outcome |awk '{print $7}'|grep -o '[0-9]\+.[0-9]\+')

if [ $(echo "$RATE > 8" |bc) -eq 1 ]; then
  exit 0
else
  exit 1
fi
