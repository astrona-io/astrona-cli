# qemu-extra-disks-01 — attaching blank scratch disks

Shows `runtime.qemu.extraDisks`: attaching one or more additional blank
disks to a qemu VM alongside its main (base-image-backed) disk — useful for
labs that need to practice partitioning, LVM, or filesystem formatting
without ever touching the VM's own root filesystem.

See [`docs/prerequisites.md`](docs/prerequisites.md) for what needs to be on
`PATH` — same qemu toolchain as [`qemu-basics-01`](../qemu-basics-01),
nothing extra.

## The config

```yaml
runtime:
  qemu:
  - image: {...}
    diskSizeGB: 10       # the main disk — root filesystem, from the base image
    extraDisks:
      - sizeGB: 5
        serial: "scratch1"
      - sizeGB: 2
        format: "raw"
        serial: "scratch2"
```

- `sizeGB` is required per disk — an extra disk has no base image to inherit
  a size from, unlike the main disk's `diskSizeGB`.
- `format` is the host-side qcow2/raw image file astrona stores the disk as
  — not a guest filesystem. `""` (default) is `qcow2` (sparse); `raw`
  pre-allocates the full size up front. Either way the guest sees a
  completely blank block device, no partition table.
- `serial`, if set, is exposed to the guest as the virtio-blk device's
  serial number, so a script can target a stable
  `/dev/disk/by-id/virtio-<serial>` path instead of relying on `/dev/vdb`,
  `/dev/vdc`, ... enumeration order (not guaranteed stable once there's more
  than one extra disk).

Extra disks attach in list order after the main disk and the cloud-init seed
— `/dev/vdb` is the first `extraDisks` entry, `/dev/vdc` the second, and so
on — but scripts here use the `serial`-based `by-id` path instead of
depending on that order directly.

## What this lab does

`bootstrap/prepare-disks.sh` waits for both scratch disks' `by-id` symlinks
to appear, then:

- partitions `scratch1` (gpt, one partition spanning the disk) and leaves it
  unformatted — the "practice fdisk/parted/LVM by hand" case
- formats `scratch2` ext4 and mounts it at `/mnt/scratch2` — the "already
  usable storage" case

`validation/validate.sh` checks both: the partition exists on `scratch1`,
and `scratch2` is mounted ext4. In a real lab you'd swap `prepare-disks.sh`
for whatever setup the exercise needs and have the student do the
partitioning/formatting themselves during the lab, with validation checking
their work — this example does it all in bootstrap purely to demonstrate
the disks attach and behave as expected end to end.

## Running it

```sh
go build -o astrona .
./astrona run -c examples/qemu-extra-disks-01
./astrona submit -c examples/qemu-extra-disks-01
./astrona ssh qemu-extra-disks-01   # lsblk, look around
./astrona destroy -c examples/qemu-extra-disks-01
```

Inside the VM:

```sh
lsblk
# NAME   ... SIZE
# vda    ...  10G   <- main disk (root filesystem)
# vdb    ...   5G   <- extraDisks[0] "scratch1", partitioned
# └─vdb1
# vdc    ...   2G   <- extraDisks[1] "scratch2", ext4, mounted at /mnt/scratch2

ls -l /dev/disk/by-id/ | grep virtio
```

Every extra disk is as disposable as the main one: recreated blank on every
`astrona run`, deleted with the rest of the VM's state dir
(`~/.astrona/qemu/qemu-extra-disks-01`) on `astrona destroy` — nothing
written to a scratch disk survives a teardown.
