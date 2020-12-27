#!/bin/sh

set -e

# Download the root certificate and set permissions
if [ "$STEP_HOST" == "" ];
then
    KEY=$USER_KEY
else
    KEY=$HOST_KEY
fi

step ca bootstrap -f

step ssh certificate $KEY_ID $KEY --insecure --no-password -f
chmod 644 $KEY $KEY.pub $KEY-cert.pub

unset STEP_TOKEN
unset STEP_HOST

STEP_HOST=false step ssh config --roots > $USER_CA
STEP_HOST=true step ssh config --roots > $HOST_CA
chmod 644 $USER_CA $HOST_CA
