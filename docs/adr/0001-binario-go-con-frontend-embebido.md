# 0001 · Un binario Go con el frontend React embebido

- **Estado:** aceptada · 21 de octubre de 2026

## Contexto

La promesa del producto es la sencillez de Adminer (un solo archivo) con una UX moderna, distribuida como imagen Docker que cualquiera levanta con un comando. Necesitamos túneles SSH, TLS con certificados de cliente y streaming de resultados grandes.

## Decisión

- Backend en **Go**: binario estático (`CGO_ENABLED=0`), SSH nativo (`golang.org/x/crypto/ssh`), driver MySQL maduro con TLS, SQLite sin CGO (`modernc.org/sqlite`).
- Frontend en **React + TypeScript + Vite**, compilado y embebido con `go:embed`.
- Imagen final **distroless, no-root**, multi-arquitectura (amd64 y arm64) por compilación cruzada.

## Consecuencias

- ✅ Un artefacto, imagen pequeña (~40 MB), sin intérprete ni shell en producción.
- ✅ La misma base sirve para una futura app de escritorio (Wails).
- ⚠️ Dos lenguajes en el repositorio; contribuir exige Go y Node.
- ⚠️ Las dependencias más recientes exigen Go 1.26+.

## Alternativas descartadas

- **Node.js + TypeScript en todo el stack:** un solo lenguaje, pero imagen ~150 MB y sin binario único.
- **PHP como Adminer:** contradice la idea de imagen mínima y endurecida.
