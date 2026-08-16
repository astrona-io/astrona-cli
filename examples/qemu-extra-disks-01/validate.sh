#!/usr/bin/env bash
set -euo pipefail

SCRATCH1=/dev/disk/by-id/virtio-scratch1
SCRATCH2=/dev/disk/by-id/virtio-scratch2

if [ ! -e "${SCRATCH1}-part1" ]; then
  echo "expected a partition on $SCRATCH1 (by-id ${SCRATCH1}-part1 not found)"
  exit 1
fi

if ! mountpoint -q /mnt/scratch2; then
  echo "expected $SCRATCH2 to be mounted at /mnt/scratch2"
  exit 1
fi

FSTYPE=$(lsblk -no FSTYPE "$SCRATCH2")
if [ "$FSTYPE" != "ext4" ]; then
  echo "expected $SCRATCH2 to be ext4, got '$FSTYPE'"
  exit 1
fi

echo "extra disks partitioned/formatted/mounted correctly"
