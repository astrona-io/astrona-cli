#!/usr/bin/env bash
# Run the terminal acceptance suite inside a Fedora VM with SELinux enforcing.
# GitHub's Ubuntu runners use AppArmor, and a container shares the runner's
# kernel, so a Fedora host can only be had as a KVM guest. Run from the repo
# root on a runner with KVM, qemu, genisoimage, and astrona, kind, kubectl and
# uv already built or installed; the report lands in ./oat-report.
set -euo pipefail
engine="${1:?usage: oat-fedora-vm.sh <docker|podman-rootful|podman-rootless>}"
case "$engine" in docker|podman-rootful|podman-rootless) ;; *) echo "Unsupported engine: $engine" >&2; exit 1;; esac

# Fedora Cloud Base, pinned to the SHA-256 from the release's CHECKSUM file.
image_url="https://download.fedoraproject.org/pub/fedora/linux/releases/45/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-45-1.4.x86_64.qcow2"
image_sha256="50bdc5e70f6a24f540932d15411c1c6ae9fcfa40fb990d876d83c5cc823b2ad0"

root="$(pwd)"
work="${RUNNER_TEMP:-$(mktemp -d)}/fedora-vm"
mkdir -p "$work"

curl -fsSL --retry 5 --retry-all-errors -o "$work/base.qcow2" "$image_url"
echo "$image_sha256  $work/base.qcow2" | sha256sum --check
qemu-img create -q -f qcow2 -F qcow2 -b "$work/base.qcow2" "$work/disk.qcow2" 40G

ssh-keygen -q -t ed25519 -N '' -f "$work/key"
cat > "$work/user-data" <<EOF
#cloud-config
users:
  - name: oat
    groups: [wheel]
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys: [$(cat "$work/key.pub")]
EOF
printf 'instance-id: oat-fedora\nlocal-hostname: oat-fedora\n' > "$work/meta-data"
genisoimage -quiet -output "$work/seed.iso" -volid cidata -joliet -rock "$work/user-data" "$work/meta-data"

# Leave the runner a little memory of its own; kind needs the rest.
memory_mb=$(( $(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo) - 3072 ))
qemu-system-x86_64 -enable-kvm -cpu host -smp "$(nproc)" -m "$memory_mb" \
  -drive file="$work/disk.qcow2",if=virtio -drive file="$work/seed.iso",media=cdrom \
  -netdev user,id=net0,hostfwd=tcp:127.0.0.1:2222-:22 -device virtio-net-pci,netdev=net0 \
  -display none -serial file:"$work/serial.log" -daemonize -pidfile "$work/vm.pid"

ssh_opts=(-i "$work/key" -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
  -o LogLevel=ERROR -o ConnectTimeout=5 -o ServerAliveInterval=30)
guest() { ssh "${ssh_opts[@]}" oat@127.0.0.1 "$@"; }

for _ in $(seq 60); do guest true 2>/dev/null && break; sleep 5; done
if ! guest true; then
  echo "::error::the Fedora VM did not answer on SSH"
  tail -50 "$work/serial.log"
  exit 1
fi
guest 'sudo cloud-init status --wait >/dev/null; cat /etc/fedora-release; getenforce'

# The binaries come from the runner; the guest gets the engines from Fedora.
for bin in kind kubectl uv; do
  path="$(command -v "$bin")"
  guest "sudo tee /usr/local/bin/$bin >/dev/null && sudo chmod 755 /usr/local/bin/$bin" < "$path"
done
tar -C "$root" --exclude=.git -cz . | guest 'mkdir -p astrona-cli && tar -xz -C astrona-cli'

fetch_report() {
  guest 'tar -C astrona-cli -cz oat-report 2>/dev/null' | tar -xz -C "$root" || true
}
trap fetch_report EXIT

# Copied over and run as a file: a command reading stdin would otherwise eat
# the rest of the script.
guest 'cat > oat-run.sh' <<'GUEST'
set -euo pipefail
engine="$1"
if [ "$(getenforce)" != Enforcing ]; then
  echo "::error::SELinux is $(getenforce) in the Fedora VM; this suite needs it enforcing"
  exit 1
fi
sudo dnf install -y -q podman moby-engine python3 >/dev/null
# kind's nodes need the legacy iptables modules loaded by root.
sudo modprobe -a ip_tables iptable_nat ip6_tables ip6table_nat || true

case "$engine" in
  docker)
    sudo systemctl enable --now docker.service
    # Fedora's package runs dockerd with SELinux labelling; the bug this
    # suite guards against only exists with it on.
    if ! sudo docker info --format '{{.SecurityOptions}}' | grep -q selinux; then
      echo '{"selinux-enabled": true}' | sudo tee /etc/docker/daemon.json
      sudo systemctl restart docker.service
    fi
    sudo docker info --format 'Docker {{.ServerVersion}}: {{.SecurityOptions}}'
    sudo usermod -aG docker oat ;;
  podman-rootless)
    # kind needs every cgroup controller delegated to the user's systemd.
    sudo mkdir -p /etc/systemd/system/user@.service.d
    printf '[Service]\nDelegate=yes\n' | sudo tee /etc/systemd/system/user@.service.d/delegate.conf >/dev/null
    sudo systemctl daemon-reload
    sudo loginctl enable-linger oat
    sudo systemctl restart "user@$(id -u).service"
    export XDG_RUNTIME_DIR="/run/user/$(id -u)" DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$(id -u)/bus"
    until [ -S "/run/user/$(id -u)/bus" ]; do sleep 1; done
    cat "/sys/fs/cgroup/user.slice/user-$(id -u).slice/user@$(id -u).service/cgroup.controllers" ;;
esac
podman --version

cd astrona-cli
robot=(uv run --with-requirements acceptance/requirements.txt
  robot --include terminal --variable "ENGINE:$engine" --metadata "Host:Fedora, SELinux enforcing"
  --outputdir oat-report --xunit xunit.xml acceptance/suites)
status=0
case "$engine" in
  # sg picks up the docker group added in this session.
  docker) sg docker -c "KIND_EXPERIMENTAL_PROVIDER=docker $(printf '%q ' "${robot[@]}")" || status=$? ;;
  podman-rootful) sudo env "PATH=$PATH" KIND_EXPERIMENTAL_PROVIDER=podman "${robot[@]}" || status=$? ;;
  podman-rootless) systemd-run --user --scope --quiet -p Delegate=yes \
    env KIND_EXPERIMENTAL_PROVIDER=podman "${robot[@]}" || status=$? ;;
esac
sudo chown -R "$(id -u):$(id -g)" oat-report 2>/dev/null || true
if [ "$status" -ne 0 ]; then
  echo "== engine state after the failure"
  case "$engine" in
    docker) sudo docker ps -a || true ;;
    podman-rootful) sudo podman ps -a || true ;;
    *) podman ps -a || true ;;
  esac
  echo "== SELinux denials"
  sudo ausearch -m AVC -ts recent 2>/dev/null | tail -40 || true
fi
exit "$status"
GUEST
guest bash oat-run.sh "$engine"
