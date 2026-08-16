#!/usr/bin/env bash
set -euo pipefail

SCRATCH1=/dev/disk/by-id/virtio-scratch1
SCRATCH2=/dev/disk/by-id/virtio-scratch2

# udev can lag the VM's own boot slightly before the by-id symlinks for a
# freshly attached virtio-blk device show up.
for i in $(seq 1 30); do
  [ -e "$SCRATCH1" ] && [ -e "$SCRATCH2" ] && break
  sleep 1
done

# scratch1: partition it (gpt, one partition spanning the disk) but leave it
# unformatted — this is the "practice fdisk/parted/LVM by hand" case, so the
# validation script only checks the partition exists, not what's on it.
sudo parted -s "$SCRATCH1" mklabel gpt mkpart primary ext4 0% 100%
sudo partprobe "$SCRATCH1"

# scratch2: format the whole disk ext4 and mount it — the "already usable
# storage" case.
sudo mkfs.ext4 -F "$SCRATCH2"
sudo mkdir -p /mnt/scratch2
sudo mount "$SCRATCH2" /mnt/scratch2
