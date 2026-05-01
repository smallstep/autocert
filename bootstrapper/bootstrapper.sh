#!/bin/sh


if [ -f "$STEP_ROOT" ] && [ -f "$CRT" ] && [ -f "$KEY" ];
then
    echo "Found existing $STEP_ROOT, $CRT, and $KEY, skipping bootstrap"
    exit 0
fi

# Download the root certificate and set permissions
step ca root $STEP_ROOT

if [ "$DURATION" == "" ];
then
    step ca certificate $COMMON_NAME $CRT $KEY
else
    step ca certificate --not-after $DURATION $COMMON_NAME $CRT $KEY
fi
cat $CRT $KEY > $PEM
step certificate p12 $P12 $CRT $KEY --no-password --insecure --force

if [ -n "$OWNER" ]
then
    chown "$OWNER" $CRT $KEY $STEP_ROOT $P12 $PEM
fi

if [ -n "$MODE" ]
then
    chmod "$MODE" $CRT $KEY $STEP_ROOT $P12 $PEM
else
    chmod 644 $CRT $KEY $STEP_ROOT $P12 $PEM
fi
