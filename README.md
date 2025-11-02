# goimagetool

Unified image tool (Go).

## Features
- **Initramfs (CPIO newc)** — read/write, compression: `auto/none/gzip/zstd/lz4/lzma/bzip2/xz`.
- **U-Boot uImage (legacy)** — read/write (header, CRC, payload).
- **U-Boot FIT/ITB** — CRUD + write (MVP): `fit new/ls/add/rm/set-default/extract`.
- **SquashFS v4** — read/write via `go-diskfs` (gzip/xz/zstd/lz4/lzo/lzma), preserves `mode/mtime`, best-effort `uid/gid` (Unix).
- **EXT2** — write (fsck-clean) via `mke2fs -d`, preserves `mode/mtime`, best-effort `uid/gid`.
- **In-memory FS (memfs)** — files/dirs/symlinks/devnodes; helpers:
  - `fs ls` (**`-L` follow symlinks**), `fs add`, `fs extract`, `fs ln -s`, `fs mknod`.
- **Sessions** — persist state between runs: `--session` or `GOIMAGETOOL_SESSION`.

> Cross-platform build: Linux / macOS / Windows / FreeBSD.  
> Note: EXT2 write requires **`mke2fs`** (package *e2fsprogs*) on the host.

---

## Install / Build

```bash
go build ./cmd/goimagetool
./goimagetool -h
```

Optional deps:
- EXT2 write: `sudo apt-get install -y e2fsprogs` (Linux) / `brew install e2fsprogs` (macOS).

---

## Quick start

### Explore an initramfs (cpio.gz)
```bash
./goimagetool --session   load initramfs rootfs.cpio.gz auto   fs ls /
```

Follow symlinks (e.g. `/lib -> /usr/lib`):
```bash
./goimagetool --session fs ls -L /lib
```

### Add a file and repack
```bash
./goimagetool --session   fs add ./ssh/authorized_keys /root/.ssh/authorized_keys   store initramfs out.cpio.gz gzip
```

### SquashFS round-trip
```bash
./goimagetool --session   load squashfs rootfs.squashfs   fs ls /etc   store squashfs out.sqsh zstd
```

### Build an EXT2 image (fsck-clean)
```bash
./goimagetool --session   load initramfs rootfs.cpio.gz auto   store ext2 out.ext2 4096

# optional validation (Linux):
e2fsck -fn out.ext2
```

### FIT/ITB basics
```bash
# create empty FIT, add ramdisk, set default, write ITB
./goimagetool --session fit new
./goimagetool --session fit add ramdisk ./out.cpio.gz
./goimagetool --session fit set-default ramdisk
./goimagetool --session store kernel-fit kernel.itb none

# list/extract from existing ITB
./goimagetool --session load kernel-fit kernel.itb auto
./goimagetool --session fit ls
./goimagetool --session fit extract ramdisk ./ramdisk.cpio.gz
```

### Extract entire FS to host
```bash
./goimagetool --session fs extract ./out_dir
```

---

## CLI reference

```
load  initramfs <path> [compression]           # compression: auto|none|gzip|zstd|lz4|lzma|bzip2|xz
load  kernel-legacy <uImage>
load  kernel-fit    <itb> [compression]
load  squashfs      <img> [compression]
load  ext2          <img> [compression]

store initramfs     <out> [compression]
store kernel-legacy <out>
store kernel-fit    <out> [compression]
store squashfs      <out> [compression]        # gzip|xz|zstd|lz4|lzo|lzma
store ext2          <out> [blockSize] [compression]  # blockSize: 1024|2048|4096

fs ls [-L] [path]
fs add <src> <dst-in-image>
fs extract <dst-dir>
fs ln -s <target> <dst>
fs mknod <c|b|p> <major> <minor> <dst>

fit new | ls | add <name> <file> | rm <name> | set-default <name> | extract <name> <out>

session save [path] | load [path] | clear
info
```

Sessions:
- `--session` without path → default location.
- Or set `GOIMAGETOOL_SESSION=/path/to/session`.

---

## CI usage (snippets)

Inject a file into initramfs:
```bash
GOIMAGETOOL_SESSION=/tmp/gt.session ./goimagetool load initramfs artifacts/initramfs.cpio.gz auto   fs add ./files/sshd_config /etc/ssh/sshd_config   store initramfs dist/initramfs.patched.cpio.gz gzip
```

Replace ramdisk in a FIT image:
```bash
GOIMAGETOOL_SESSION=/tmp/gt.session ./goimagetool load kernel-fit artifacts/kernel.itb auto   fit add ramdisk dist/initramfs.patched.cpio.gz   store kernel-fit dist/kernel.patched.itb none
```
