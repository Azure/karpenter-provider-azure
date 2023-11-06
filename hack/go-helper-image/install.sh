#!/bin/sh
set -e

if [ ! -d /dbg ]; then
    echo "Error: installation requires a volume mount at /dbg" 1>&2
    exit 1
fi

echo "Installing runtime debugging support files in /dbg"
tar cf - -C /duct-tape . | tar xf - -C /dbg
echo "Installation complete"
