# An SSH server for testing Pigkin's remote hosts from an emulator: scripts/ssh-test-host.sh builds and runs it.
FROM debian:trixie-slim

RUN apt-get update \
  && apt-get install -y --no-install-recommends openssh-server bash git ripgrep file procps ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && rm -f /etc/ssh/ssh_host_* \
  && mkdir -p /run/sshd \
  && useradd --create-home --shell /bin/bash pigkin \
  && echo 'pigkin:pigkin' | chpasswd \
  && install -d -m 700 -o pigkin -g pigkin /home/pigkin/.ssh

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /entrypoint.sh

EXPOSE 22
ENTRYPOINT ["/entrypoint.sh"]
