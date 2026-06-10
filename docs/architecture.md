# Architecture Draft

## Split

- frontend: web UI only
- unified-server: Go process, owns task execution, device control, template management, OCR/OpenCV matching, WebSocket push
- adapter-rs: standalone Rust service for upstream protocol adaptation

## Why

The old local API chain between business process and OpenCV/OCR service costs CPU and memory because screenshots are serialized repeatedly.

## New direction

1. Vue communicates with Go over HTTP and WebSocket.
2. Go keeps screenshot bytes in process and executes OpenCV/OCR matching locally.
3. Rust adapter remains standalone so upstream protocol changes stay isolated.
4. Final packaging can focus on Go binary + frontend static files + Rust adapter binary.

## Migration order

1. scaffold this repo
2. move template model and debug panel contracts into Go
3. port task loop and device control into Go
4. port adapter protocol into Rust
5. cut over frontend from old workspace