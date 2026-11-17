#!/bin/sh
# Starts sshd in the foreground. Host keys are made on first start, so rebuilding the container gives it new ones,
# which is how the "host key changed" case is tested.
set -eu

ssh-keygen -A >/dev/null

if [ "${PIGKIN_PASSWORD_AUTH:-0}" = 1 ]; then
  sed -i 's/^PasswordAuthentication no$/PasswordAuthentication yes/' /etc/ssh/sshd_config
fi

# A host without bash, to check that Pigkin says bash is missing instead of failing obscurely.
if [ "${PIGKIN_NO_BASH:-0}" = 1 ]; then
  usermod --shell /bin/sh pigkin
  rm -f /bin/bash /usr/bin/bash
fi

# Added only when missing, since `start` runs this again and keys added with `authorize` must survive it.
keys=/home/pigkin/.ssh/authorized_keys
if [ -n "${PIGKIN_AUTHORIZED_KEY:-}" ] && ! grep -qxF "$PIGKIN_AUTHORIZED_KEY" "$keys" 2>/dev/null; then
  printf '%s\n' "$PIGKIN_AUTHORIZED_KEY" >>"$keys"
  chown pigkin:pigkin "$keys"
  chmod 600 "$keys"
fi

exec /usr/sbin/sshd -D -e
