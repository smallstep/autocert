#!/bin/sh -x

SCRIPT_PATH=/renewer/renew.sh

if [ -e "$SCRIPT_PATH" ]
then
        CMD="--exec=$SCRIPT_PATH"
fi

/bin/bash -xc "step ca renew $CMD --daemon $CRT $KEY"
