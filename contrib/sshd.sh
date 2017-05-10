#!/bin/bash

SSHDCFG="$PWD/sshd.cfg"

# Directory and shell binary both have to be owned by root,
# both require file permission 775 or lower.
BINARY="/gin-repo/gin-shell"

if [ ! -x "$BINARY" ]; then
   echo "$BINARY does not exist (or is not executable)"
   exit -1
fi

HOSTKEY="$PWD/ssh_host_rsa_key"

if [ ! -e "$HOSTKEY" ]; then
    ssh-keygen -b 4096 -t rsa -f "$HOSTKEY" -P ""
fi

SSHD=`which sshd`

PORT="22222"
USER=`whoami`

cat << EOF > "$SSHDCFG"
Port $PORT
AddressFamily inet
HostKey $HOSTKEY
UsePrivilegeSeparation yes
AuthorizedKeysCommand $BINARY --keys  %u "%t %k"
AuthorizedKeysCommandUser $USER
UsePam no
PidFile $PWD/sshd.pid
EOF

AUTHKEYS="$PWD/ssh_authorized_keys"
echo "command .. $BINARY"
echo "sshd ..... $SSHD"
echo "pwd ...... $PWD"
echo "cfg ...... $SSHDCFG"
echo "host key . $HOSTKEY"
echo "port ..... $PORT"
echo "user ..... $USER"

"$SSHD" -De -f "$SSHDCFG"
