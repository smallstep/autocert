#!/bin/sh

if [ -e /renew/renew.sh ]
then
        CMD="--exec=/renew/renew.sh"
fi

/bin/bash -xc "step ca renew $CMD --daemon $CRT $KEY"

