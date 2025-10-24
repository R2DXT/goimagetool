# goimagetool

Unified image tool (Go). Starter includes:
- CPIO (newc) read/write (+ gzip)
- U‑Boot legacy uImage read/write (CRC checks)
- In‑memory FS
- Minimal CLI: `load`, `fs` (`ls`, `add`, `extract`), `store`, `info`

## Build
```bash
go build ./cmd/goimagetool
./goimagetool -h
```
