# goimagetool

Single-binary utility to inspect, modify, and (re)pack common Linux image formats using an in-memory filesystem.

## Formats

- **Initramfs (CPIO newc)** — read/write. Compression: none, gzip, zstd, xz, lz4, bzip2, lzma.
    
- **U-Boot legacy (uImage)** — read/write (CRC).
    
- **U-Boot FIT/ITB** — read/write; list/add/remove images, set default, verify, extract.
    
- **EXT2**
    
    - Read: native reader (RO).
        
    - Write: via `mke2fs` (requires `mke2fs` on Unix).
        
- **SquashFS** — load/store with basic options.
    
- **Tar / Tar.gz** — read (RO) and store (tar, tgz).
    
- **MemFS** — internal editable view: files, dirs, symlinks, char/block/fifo, mode/uid/gid/mtime.
    

## Build

`go build ./cmd/goimagetool ./goimagetool -h`

## Session

State can persist across runs.

`# auto session in XDG_RUNTIME_DIR GOIMAGETOOL_SESSION=auto ./goimagetool load auto rootfs.cpio.gz fs ls /  # or explicit ./goimagetool --session ~/.cache/goimagetool/session.json load auto rootfs.cpio.gz ./goimagetool --session ~/.cache/goimagetool/session.json fs ls /`

## Commands

### Load

`# Auto-detect format and compression goimagetool load auto path/to/image  # Initramfs (cpio newc), compression: auto|none|gzip|zstd|xz|lz4|bzip2|lzma goimagetool load initramfs rootfs.cpio.gz auto  # U-Boot goimagetool load kernel-legacy uImage goimagetool load kernel-fit   kernel.itb auto  # SquashFS goimagetool load squashfs rootfs.sqsh auto  # EXT2 goimagetool load ext2 rootfs.ext2 none  # Tar / Tar.gz goimagetool load tar rootfs.tar.gz gzip`

### Store

`# Initramfs goimagetool store initramfs out.cpio.gz gzip  # U-Boot goimagetool store kernel-legacy out.uImage goimagetool store kernel-fit    out.itb none  # SquashFS (compression: gzip|xz|zstd|lz4|lzo|lzma) goimagetool store squashfs out.sqsh zstd  # EXT2 (block size: 1024|2048|4096; requires mke2fs on Unix) goimagetool store ext2 out.ext2 4096 none  # Tar / Tar.gz goimagetool store tar out.tar.gz gzip`

### Filesystem (MemFS)

`# List (optionally follow symlinks with -L) goimagetool fs ls / goimagetool fs ls -L /lib  # Add from host into image goimagetool fs add ./busybox /bin/busybox  # Extract image FS to host directory goimagetool fs extract ./out_rootfs  # Create symlink in image goimagetool fs ln -s /bin/busybox /bin/sh  # Create device/fifo in image goimagetool fs mknod c 1 3  /dev/null goimagetool fs mknod b 8 0  /dev/sda goimagetool fs mknod p 0 0  /run/myfifo`

### FIT/ITB

`# Start empty FIT goimagetool fit new  # List entries (mark * is default) goimagetool fit ls  # Add image with type/hash goimagetool fit add -t kernel -H sha256 kernel /path/to/zImage  # Set default entry goimagetool fit set-default kernel  # Extract entry to file goimagetool fit extract kernel zImage.out  # Verify hashes (all or one) goimagetool fit verify goimagetool fit verify kernel  # Remove entry goimagetool fit rm kernel`

### Sessions

`goimagetool session save goimagetool session load goimagetool session clear`

### Info

`goimagetool info`

### TUI (two-panel, experimental)

`# Optional file manager goimagetool fm             # host start = $PWD goimagetool fm /var/tmp    # explicit host start`

### Raw image size helpers

`# Resize raw file: +K|M|G, -K|M|G, or --to SIZE[K|M|G] goimagetool image resize /path/to/image.img +512M goimagetool image resize /path/to/image.img -256M goimagetool image resize /path/to/image.img --to 2G  # Pad to alignment (--align SIZE[K|M|G]) goimagetool image pad /path/to/image.img --align 1M`

## Examples

Inspect and repack initramfs:

`goimagetool load initramfs initrd.cpio.zst auto goimagetool fs ls /sbin goimagetool fs add ./mytool /usr/bin/mytool goimagetool store initramfs initrd.new.cpio.gz gzip`

Modify FIT:

`goimagetool load kernel-fit kernel.itb auto goimagetool fit ls goimagetool fit add -t fdt -H sha1 dtb /path/board.dtb goimagetool fit set-default kernel goimagetool store kernel-fit kernel.new.itb none`

Create EXT2 from a tree:

`goimagetool load initramfs /dev/null none      # start empty goimagetool fs add ./rootfs_tree / goimagetool store ext2 rootfs.ext2 4096 none   # requires mke2fs`

Extract tar.gz quickly:

`goimagetool load tar rootfs.tar.gz gzip goimagetool fs extract ./unpacked`

## Notes

- EXT2 write uses `mke2fs` (Unix). On Windows, EXT2 write is unavailable.
    
- EXT2 read has a native RO path; write via `mke2fs`.
    
- SquashFS support is basic (works for common cases).
    
- TUI is experimental.
    
