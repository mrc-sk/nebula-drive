#!/bin/sh
mkdir -p /var/lib/nebuladrive
chown -R root:root /var/lib/nebuladrive
systemctl daemon-reload
