# PDD Go Rust

A new lightweight architecture skeleton that splits the system into:

- frontend: Vue 3 + Vite web UI
- unified-server: Go business server with built-in OCR/OpenCV modules
- adapter-rs: Rust Axum adapter service

## Goals

- remove Electron packaging
- avoid image transfer between local services during business execution
- keep adapter as a standalone service
- move OCR and OpenCV into one Go process
- use WebSocket between Vue frontend and Go backend

## Current status

This directory contains the initial scaffold only. Go and Rust toolchains are required locally to build the backend services.

## Start on Windows

- `start-adapter.bat`: start Rust adapter on `127.0.0.1:8091`
- `start-unified-server.bat`: start Go business server on `127.0.0.1:8080`
- `start-frontend.bat`: start Vue dev server on `127.0.0.1:5173`
- `start-dev-all.bat`: start all three in separate terminal windows

## Docs

- `docs/ai-handoff.md`: current status, legacy protocol notes, migration plan, pending work
- `docs/adapter-apipost.md`: adapter and unified-server Apipost examples
- `docs/architecture.md`: architecture draft
