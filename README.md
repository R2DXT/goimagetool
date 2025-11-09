# goimagetool

Command‑line utility to inspect, modify, and (re)pack common Linux image formats using an in‑memory filesystem (MemFS). Supports auto‑detection or explicit subcommands.

---

## Features

- **Initramfs (CPIO newc)** — read/write; compression: none, gzip, zstd, xz, lz4, bzip2, lzma.
    
- **Tar / Tar.gz** — read (RO) and write (tar/tgz) to/from MemFS.
    
- **U‑Boot**
    
    - legacy **uImage** — read/write (CRC, payload from MemFS).
        
    - **FIT/ITB** — read/write; `ls`, `add`, `rm`, `set-default`, `verify`, `extract`.
        
- **EXT2**
    
    - **Native RO** — directories, files (direct/1‑/2‑/3‑indirect), fast/long symlink, fifo/char/block with major/minor, mode/uid/gid/mtime.
        
    - **RW** — write via `mke2fs` (Unix): correct mode/uid/gid/mtime/special files.
        
- **SquashFS** (Step 2 — complete)
    
    - Read/write, supports uid/gid, symlinks, hard‑links, large dirs, fragments.
        
    - Compression: gzip, xz, zstd, lzo, lz4, lzma (when built with respective options).
        
- **MemFS** — dirs, files, symlinks, char/block/fifo, mode, owners, mtime; `snapshot/walk`.
    
- **Sessions** — persist/restore state (`--session` or `GOIMAGETOOL_SESSION=auto`).
    
- **Raw file helpers** — `image resize` (+K|M|G, −K|M|G, `--to`) and `image pad --align`.
    
- **TUI (two‑panel)** — prototype file manager for MemFS ↔ host.
    

---

## Limitations & Dependencies

- **EXT2 write** requires `mke2fs` (Unix). On Windows, EXT2 is RO only.
    
- **EXT2 read** uses built‑in code (no external tools).
    
- **FIT signing** (RSA/ECDSA) is not supported yet.
    
- **TUI** is experimental.
    

---

## Build

```bash
# Go 1.21+ required
go build ./cmd/goimagetool
./goimagetool -h
```

Cross‑platform: Linux, macOS, Windows, FreeBSD (see limits above).

---

## Quick start

```bash
# Auto‑detect format and compression
./goimagetool load auto path/to/image
./goimagetool fs ls /

# Add a file and repack initramfs
./goimagetool fs add ./busybox /bin/busybox
./goimagetool store initramfs out.cpio.gz gzip
```

Sessions:

```bash
# Auto session in XDG_RUNTIME_DIR
GOIMAGETOOL_SESSION=auto ./goimagetool load auto rootfs.cpio.gz fs ls /

# Explicit session path
./goimagetool --session ~/.cache/goimagetool/session.json load auto rootfs.cpio.gz
./goimagetool --session ~/.cache/goimagetool/session.json fs ls /
```

---

## Commands

### 1) Load images

```bash
# Auto‑detect
./goimagetool load auto <path>

# Initramfs (cpio newc)
./goimagetool load initramfs <path> [auto|none|gzip|zstd|xz|lz4|bzip2|lzma]

# U‑Boot
./goimagetool load kernel-legacy <uImage>
./goimagetool load kernel-fit    <itb> [compression]

# SquashFS
./goimagetool load squashfs <img> [compression]

# EXT2
./goimagetool load ext2 <img> [compression]

# Tar / Tar.gz
./goimagetool load tar <tar|tar.gz> [auto|none|gzip]
```

### 2) Store images

```bash
# Initramfs
./goimagetool store initramfs <out> [none|gzip|zstd|xz|lz4|bzip2|lzma]

# U‑Boot
./goimagetool store kernel-legacy <out.uImage>
./goimagetool store kernel-fit    <out.itb> [compression]

# SquashFS (gzip|xz|zstd|lzo|lz4|lzma)
./goimagetool store squashfs <out.sqsh> <codec>

# EXT2 (1024|2048|4096)
./goimagetool store ext2 <out.ext2> <blockSize> [compression]

# Tar / Tar.gz
./goimagetool store tar <out.tar[.gz]> [none|gzip]
```

### 3) Filesystem (MemFS)

```bash
# List (‑L follows symlinks)
./goimagetool fs ls [/path]
./goimagetool fs ls -L [/path]

# Add host file/dir into image
./goimagetool fs add <hostPath> <dstPathInImage>

# Extract entire image FS to host dir
./goimagetool fs extract <hostDir>

# Create symlink inside image
./goimagetool fs ln -s <target> <dstPathInImage>

# Create special files
./goimagetool fs mknod c <major> <minor> <dst>   # char
./goimagetool fs mknod b <major> <minor> <dst>   # block
./goimagetool fs mknod p 0 0 <dst>               # fifo
```

### 4) FIT/ITB

```bash
# New empty FIT
./goimagetool fit new

# List nodes (* marks default)
./goimagetool fit ls

# Add entry
./goimagetool fit add -t kernel -H sha256 kernel ./zImage

# Set default
./goimagetool fit set-default kernel

# Extract entry
./goimagetool fit extract kernel ./zImage.out

# Verify hashes (all/one)
./goimagetool fit verify
./goimagetool fit verify kernel

# Remove entry
./goimagetool fit rm kernel
```

### 5) Sessions & info

```bash
./goimagetool session save [path]
./goimagetool session load [path]
./goimagetool session clear

./goimagetool info
```

### 6) Raw file helpers

```bash
# Resize raw file
./goimagetool image resize <file> +512M
./goimagetool image resize <file> -256M
./goimagetool image resize <file> --to 2G

# Pad to alignment
./goimagetool image pad <file> --align 1M
```

### 7) TUI (experimental)

```bash
./goimagetool fm            # start from $PWD
./goimagetool fm /var/tmp   # explicit host start dir
```

---

## Examples

**Inspect & patch initramfs:**

```bash
./goimagetool load initramfs initrd.cpio.zst auto
./goimagetool fs add ./tool /usr/bin/tool
./goimagetool fs ln -s /usr/bin/tool /usr/bin/t
./goimagetool store initramfs initrd.new.cpio.gz gzip
```

**Work with FIT:**

```bash
./goimagetool load kernel-fit kernel.itb auto
./goimagetool fit ls
./goimagetool fit add -t fdt -H sha1 dtb ./board.dtb
./goimagetool fit set-default kernel
./goimagetool store kernel-fit kernel.new.itb none
```

**Create EXT2 from a tree:**

```bash
./goimagetool load initramfs /dev/null none   # empty MemFS
./goimagetool fs add ./rootfs /
./goimagetool store ext2 rootfs.ext2 4096 none
```

**Extract tar.gz:**

```bash
./goimagetool load tar rootfs.tar.gz gzip
./goimagetool fs extract ./unpacked
```

---

## CI/CD integration (sketch)

- Lint/tests: `golangci-lint`, `go test ./...`.
    
- Cross‑build matrix: `GOOS/GOARCH` (linux/windows/darwin/freebsd; amd64/arm64).
    
- Example jobs:
    
    - Inspect initramfs/FIT in release pipelines.
        
    - Patch rootfs (add binaries/configs) and repack.
        
    - Enforce size/alignment with `image resize/pad`.
        

---

## Exit codes

- `0` — success
    
- `2` — invalid args/validation error
    
- `>0` — I/O or unsupported operation
    

---

## License

