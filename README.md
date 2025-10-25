# goimagetool

Unified image tool (Go). Starter includes:
- CPIO (newc) read/write (+ gzip)
- U‑Boot legacy uImage read/write (CRC checks)
- U‑Boot **FIT/ITB** read/write (MVP)
- In‑memory FS
- Minimal CLI: `load`, `fs` (`ls`, `add`, `extract`), `store`, `info`, `fit` (ls/extract)

> EXT2 RW: scaffolding added (API + stubs), full implementation will follow.

## Build
```bash
go build ./cmd/goimagetool
./goimagetool -h
```
