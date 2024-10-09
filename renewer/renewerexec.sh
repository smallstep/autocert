#!/bin/sh
cat $CRT $KEY > $PEM
step certificate p12 $P12 $CRT $KEY --no-password --insecure --force