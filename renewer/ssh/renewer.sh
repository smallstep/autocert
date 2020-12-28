#!/bin/sh

set -e

if [ "$STEP_HOST" == "" ];
then
    KEY=$USER_KEY
else
    KEY=$HOST_KEY
fi

while true; do
  sleep $(expr $RENEWAL_SEC + $RANDOM % $RENEWAL_JITTER_SEC);
  step ssh renew -f $KEY-cert.pub $KEY;
done;
