#!/bin/sh

# Download the root certificate and set permissions
if [ "$DURATION" == "" ];
then
    step ca certificate $COMMON_NAME $CRT $KEY
else
    step ca certificate --not-after $DURATION $COMMON_NAME $CRT $KEY
fi
chmod 644 $CRT $KEY

step ca root $STEP_ROOT
chmod 644 $STEP_ROOT
